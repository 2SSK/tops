package remote

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/2SSK/tops/internal/release"
	"github.com/2SSK/tops/internal/wire"
)

// tarGz builds a release archive holding files, name -> content, as GoReleaser lays it out.
func tarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, name := range []string{"LICENSE", "completions/_tops", "tops"} { // GoReleaser's order
		content, ok := files[name]
		if !ok {
			continue
		}
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
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

func hexSum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// fakeRelease serves v0.2.0's assets the way GitHub does: /<tag>/<asset>.
type fakeRelease struct {
	assets map[string][]byte // asset -> bytes
	hits   atomic.Int32
}

func (r *fakeRelease) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.hits.Add(1)
	b, ok := r.assets[strings.TrimPrefix(req.URL.Path, "/v0.2.0/")]
	if !ok {
		http.NotFound(w, req)
		return
	}
	_, _ = w.Write(b)
}

// newRelease serves a v0.2.0 with linux archives for amd64 and arm64 and a checksums.txt.
func newRelease(t *testing.T, helper string) *fakeRelease {
	t.Helper()
	r := &fakeRelease{assets: map[string][]byte{}}
	var sums strings.Builder
	for _, arch := range []string{"amd64", "arm64"} {
		name := "tops_0.2.0_linux_" + arch + ".tar.gz"
		r.assets[name] = tarGz(t, map[string]string{"LICENSE": "MIT", "completions/_tops": "#compdef", "tops": helper + " " + arch})
		fmt.Fprintf(&sums, "%s  %s\n", hexSum(r.assets[name]), name)
	}
	r.assets["checksums.txt"] = []byte(sums.String())
	return r
}

// releaseDialer is a darwin/arm64 tops v0.2.0 whose releases are served by r.
func releaseDialer(t *testing.T, r *fakeRelease) *Dialer {
	t.Helper()
	srv := httptest.NewTLSServer(r)
	t.Cleanup(srv.Close)
	return &Dialer{
		exe: filepath.Join(t.TempDir(), "tops"), goos: "darwin", goarch: "arm64",
		version: "v0.2.0", cache: t.TempDir(), releases: srv.URL, http: srv.Client(),
		bins: map[string]binary{},
	}
}

func TestFetchRelease(t *testing.T) {
	r := newRelease(t, "helper v0.2.0")
	d := releaseDialer(t, r)
	path, err := d.binaryPath(t.Context(), "arm64")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(d.cache, "v0.2.0", "tops-linux-arm64"); path != want {
		t.Errorf("path = %s, want %s", path, want)
	}
	b, err := os.ReadFile(path) //nolint:gosec // the test's cache
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "helper v0.2.0 arm64" {
		t.Errorf("cached helper = %q, want the archive's tops", b)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o755 {
		t.Errorf("cached helper mode = %v, %v; want 0755", info.Mode(), err)
	}

	hits := r.hits.Load()
	if _, err := d.binaryPath(t.Context(), "arm64"); err != nil {
		t.Fatal(err)
	}
	if r.hits.Load() != hits {
		t.Error("the second run downloaded again instead of using the cache")
	}
}

func TestFetchReleaseRefuses(t *testing.T) {
	tests := []struct {
		name  string
		spoil func(r *fakeRelease)
		code  string
		want  string
	}{
		{"tampered archive", func(r *fakeRelease) {
			r.assets["tops_0.2.0_linux_arm64.tar.gz"] = tarGz(t, map[string]string{"tops": "evil"})
		}, "release.checksum", "does not match checksums.txt"},
		{"not in checksums.txt", func(r *fakeRelease) {
			r.assets["checksums.txt"] = []byte("abc  tops_0.2.0_linux_amd64.tar.gz\n")
		}, "release.checksum", "does not list tops_0.2.0_linux_arm64.tar.gz"},
		{"no checksums.txt", func(r *fakeRelease) { delete(r.assets, "checksums.txt") }, "release.download", "checksums.txt"},
		{"no archive", func(r *fakeRelease) { delete(r.assets, "tops_0.2.0_linux_arm64.tar.gz") }, "release.download", "404"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRelease(t, "helper")
			tt.spoil(r)
			d := releaseDialer(t, r)
			_, err := d.binaryPath(t.Context(), "arm64")
			ue := userError(t, err, tt.code)
			if all := ue.What + " " + ue.Why; !strings.Contains(all, tt.want) {
				t.Errorf("error = %+v, want %q", ue, tt.want)
			}
			if entries, _ := os.ReadDir(filepath.Join(d.cache, "v0.2.0")); len(entries) != 0 {
				t.Errorf("a refused download left %d files in the cache", len(entries))
			}
		})
	}
}

func TestFetchReleaseBadArchive(t *testing.T) {
	noise := make([]byte, 4096) // incompressible, so a cut archive cuts into the binary
	_, _ = rand.Read(noise)
	whole := tarGz(t, map[string]string{"tops": string(noise)})
	for name, archive := range map[string][]byte{
		"not gzip":  []byte("not an archive"),
		"no binary": tarGz(t, map[string]string{"LICENSE": "MIT"}),
		"cut short": whole[:len(whole)/2],
	} {
		t.Run(name, func(t *testing.T) {
			r := newRelease(t, "helper")
			r.assets["tops_0.2.0_linux_arm64.tar.gz"] = archive
			r.assets["checksums.txt"] = []byte(hexSum(archive) + "  tops_0.2.0_linux_arm64.tar.gz\n")
			_, err := releaseDialer(t, r).binaryPath(t.Context(), "arm64")
			if err == nil || !strings.Contains(err.Error(), "tops_0.2.0_linux_arm64.tar.gz") {
				t.Fatalf("err = %v, want the archive named", err)
			}
		})
	}
}

func TestFetchReleaseTooLarge(t *testing.T) {
	r := newRelease(t, "helper")
	r.assets["checksums.txt"] = bytes.Repeat([]byte("x"), release.MaxChecksums+1)
	_, err := releaseDialer(t, r).binaryPath(t.Context(), "arm64")
	if ue := userError(t, err, "release.download"); !strings.Contains(ue.Why, "larger than") {
		t.Errorf("Why = %q", ue.Why)
	}
}

// TestNoReleaseForDevBuilds: only an exact release version downloads; a build after a tag
// has no release to download from.
func TestNoReleaseForDevBuilds(t *testing.T) {
	r := newRelease(t, "helper")
	for _, v := range []string{"dev", "v0.2.0-3-gabc1234", "v0.2.0-dirty", "v0.2.1-0.20261114150316-5ab47c43cda0"} {
		d := releaseDialer(t, r)
		d.version = v
		_, err := d.binaryPath(t.Context(), "arm64")
		if ue := userError(t, err, "helper.no_binary"); !strings.Contains(ue.Why, "not a release") {
			t.Errorf("%s: Why = %q", v, ue.Why)
		}
	}
	if r.hits.Load() != 0 {
		t.Errorf("dev builds made %d downloads", r.hits.Load())
	}
}

// TestConnectFromAMac: a released tops on a Mac uploads the release's linux helper.
func TestConnectFromAMac(t *testing.T) {
	r := newRelease(t, "helper v0.2.0")
	node := &fakeNode{machine: "aarch64", installed: map[string]string{}}
	_, cfg := newNode(t, node.exec)
	d, err := NewDialer(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	rd := releaseDialer(t, r)
	d.exe, d.goos, d.goarch, d.version, d.cache, d.releases, d.http = rd.exe, rd.goos, rd.goarch, rd.version, rd.cache, rd.releases, rd.http
	n, err := d.Connect(t.Context(), "vps-01", "127.0.0.1", wire.Request{RunID: "r-1"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = n.Close() }()
	if want := hexSum([]byte("helper v0.2.0 arm64")); n.SHA256 != want || !n.Uploaded {
		t.Errorf("node = %s uploaded=%v, want the release's arm64 helper %s uploaded", n.SHA256, n.Uploaded, want)
	}
}
