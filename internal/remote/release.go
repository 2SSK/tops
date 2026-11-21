package remote

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/2SSK/tops/internal/release"
	"github.com/2SSK/tops/internal/term"
)

// releaseBase is where GitHub serves a tag's release assets.
const releaseBase = "https://github.com/2SSK/tops/releases/download"

// releaseVersion matches the versions that have a release: a tag, not a build after one.
var releaseVersion = regexp.MustCompile(`^v(\d+\.\d+\.\d+)$`)

// Bounds on what tops downloads: a tops archive is about 2.5 MB.
const (
	maxArchive   = 64 << 20
	fetchTimeout = 2 * time.Minute
)

// fetchRelease returns ths linux helper for arch of this release from the cache, downloading
// it on first use: the archive from the version's own release, verified against its
// checksums.txt before anything is extracted (TRD "Helper upload and invocation").
func (d *Dialer) fetchRelease(ctx context.Context, arch string) (string, error) {
	path := filepath.Join(d.cache, d.version, "tops-linux-"+arch)
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	name := fmt.Sprintf("tops_%s_linux_%s.tar.gz", strings.TrimPrefix(d.version, "v"), arch)
	fetch := release.Fetcher{HTTP: d.http}
	base := d.releases + "/" + d.version + "/"
	sums, err := fetch.Get(ctx, base+"checksums.txt", release.MaxChecksums)
	if err != nil {
		return "", err
	}
	want, err := release.Checksum(sums, name)
	if err != nil {
		return "", err
	}
	archive, err := fetch.Get(ctx, base+name, maxArchive)
	if err != nil {
		return "", err
	}
	if got := sha256.Sum256(archive); hex.EncodeToString(got[:]) != want {
		return "", &term.UserError{
			Code: "release.checksum",
			What: name + " does not match checksums.txt",
			Why:  fmt.Sprintf("sha256 %s, checksums.txt says %s", hex.EncodeToString(got[:]), want),
			Fix:  "try again; if it persists, report it: the release may have been tampered with",
		}
	}
	bin, err := extractTops(archive)
	if err != nil {
		return "", fmt.Errorf("%s: %w", name, err)
	}
	if err := writeExecutable(path, bin); err != nil {
		return "", err
	}
	return path, nil
}

// extractTops returns the tops binary from a release archive: the file named tops at its root.
func extractTops(archive []byte) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, fmt.Errorf("read the archive: %w", err)
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil, errors.New("no tops binary in the archive")
		}
		if err != nil {
			return nil, fmt.Errorf("read the archive: %w", err)
		}
		if hdr.Name == "tops" && hdr.Typeflag == tar.TypeReg {
			return io.ReadAll(io.LimitReader(tr, maxArchive))
		}
	}
}

// writeExecutable writes b to path atomically: a run interrupted mid-write, or a parallel run,
// never leaves a half-written helper in the cache for the next run to upload.
func writeExecutable(path string, b []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("create the helper cache: %w", err)
	}
	f, err := os.CreateTemp(dir, ".tops-*")
	if err != nil {
		return fmt.Errorf("create the helper cache: %w", err)
	}
	defer func() { _ = os.Remove(f.Name()) }() // gone after the rename; cleans up after a failure
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Chmod(f.Name(), 0o755); err != nil { //nolint:gosec // the helper must be executable
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
