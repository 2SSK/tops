package cli

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/2SSK/tops/internal/remote"
	"github.com/2SSK/tops/internal/term"
)

func TestNewBuildInfo(t *testing.T) {
	stamped := &debug.BuildInfo{
		GoVersion: "go1.26.8",
		Settings: []debug.BuildSetting{
			{Key: "vcs", Value: "git"},
			{Key: "vcs.revision", Value: "5c8da89f00d4a1b2c3d4e5f60718293a4b5c6d7e"},
		},
	}

	tests := []struct {
		name string
		bi   *debug.BuildInfo
		ok   bool
		want string
	}{
		{"stamped", stamped, true, "tops dev (5c8da89, go1.26.8)"},
		{"no commit", &debug.BuildInfo{GoVersion: "go1.26.8"}, true, "tops dev (unknown, go1.26.8)"},
		{"no build info", nil, false, "tops dev (unknown, )"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := newBuildInfo(tt.bi, tt.ok).String(); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestModuleVersion(t *testing.T) {
	recorded := func(v string) *debug.BuildInfo {
		return &debug.BuildInfo{Main: debug.Module{Version: v}}
	}
	tests := []struct {
		name, stamped string
		bi            *debug.BuildInfo
		ok            bool
		want          string
	}{
		{"go install", "dev", recorded("v0.1.0"), true, "v0.1.0"},
		{"make build wins", "v0.1.0-3-gabc", recorded("v0.1.0"), true, "v0.1.0-3-gabc"},
		{"test binary", "dev", recorded(""), true, "dev"},
		{"plain build", "dev", recorded("(devel)"), true, "dev"},
		{"no build info", "dev", nil, false, "dev"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := moduleVersion(tt.stamped, tt.bi, tt.ok); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestVersionCommand(t *testing.T) {
	var out bytes.Buffer
	cmd := NewRootCommand(&Options{})
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"version"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("version: %v", err)
	}
	if !strings.HasPrefix(out.String(), "tops dev (") {
		t.Errorf("output = %q", out.String())
	}
}

func TestVersionJSON(t *testing.T) {
	var out bytes.Buffer
	cmd := NewRootCommand(&Options{})
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"version", "--json"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("version --json: %v", err)
	}
	var doc versionDocument
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, out.String())
	}
	if doc.Kind != "Version" || doc.TopsVersion != "dev" || doc.Tops.Version != "dev" {
		t.Errorf("document = %+v", doc)
	}
}

func TestWriteNodeVersions(t *testing.T) {
	sum := "c1d975b4e54a0326" + strings.Repeat("0", 48)
	vs := nodeVersions([]*remote.Node{
		{Client: &remote.Client{Node: "vps-01"}, SHA256: sum, Uploaded: true},
		{Client: &remote.Client{Node: "vps-100"}, SHA256: sum},
	})
	var out bytes.Buffer
	if err := writeNodeVersions(&out, vs); err != nil {
		t.Fatal(err)
	}
	want := "vps-01   helper c1d975b4e54a0326  uploaded\n" +
		"vps-100  helper c1d975b4e54a0326  present\n"
	if out.String() != want {
		t.Errorf("output =\n%s\nwant\n%s", out.String(), want)
	}
	b, err := json.Marshal(vs[0])
	if err != nil {
		t.Fatal(err)
	}
	if got := string(b); got != `{"name":"vps-01","helperSha256":"c1d975b4e54a0326","uploaded":true}` {
		t.Errorf("json = %s", got)
	}
}

func TestNewRequest(t *testing.T) {
	now := time.Date(2026, 10, 23, 15, 30, 12, 0, time.FixedZone("IST", 5*3600+1800))
	req := newRequest(&Options{Getenv: func(k string) string { return map[string]string{"USER": "ssk"}[k] }}, now)
	if !regexp.MustCompile(`^r-20261023T100012Z-[0-9a-f]{4}$`).MatchString(req.RunID) {
		t.Errorf("run id = %q, want the UTC time and 4 hex digits", req.RunID)
	}
	if !strings.HasPrefix(req.By, "ssk@") {
		t.Errorf("by = %q, want ssk@<host>", req.By)
	}
	if other := newRequest(&Options{Getenv: func(string) string { return "" }}, now); other.RunID == req.RunID {
		t.Errorf("two runs in the same second share the id %s", req.RunID) // 1 in 65536
	}
}

// versionEnv writes a fleet whose two nodes, at 127.0.0.1 and 127.0.0.2, listen on nothing at
// port, with a real key and an empty known_hosts, and returns its root.
func versionEnv(t *testing.T, port int) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "environments", "qa")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatal(err)
	}
	cluster := strings.NewReplacer(
		"ssh: { user: deploy }", fmt.Sprintf("ssh: { user: deploy, port: %d, key: id_ed25519, knownHosts: known_hosts }", port),
		"10.80.0.11", "127.0.0.1",
		"10.80.0.12", "127.0.0.2",
	).Replace(validSpec)
	files := map[string]string{
		"id_ed25519":   string(pem.EncodeToMemory(block)),
		"known_hosts":  "# no nodes yet\n",
		"cluster.yaml": cluster,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeSecrets(t, dir)
	return root
}

// TestVersionEnvUnreachable: with -e, version connects to every node, and a node it cannot
// reach fails the command naming the first such node in the spec.
func TestVersionEnvUnreachable(t *testing.T) {
	l, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close() // nothing listens there now
	_, err = run(t, versionEnv(t, port), "-e", "qa", "version")
	var ue *term.UserError
	if !errors.As(err, &ue) || ue.Code != "ssh.unreachable" || !strings.Contains(ue.What, "vps-01") {
		t.Fatalf("err = %#v, want ssh.unreachable naming vps-01, the first node", err)
	}
	if got := term.ExitCode(err); got != 1 {
		t.Errorf("exit code = %d, want 1", got)
	}
}

// TestVersionEnvNoKnownHosts: ssh settings are checked before any node is dialled.
func TestVersionEnvNoKnownHosts(t *testing.T) {
	root := versionEnv(t, 22)
	if err := os.Remove(filepath.Join(root, "environments", "qa", "known_hosts")); err != nil {
		t.Fatal(err)
	}
	_, err := run(t, root, "-e", "qa", "version")
	var ue *term.UserError
	if !errors.As(err, &ue) || ue.Code != "ssh.host_key_unknown" {
		t.Fatalf("err = %#v, want ssh.host_key_unknown for the missing file", err)
	}
}

func TestVersionEnvUnknownEnv(t *testing.T) {
	_, err := run(t, fixtureFleet(t), "-e", "nope", "version")
	var ue *term.UserError
	if !errors.As(err, &ue) || ue.Code != "spec.no_env" {
		t.Fatalf("err = %#v, want spec.no_env", err)
	}
}
