package remote

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/2SSK/tops/internal/wire"
)

func TestCmdUpload(t *testing.T) {
	// The string in TRD "Helper upload and invocation", with the directory filled in.
	want := `sudo -n sh -c 'set -e; d=/usr/local/lib/tops/0123456789abcdef; mkdir -p -m 0755 "$d"; cat > "$d/tops.$$"; chmod 0755 "$d/tops.$$"; mv "$d/tops.$$" "$d/tops"'`
	if got := cmdUpload(sha); got != want {
		t.Errorf("cmdUpload =\n%s\nwant\n%s", got, want)
	}
}

// fakeNode plays Ubuntu 24.04 with sudo, sh and whatever helpers were uploaded: the upload
// command stores stdin's sha256, and an installed helper answers hello with it.
type fakeNode struct {
	machine string
	lie     string // a sha256 hello reports instead of the real one
	failSh  bool   // the upload's sh fails (disk full)

	mu        sync.Mutex
	installed map[string]string // sha16 -> sha256 of the bytes uploaded
}

var helperPath = regexp.MustCompile(`^sudo -n /usr/local/lib/tops/([0-9a-f]{16})/tops node hello$`)

func (f *fakeNode) exec(cmd string, stdin io.Reader, stdout, stderr io.Writer) uint32 {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case cmd == cmdArch || cmd == cmdSudo:
		return ubuntu(f.machine, true)(cmd, stdin, stdout, stderr)
	case strings.HasPrefix(cmd, "sudo -n sh -c "):
		h := sha256.New()
		_, _ = io.Copy(h, stdin)
		sum := hex.EncodeToString(h.Sum(nil))
		if f.failSh || cmd != cmdUpload(sum) {
			_, _ = io.WriteString(stderr, "sh: 1: cannot create /usr/local/lib/tops/x/tops.tmp: No space left on device\n")
			return 2
		}
		f.installed[sum[:16]] = sum
		return 0
	case helperPath.MatchString(cmd):
		_, _ = io.Copy(io.Discard, stdin)
		sum, ok := f.installed[helperPath.FindStringSubmatch(cmd)[1]]
		if !ok {
			_, _ = io.WriteString(stderr, "sudo: "+strings.Fields(cmd)[2]+": command not found\n")
			return 1
		}
		if f.lie != "" {
			sum = f.lie
		}
		res, _ := json.Marshal(wire.Hello{Hostname: "vps-01", SHA256: sum})
		_ = json.NewEncoder(stdout).Encode(wire.Response{OK: true, Result: res})
		return 0
	}
	_, _ = io.WriteString(stderr, "sh: 1: unexpected command\n")
	return 127
}

// helperFile writes a stand-in helper binary and returns its path and sha256.
func helperFile(t *testing.T, dir, name, content string) (string, string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil { //nolint:gosec // an executable
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(content))
	return path, hex.EncodeToString(sum[:])
}

// connectEnv is a fake node and a Dialer whose executable is a linux/amd64 stand-in helper.
type connectEnv struct {
	node *fakeNode
	srv  *testServer
	d    *Dialer
	sum  string // the stand-in helper's sha256
}

func newConnectEnv(t *testing.T, machine string) *connectEnv {
	t.Helper()
	e := &connectEnv{node: &fakeNode{machine: machine, installed: map[string]string{}}}
	var cfg Config
	e.srv, cfg = newNode(t, e.node.exec)
	d, err := NewDialer(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	d.exe, e.sum = helperFile(t, t.TempDir(), "tops", "helper build 1")
	d.goos, d.goarch = "linux", "amd64"
	e.d = d
	return e
}

func (e *connectEnv) connect(t *testing.T) (*Node, error) {
	t.Helper()
	n, err := e.d.Connect(t.Context(), "vps-01", "127.0.0.1", wire.Request{RunID: "r-1", By: "ssk@thinkpad"})
	if err == nil {
		t.Cleanup(func() { _ = n.Close() })
	}
	return n, err
}

// TestConnectCommands is H2: every command tops sends a node, in order, on a bare node and
// then on one that has the helper. Nothing else may ever appear here.
func TestConnectCommands(t *testing.T) {
	e := newConnectEnv(t, "x86_64")
	n, err := e.connect(t)
	if err != nil {
		t.Fatal(err)
	}
	if !n.Uploaded || n.Arch != "amd64" || n.SHA256 != e.sum || n.Hello.Hostname != "vps-01" {
		t.Errorf("node = %+v", n)
	}
	hello := "sudo -n /usr/local/lib/tops/" + e.sum[:16] + "/tops node hello"
	bare := []string{"uname -m", "sudo -n true", hello, cmdUpload(e.sum), hello}
	if got := e.srv.commands(); !slices.Equal(got, bare) {
		t.Fatalf("bare node: commands =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(bare, "\n"))
	}

	n2, err := e.connect(t)
	if err != nil {
		t.Fatal(err)
	}
	if n2.Uploaded {
		t.Error("the second connect uploaded again")
	}
	again := append(bare, "uname -m", "sudo -n true", hello)
	if got := e.srv.commands(); !slices.Equal(got, again) {
		t.Fatalf("second connect: commands =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(again, "\n"))
	}
}

// TestRunCallers is the static half of H2: inside this package (run is unexported, so no
// other package can call it) every command passed to run is one of the fixed constants or
// built by cmdHelper or cmdUpload.
func TestRunCallers(t *testing.T) {
	calls := runCalls(t)
	if len(calls) == 0 {
		t.Fatal("found no calls to run: the test is looking in the wrong place")
	}
	for pos, cmd := range calls {
		if !fixedCommand(cmd) {
			t.Errorf("%s: run is given a command that is not one of the fixed strings", pos)
		}
	}
}

// runCalls finds every x.run(ctx, cmd, ...) in the package's non-test files and returns
// cmd by position.
func runCalls(t *testing.T) map[string]ast.Expr {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	calls := map[string]ast.Expr{}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(node ast.Node) bool {
			if call, ok := node.(*ast.CallExpr); ok {
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "run" && len(call.Args) > 1 {
					calls[fset.Position(call.Pos()).String()] = call.Args[1]
				}
			}
			return true
		})
	}
	return calls
}

// fixedCommand reports whether cmd is cmdArch, cmdSudo, or a call to cmdHelper or cmdUpload.
func fixedCommand(cmd ast.Expr) bool {
	switch cmd := cmd.(type) {
	case *ast.Ident:
		return cmd.Name == "cmdArch" || cmd.Name == "cmdSudo"
	case *ast.CallExpr:
		fn, ok := cmd.Fun.(*ast.Ident)
		return ok && (fn.Name == "cmdHelper" || fn.Name == "cmdUpload")
	}
	return false
}

func TestConnectArm64FromAmd64(t *testing.T) {
	e := newConnectEnv(t, "aarch64")
	// this tops is linux/amd64, so the arm64 helper must be the one beside it
	arm, sum := helperFile(t, filepath.Dir(e.d.exe), "tops-linux-arm64", "helper build 1 for arm64")
	n, err := e.connect(t)
	if err != nil {
		t.Fatal(err)
	}
	if n.Arch != "arm64" || n.SHA256 != sum {
		t.Errorf("node = %s %s, want arm64 %s from %s", n.Arch, n.SHA256, sum, arm)
	}
}

func TestConnectNoBinary(t *testing.T) {
	e := newConnectEnv(t, "aarch64") // and nothing beside the executable
	_, err := e.connect(t)
	ue := userError(t, err, "helper.no_binary")
	if !strings.Contains(ue.What, "linux/arm64") || ue.Fix != "make build-linux" {
		t.Errorf("error = %+v", ue)
	}
	if got := e.srv.commands(); slices.ContainsFunc(got, func(c string) bool { return strings.HasPrefix(c, "sudo -n sh") }) {
		t.Errorf("uploaded with no binary: %q", got)
	}
}

func TestConnectWrongSHA(t *testing.T) {
	e := newConnectEnv(t, "x86_64")
	e.node.lie = strings.Repeat("f", 64)
	_, err := e.connect(t)
	ue := userError(t, err, "helper.failed")
	if !strings.Contains(ue.Why, e.node.lie) || !strings.Contains(ue.Why, e.sum) {
		t.Errorf("Why = %q, want both sha256s", ue.Why)
	}
}

func TestConnectUploadFails(t *testing.T) {
	e := newConnectEnv(t, "x86_64")
	e.node.failSh = true
	_, err := e.connect(t)
	if err == nil || !strings.Contains(err.Error(), "No space left on device") || !strings.Contains(err.Error(), "vps-01") {
		t.Fatalf("err = %v, want the node and sh's message", err)
	}
}

func TestConnectPreflightFails(t *testing.T) {
	e := newConnectEnv(t, "riscv64")
	_, err := e.connect(t)
	if ue := userError(t, err, "helper.no_binary"); !strings.Contains(ue.What, "riscv64") {
		t.Errorf("What = %q, want the machine named", ue.What)
	}
}

func TestConnectUnreachable(t *testing.T) {
	e := newConnectEnv(t, "x86_64")
	_, err := e.d.Connect(t.Context(), "vps-09", "127.0.0.2", wire.Request{})
	if ue := userError(t, err, "ssh.unreachable"); !strings.Contains(ue.What, "vps-09") {
		t.Errorf("What = %q, want the node named", ue.What)
	}
}

func TestBinaryHashedOnce(t *testing.T) {
	e := newConnectEnv(t, "x86_64")
	b1, err := e.d.binary("amd64")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(e.d.exe, []byte("rebuilt mid-run"), 0o755); err != nil { //nolint:gosec // an executable
		t.Fatal(err)
	}
	b2, err := e.d.binary("amd64")
	if err != nil {
		t.Fatal(err)
	}
	if b1 != b2 || b1.sha256 != e.sum {
		t.Errorf("binary = %+v then %+v, want the first hash kept for the whole run", b1, b2)
	}
}

func TestBinaryFor(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "tops")
	arm, _ := helperFile(t, dir, "tops-linux-arm64", "x")
	tests := []struct {
		goos, goarch, arch, want string
	}{
		{"linux", "amd64", "amd64", exe},
		{"linux", "amd64", "arm64", arm},
		{"darwin", "arm64", "arm64", arm}, // a Mac is never linux, whatever the arch
	}
	for _, tt := range tests {
		got, err := binaryFor(exe, tt.goos, tt.goarch, tt.arch)
		if err != nil || got != tt.want {
			t.Errorf("binaryFor(%s/%s, %s) = %q, %v; want %q", tt.goos, tt.goarch, tt.arch, got, err, tt.want)
		}
	}
}
