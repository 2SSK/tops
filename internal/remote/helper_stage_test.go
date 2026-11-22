//go:build helper

package remote

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/2SSK/tops/internal/wire"
)

// releaseArchive is a release .tar.gz whose binaries are shell scripts printing their name
// and version, so the test can run them through the links stage makes.
func releaseArchive(t *testing.T, version string, names ...string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, name := range append([]string{"LICENSE"}, names...) {
		body := "#!/bin/sh\necho " + name + " " + version + "\n"
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// releaseServer serves archives by path and counts the requests.
type releaseServer struct {
	files    map[string][]byte
	requests atomic.Int32
}

func (s *releaseServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.requests.Add(1)
	b, ok := s.files[r.URL.Path]
	if !ok {
		http.NotFound(w, r)
		return
	}
	_, _ = w.Write(b)
}

// serveOnNode serves h on the node's loopback through the SSH connection, a remote forward
// like ssh -R: the node downloads from the workstation without any docker networking.
func serveOnNode(t *testing.T, n *Node, h http.Handler) string {
	t.Helper()
	l, err := n.ssh.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { _ = srv.Close() })
	return "http://" + l.Addr().String()
}

// checkStaged asserts what a fresh stage leaves on the node: a system user with its 0750
// home and no shell, current at the first release, and the binaries on PATH.
func checkStaged(t *testing.T, c *container) {
	t.Helper()
	for cmd, want := range map[string]string{ //nolint:gosec // commands and their output; "passwd" is getent's database, not a credential
		"getent passwd temporal | cut -d: -f6,7":  "/var/lib/temporal:/usr/sbin/nologin",
		"stat -c '%a %U %G' /var/lib/temporal":    "750 temporal temporal",
		"test $(id -u temporal) -lt 1000 && echo": "", // a system user
		"readlink /opt/temporal/current":          "/opt/temporal/releases/1.31.3",
		"ls -A /opt/temporal/releases/1.31.3":     "tdbg\ntemporal-server\ntemporal-sql-tool",
		"tdbg; temporal-sql-tool; temporal":       "tdbg 1.31.3\ntemporal-sql-tool 1.31.3\ntemporal 1.10.0", // through the links, on PATH
	} {
		if got := docker(t, nil, "exec", c.id, "sh", "-c", cmd); got != want {
			t.Errorf("%s = %q, want %q", cmd, got, want)
		}
	}

}

// TestHelperStage: on a bare Ubuntu node, the real useradd and filesystem. A fresh stage
// creates the temporal user and installs the releases, a second changes and downloads
// nothing, and an archive that does not match its sha256 installs nothing.
func TestHelperStage(t *testing.T) {
	c := startContainer(t)
	n, err := c.connect(t, c.dialer(t, c.cfg))
	if err != nil {
		t.Fatal(err)
	}
	server := releaseArchive(t, "1.31.3", "temporal-server", "temporal-sql-tool", "tdbg")
	cli := releaseArchive(t, "1.10.0", "temporal")
	rs := &releaseServer{files: map[string][]byte{"/server.tar.gz": server, "/cli.tar.gz": cli}}
	base := serveOnNode(t, n, rs)
	s := wire.Stage{User: true, Releases: []wire.Download{
		{Kind: "server", Version: "1.31.3", URL: base + "/server.tar.gz", SHA256: hexSum(server), Extract: []string{"temporal-server", "temporal-sql-tool", "tdbg"}},
		{Kind: "cli", Version: "1.10.0", URL: base + "/cli.tar.gz", SHA256: hexSum(cli), Extract: []string{"temporal"}},
	}}

	r, err := n.Stage(t.Context(), s)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Changed) != 7 || r.Changed[0] != "user temporal" {
		t.Errorf("first stage changed %q, want the user, two releases and four links", r.Changed)
	}
	checkStaged(t, c)

	// Done when: a second run changes nothing, and downloads nothing.
	before := rs.requests.Load()
	if r, err := n.Stage(t.Context(), s); err != nil || len(r.Changed) != 0 {
		t.Errorf("second stage changed %q (%v), want nothing", r.Changed, err)
	}
	if got := rs.requests.Load(); got != before {
		t.Errorf("the second stage downloaded %d times", got-before)
	}

	// An archive that is not the one checksums.txt names: release.checksum, nothing installed.
	bad := s.Releases[0]
	bad.Version, bad.SHA256 = "1.31.4", strings.Repeat("0", 64)
	_, err = n.Stage(t.Context(), wire.Stage{Releases: []wire.Download{bad}})
	_ = userError(t, err, "release.checksum")
	if got := strings.Fields(docker(t, nil, "exec", c.id, "ls", "-A", "/opt/temporal/releases")); !slices.Equal(got, []string{"1.31.3"}) {
		t.Errorf("releases after a bad archive = %q, want only 1.31.3 and no leftovers", got)
	}
}
