package helper

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/2SSK/tops/internal/wire"
)

// archive is a .tar.gz of name -> content; a name ending in / is a directory.
func archive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	for _, name := range slices.Sorted(func(yield func(string) bool) {
		for k := range files {
			if !yield(k) {
				return
			}
		}
	}) {
		hdr := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(files[name])), Typeflag: tar.TypeReg}
		if strings.HasSuffix(name, "/") {
			hdr.Typeflag, hdr.Size = tar.TypeDir, 0
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(files[name])); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func sum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// serverArchive is a Temporal server release as GitHub ships it, plus what stage must never
// take: a path outside the release, a nested file, a link.
func serverArchive(t *testing.T, v string) []byte {
	return archive(t, map[string]string{
		"temporal-server": "server " + v, "temporal-sql-tool": "sql-tool " + v, "./tdbg": "tdbg " + v,
		"LICENSE": "MIT", "config/": "", "config/development.yaml": "x", "../evil": "escaped",
	})
}

// stageNode is a fake node with no temporal user, and a release server; downloads counts the
// archives fetched.
type stageNode struct {
	host      Host
	srv       *httptest.Server
	assets    map[string][]byte
	downloads atomic.Int32
}

func newStageNode(t *testing.T) *stageNode {
	t.Helper()
	root := t.TempDir()
	etc := filepath.Join(root, "etc")
	writeFile(t, filepath.Join(etc, "passwd"), "root:x:0:0:root:/root:/bin/bash\n")
	n := &stageNode{assets: map[string][]byte{}}
	n.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, ok := n.assets[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		n.downloads.Add(1)
		_, _ = w.Write(b)
	}))
	t.Cleanup(n.srv.Close)
	n.host = Host{Root: root, Etc: etc, HTTP: n.srv.Client(), AddUser: func(_ context.Context, name, home string) error {
		f, err := os.OpenFile(filepath.Join(etc, "passwd"), os.O_APPEND|os.O_WRONLY, 0o644) //nolint:gosec // the fake node's passwd
		if err != nil {
			return err
		}
		_, err = f.WriteString(name + ":x:999:999::" + home + ":/usr/sbin/nologin\n")
		_ = f.Close()
		if err != nil {
			return err
		}
		return os.MkdirAll(filepath.Join(root, home), 0o700)
	}}
	return n
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil { //nolint:gosec // test files
		t.Fatal(err)
	}
}

// release serves an archive and returns the Download that stages it.
func (n *stageNode) release(kind, v string, b []byte, extract ...string) wire.Download {
	path := "/" + kind + "/" + v + ".tar.gz"
	n.assets[path] = b
	return wire.Download{Kind: kind, Version: v, URL: n.srv.URL + path, SHA256: sum(b), Extract: extract}
}

func (n *stageNode) server(t *testing.T, v string) wire.Download {
	return n.release("server", v, serverArchive(t, v), "temporal-server", "temporal-sql-tool", "tdbg")
}

func (n *stageNode) cli(t *testing.T, v string) wire.Download {
	return n.release("cli", v, archive(t, map[string]string{"temporal": "cli " + v, "LICENSE": "MIT"}), "temporal")
}

func (n *stageNode) path(p string) string { return filepath.Join(n.host.Root, p) }

func (n *stageNode) readlink(t *testing.T, p string) string {
	t.Helper()
	got, err := os.Readlink(n.path(p))
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// TestStageFresh: a bare node gets the temporal user, the server and CLI releases with only
// their binaries, current, and the tools on PATH.
func TestStageFresh(t *testing.T) {
	n := newStageNode(t)
	r, err := n.host.Stage(t.Context(), wire.Stage{User: true, Releases: []wire.Download{n.server(t, "1.31.3"), n.cli(t, "1.10.0")}})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"user temporal",
		"/opt/temporal/releases/1.31.3",
		"/opt/temporal/current -> /opt/temporal/releases/1.31.3",
		"/usr/local/bin/tdbg -> /opt/temporal/current/tdbg",
		"/usr/local/bin/temporal-sql-tool -> /opt/temporal/current/temporal-sql-tool",
		"/opt/temporal/cli/1.10.0",
		"/usr/local/bin/temporal -> /opt/temporal/cli/1.10.0/temporal",
	}
	if !reflect.DeepEqual(r.Changed, want) {
		t.Errorf("changed =\n%q\nwant\n%q", r.Changed, want)
	}
	if info, err := os.Stat(n.path("/var/lib/temporal")); err != nil || info.Mode().Perm() != 0o750 {
		t.Errorf("home: %v %v", info.Mode(), err)
	}
	entries, _ := os.ReadDir(n.path("/opt/temporal/releases/1.31.3"))
	var names []string
	for _, e := range entries {
		info, _ := e.Info()
		names = append(names, e.Name()+" "+info.Mode().Perm().String())
	}
	if want := []string{"tdbg -rwxr-xr-x", "temporal-server -rwxr-xr-x", "temporal-sql-tool -rwxr-xr-x"}; !reflect.DeepEqual(names, want) {
		t.Errorf("release holds %v, want only the binaries, 0755", names)
	}
	if b, _ := os.ReadFile(n.path("/opt/temporal/releases/1.31.3/temporal-server")); string(b) != "server 1.31.3" {
		t.Errorf("temporal-server = %q", b)
	}
	for _, escaped := range []string{"/opt/temporal/releases/evil", "/opt/temporal/evil", "/opt/temporal/releases/1.31.3/LICENSE"} {
		if _, err := os.Stat(n.path(escaped)); err == nil {
			t.Errorf("%s was extracted: only the wanted binaries may be", escaped)
		}
	}
	assertNoLeftovers(t, n, "/opt/temporal/releases", "/opt/temporal/cli")
}

// assertNoLeftovers: no temporary directory or download is left beside the releases.
func assertNoLeftovers(t *testing.T, n *stageNode, dirs ...string) {
	t.Helper()
	for _, dir := range dirs {
		entries, _ := os.ReadDir(n.path(dir))
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), ".") {
				t.Errorf("%s/%s left behind", dir, e.Name())
			}
		}
	}
}

// TestStageAgain: a second run downloads nothing and changes nothing.
func TestStageAgain(t *testing.T) {
	n := newStageNode(t)
	s := wire.Stage{User: true, Releases: []wire.Download{n.server(t, "1.31.3"), n.cli(t, "1.10.0")}}
	if _, err := n.host.Stage(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	before := n.downloads.Load()
	r, err := n.host.Stage(t.Context(), s)
	if err != nil || len(r.Changed) != 0 || n.downloads.Load() != before {
		t.Errorf("second run: changed %q, %d more downloads, %v", r.Changed, n.downloads.Load()-before, err)
	}
}

// TestStageKeepsCurrent: a newer release is staged beside the running one; current moves only
// through upgrade, never through stage.
func TestStageKeepsCurrent(t *testing.T) {
	n := newStageNode(t)
	for _, v := range []string{"1.31.3", "1.31.4"} {
		if _, err := n.host.Stage(t.Context(), wire.Stage{Releases: []wire.Download{n.server(t, v)}}); err != nil {
			t.Fatal(err)
		}
	}
	if got := n.readlink(t, "/opt/temporal/current"); got != "/opt/temporal/releases/1.31.3" {
		t.Errorf("current -> %s, want the first release", got)
	}
	if _, err := os.Stat(n.path("/opt/temporal/releases/1.31.4/tdbg")); err != nil {
		t.Errorf("1.31.4 not staged: %v", err)
	}
}

// TestStagePrunes: beyond the newest three, releases go, but never current's; versions sort
// numerically (1.31.10 is newer than 1.31.9).
func TestStagePrunes(t *testing.T) {
	n := newStageNode(t)
	var r wire.StageResult
	for _, v := range []string{"1.31.1", "1.31.2", "1.31.9", "1.31.10", "1.32.0"} {
		var err error
		if r, err = n.host.Stage(t.Context(), wire.Stage{Releases: []wire.Download{n.server(t, v)}}); err != nil {
			t.Fatal(err)
		}
	}
	installed, _ := n.host.releases()
	if want := []string{"1.31.1", "1.31.10", "1.31.9", "1.32.0"}; !reflect.DeepEqual(installed, want) {
		t.Errorf("installed %v, want the newest three and current's 1.31.1", installed)
	}
	if !slices.Contains(r.Changed, "removed /opt/temporal/releases/1.31.2") {
		t.Errorf("last run changed %q", r.Changed)
	}
}

// TestStageRefusesBadArchives: a wrong sha256, a missing binary or a failed download installs
// nothing, leaves nothing behind, and says which.
func TestStageRefusesBadArchives(t *testing.T) {
	for _, tt := range []struct {
		name string
		bad  func(n *stageNode, d *wire.Download)
		code string
		msg  string
	}{
		{"checksum", func(_ *stageNode, d *wire.Download) { d.SHA256 = strings.Repeat("0", 64) }, codeChecksum, "does not match checksums.txt"},
		{"missing binary", func(n *stageNode, d *wire.Download) {
			b := archive(t, map[string]string{"temporal-server": "s", "temporal-sql-tool": "q"})
			*d = n.release("server", "1.31.3", b, "temporal-server", "temporal-sql-tool", "tdbg")
		}, codeFailed, "no tdbg in the archive"},
		{"not found", func(_ *stageNode, d *wire.Download) { d.URL += ".missing" }, codeDownload, "download "},
		{"not gzip", func(n *stageNode, d *wire.Download) {
			*d = n.release("server", "1.31.3", []byte("not an archive"), "tdbg")
		}, codeFailed, "read the archive"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			n := newStageNode(t)
			d := n.server(t, "1.31.3")
			tt.bad(n, &d)
			code, resp, _ := run(t, Env{Host: n.host, Root: true}, wire.OpStage, request(t, wire.Stage{Releases: []wire.Download{d}}))
			if code != ExitNotOK || resp.Error == nil || resp.Error.Code != tt.code || !strings.Contains(resp.Error.Message, tt.msg) {
				t.Errorf("exit %d, error %+v, want %s %q", code, resp.Error, tt.code, tt.msg)
			}
			if _, err := os.Stat(n.path("/opt/temporal/releases/1.31.3")); !errors.Is(err, os.ErrNotExist) {
				t.Error("a failed stage left the release installed")
			}
			if _, err := os.Lstat(n.path("/opt/temporal/current")); !errors.Is(err, os.ErrNotExist) {
				t.Error("a failed stage set current")
			}
			assertNoLeftovers(t, n, "/opt/temporal/releases")
		})
	}
}

// request is a stage request on stdin.
func request(t *testing.T, s wire.Stage) string {
	t.Helper()
	args, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(wire.Request{RunID: "r-1", By: "ssk@thinkpad", Args: args})
	return string(b)
}

// TestStageRefusesUnsafeArgs: arguments that could make root write outside the release
// directories are a bad request, before anything is downloaded.
func TestStageRefusesUnsafeArgs(t *testing.T) {
	for name, d := range map[string]wire.Download{
		"kind":    {Kind: "ui", Version: "1.0.0", URL: "https://x/a"},
		"version": {Kind: "server", Version: "../../etc", URL: "https://x/a"},
		"extract": {Kind: "server", Version: "1.31.3", URL: "https://x/a", Extract: []string{"../sbin/init"}},
		"url":     {Kind: "server", Version: "1.31.3", URL: "file:///etc/shadow"},
	} {
		t.Run(name, func(t *testing.T) {
			n := newStageNode(t)
			code, resp, _ := run(t, Env{Host: n.host, Root: true}, wire.OpStage, request(t, wire.Stage{Releases: []wire.Download{d}}))
			if code != ExitBadRequest || resp.Error == nil || resp.Error.Code != codeBadRequest {
				t.Errorf("exit %d, error %+v", code, resp.Error)
			}
			if n.downloads.Load() != 0 {
				t.Error("downloaded before checking")
			}
		})
	}
}

// TestRunStage: the op through Run, with the result on stdout.
func TestRunStage(t *testing.T) {
	n := newStageNode(t)
	code, resp, _ := run(t, Env{Host: n.host, Root: true}, wire.OpStage, request(t, wire.Stage{User: true}))
	var r wire.StageResult
	if code != ExitOK || json.Unmarshal(resp.Result, &r) != nil || !reflect.DeepEqual(r.Changed, []string{"user temporal"}) {
		t.Errorf("exit %d, result %s", code, resp.Result)
	}
}
