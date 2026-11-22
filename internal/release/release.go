// Package release finds and fetches the releases tops installs (TRD "Releases"): Temporal's
// server and CLI, and tops' own linux helpers. The workstation fetches checksums.txt over TLS
// and passes each archive's sha256 on; a node only compares.
package release

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/2SSK/tops/internal/term"
	"github.com/2SSK/tops/internal/wire"
)

// GitHub is where the releases are served from.
const GitHub = "https://github.com"

// MaxChecksums bounds a checksums.txt: each is under 2 KB.
const MaxChecksums = 1 << 20

// Kind is a Temporal release tops installs.
type Kind string

// The Temporal releases.
const (
	Server Kind = "server" // temporalio/temporal: temporal-server, temporal-sql-tool, tdbg
	CLI    Kind = "cli"    // temporalio/cli: temporal
)

// kinds is each kind's repository, archive name and the binaries tops takes from it.
var kinds = map[Kind]struct {
	repo, prefix string
	extract      []string
}{
	Server: {"temporalio/temporal", "temporal", []string{"temporal-server", "temporal-sql-tool", "tdbg"}},
	CLI:    {"temporalio/cli", "temporal_cli", []string{"temporal"}},
}

// Fetcher downloads release assets. Base is where GitHub serves them; tests use a local server.
type Fetcher struct {
	HTTP *http.Client
	Base string // GitHub
}

// Resolve is what a node needs to stage kind at version for arch: the archive's URL, its sha256
// from the release's checksums.txt, and the binaries to extract.
func (f Fetcher) Resolve(ctx context.Context, kind Kind, version, arch string) (wire.Download, error) {
	k := kinds[kind]
	dir := fmt.Sprintf("%s/%s/releases/download/v%s/", f.Base, k.repo, version)
	sums, err := f.Get(ctx, dir+"checksums.txt", MaxChecksums)
	if err != nil {
		return wire.Download{}, err
	}
	asset := fmt.Sprintf("%s_%s_linux_%s.tar.gz", k.prefix, version, arch)
	sum, err := Checksum(sums, asset)
	if err != nil {
		return wire.Download{}, err
	}
	return wire.Download{Kind: string(kind), Version: version, URL: dir + asset, SHA256: sum, Extract: k.extract}, nil
}

// Get fetches url, at most limit bytes.
func (f Fetcher) Get(ctx context.Context, url string, limit int64) ([]byte, error) {
	fail := func(why string) error {
		return &term.UserError{Code: "release.download", What: "cannot download " + url, Why: why, Fix: "check the network and try again"}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", url, err)
	}
	resp, err := f.HTTP.Do(req)
	if err != nil {
		return nil, fail(err.Error())
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fail(resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fail(err.Error())
	}
	if int64(len(b)) > limit {
		return nil, fail(fmt.Sprintf("larger than %d bytes", limit))
	}
	return b, nil
}

// Checksum finds name's sha256 in a checksums.txt: "<hex> <name>" per line.
func Checksum(sums []byte, name string) (string, error) {
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		if sum, file, ok := strings.Cut(sc.Text(), "  "); ok && file == name {
			return sum, nil
		}
	}
	return "", &term.UserError{
		Code: "release.checksum",
		What: "checksums.txt does not list " + name,
		Why:  "the release has no linux archive for this arch",
		Fix:  "use an amd64 or arm64 node",
	}
}
