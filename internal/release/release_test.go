package release

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/2SSK/tops/internal/term"
	"github.com/2SSK/tops/internal/wire"
)

// Temporal 1.31.3's checksums.txt, as GitHub serves it (the linux lines are the real sums).
const serverSums = `99453eb731fd3167f37509ee730f94c161c9d48baa5ffc0d67557af4fc236e30  temporal_1.31.3_darwin_amd64.tar.gz
f2c3bf9f1115b506259e5972a62c38257296c043e15798358c3fb758625b96b8  temporal_1.31.3_linux_amd64.tar.gz
aaaa  temporal_1.31.3_linux_amd64.tar.gz.sbom.json
`

const cliSums = `521f461f3d108902763bb71e85cd0fd382dc9357b59604ae03da6ae734d16890  temporal_cli_1.10.0_linux_amd64.tar.gz
`

// github serves path -> body; anything else is a 404.
func github(t *testing.T, assets map[string]string) Fetcher {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := assets[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return Fetcher{HTTP: srv.Client(), Base: srv.URL}
}

func userError(t *testing.T, err error, code string) *term.UserError {
	t.Helper()
	var ue *term.UserError
	if !errors.As(err, &ue) || ue.Code != code {
		t.Fatalf("err = %v, want %s", err, code)
	}
	return ue
}

// TestResolve: each kind's archive for the node's arch, its sha256 from the release's
// checksums.txt, and the binaries tops takes from it.
func TestResolve(t *testing.T) {
	f := github(t, map[string]string{
		"/temporalio/temporal/releases/download/v1.31.3/checksums.txt": serverSums,
		"/temporalio/cli/releases/download/v1.10.0/checksums.txt":      cliSums,
	})
	for _, tt := range []struct {
		kind    Kind
		version string
		want    wire.Download
	}{
		{Server, "1.31.3", wire.Download{Kind: "server", Version: "1.31.3",
			URL:    f.Base + "/temporalio/temporal/releases/download/v1.31.3/temporal_1.31.3_linux_amd64.tar.gz",
			SHA256: "f2c3bf9f1115b506259e5972a62c38257296c043e15798358c3fb758625b96b8", Extract: []string{"temporal-server", "temporal-sql-tool", "tdbg"}}},
		{CLI, "1.10.0", wire.Download{Kind: "cli", Version: "1.10.0",
			URL:    f.Base + "/temporalio/cli/releases/download/v1.10.0/temporal_cli_1.10.0_linux_amd64.tar.gz",
			SHA256: "521f461f3d108902763bb71e85cd0fd382dc9357b59604ae03da6ae734d16890", Extract: []string{"temporal"}}},
	} {
		got, err := f.Resolve(t.Context(), tt.kind, tt.version, "amd64")
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("Resolve(%s %s) =\n%+v\nwant\n%+v", tt.kind, tt.version, got, tt.want)
		}
	}
}

// TestResolveURLsAreGitHubs: with the real base, the URLs are the ones the releases have
// (the server's is the one test/e2e/hand-cluster.sh downloads).
func TestResolveURLsAreGitHubs(t *testing.T) {
	var got []string
	f := Fetcher{Base: GitHub, HTTP: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		got = append(got, r.URL.String())
		return nil, errors.New("offline")
	})}}
	_, _ = f.Resolve(t.Context(), Server, "1.31.3", "arm64")
	if want := []string{"https://github.com/temporalio/temporal/releases/download/v1.31.3/checksums.txt"}; !reflect.DeepEqual(got, want) {
		t.Errorf("fetched %v, want %v", got, want)
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// TestResolveNoArchive: a release without a linux archive for the node's arch is refused by
// name; an arch's sbom line does not count as its archive.
func TestResolveNoArchive(t *testing.T) {
	f := github(t, map[string]string{"/temporalio/temporal/releases/download/v1.31.3/checksums.txt": serverSums})
	_, err := f.Resolve(t.Context(), Server, "1.31.3", "arm64")
	if ue := userError(t, err, "release.checksum"); !strings.Contains(ue.What, "temporal_1.31.3_linux_arm64.tar.gz") {
		t.Errorf("What = %q", ue.What)
	}
}

// TestResolveNoRelease: a version with no release is a download error naming the URL and the
// status, so a typo in temporal.version is plain.
func TestResolveNoRelease(t *testing.T) {
	f := github(t, nil)
	_, err := f.Resolve(t.Context(), Server, "1.31.99", "amd64")
	ue := userError(t, err, "release.download")
	if !strings.Contains(ue.What, "/v1.31.99/checksums.txt") || ue.Why != "404 Not Found" {
		t.Errorf("error = %+v", ue)
	}
}

func TestGetLimits(t *testing.T) {
	f := github(t, map[string]string{"/big": strings.Repeat("x", 11)})
	if _, err := f.Get(t.Context(), f.Base+"/big", 10); !strings.Contains(userError(t, err, "release.download").Why, "larger than 10 bytes") {
		t.Errorf("err = %v", err)
	}
	if b, err := f.Get(t.Context(), f.Base+"/big", 11); err != nil || len(b) != 11 {
		t.Errorf("Get at the limit = %d bytes, %v", len(b), err)
	}
}

func TestGetUnreachable(t *testing.T) {
	f := github(t, nil)
	f.HTTP = &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) { return nil, errors.New("connection refused") })}
	if _, err := f.Get(t.Context(), f.Base+"/x", 10); !strings.Contains(userError(t, err, "release.download").Why, "connection refused") {
		t.Errorf("err = %v", err)
	}
	if _, err := f.Get(t.Context(), "http://bad host/", 10); err == nil {
		t.Error("a malformed URL was fetched")
	}
}

// TestGetCutShort: a connection that drops mid-body is a download error, not a short archive
// handed on to be checksummed.
func TestGetCutShort(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "100")
		_, _ = w.Write([]byte("short"))
	}))
	t.Cleanup(srv.Close)
	f := Fetcher{HTTP: srv.Client(), Base: srv.URL}
	if _, err := f.Get(t.Context(), srv.URL+"/x", 1000); !strings.Contains(userError(t, err, "release.download").Why, "unexpected EOF") {
		t.Errorf("err = %v", err)
	}
}
