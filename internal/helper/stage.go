package helper

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/2SSK/tops/internal/wire"
)

// Where staged releases live (backend schema "Files on a node").
const (
	cliDir       = "/opt/temporal/cli"
	binDir       = "/usr/local/bin"
	temporalHome = "/var/lib/temporal"
	keepReleases = 3
	maxDownload  = 512 << 20 // a server archive is about 80 MB
	stageTimeout = 10 * time.Minute
)

// Error codes of stage.
const (
	codeChecksum = "release.checksum"
	codeDownload = "release.download"
)

// versionName is a release version as a directory name: digits and dots only, so it cannot
// reach outside its parent.
var versionName = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

// binaryName is a file tops takes from an archive: a plain name, at the archive's root.
var binaryName = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// Stage installs what tops' units need before any file is written: the temporal user and the
// releases. It is idempotent: what is there already is left alone and not reported, so a second
// run returns no changes.
func (h Host) Stage(ctx context.Context, s wire.Stage) (wire.StageResult, error) {
	r := wire.StageResult{Changed: []string{}}
	if s.User {
		if err := h.stageUser(ctx, &r); err != nil {
			return wire.StageResult{}, err
		}
	}
	for _, d := range s.Releases {
		if err := h.stageRelease(ctx, d, &r); err != nil {
			return wire.StageResult{}, err
		}
	}
	if err := h.prune(&r); err != nil {
		return wire.StageResult{}, err
	}
	return r, nil
}

// checkStage refuses what could make stage write outside its directories.
func checkStage(s wire.Stage) error {
	for _, d := range s.Releases {
		if d.Kind != "server" && d.Kind != "cli" {
			return fmt.Errorf("release kind %q is not server or cli", d.Kind)
		}
		if !versionName.MatchString(d.Version) {
			return fmt.Errorf("release version %q is not x.y.z", d.Version)
		}
		for _, name := range d.Extract {
			if !binaryName.MatchString(name) {
				return fmt.Errorf("%q is not a binary name", name)
			}
		}
		if !strings.HasPrefix(d.URL, "https://") && !strings.HasPrefix(d.URL, "http://") {
			return fmt.Errorf("release URL %q is not http(s)", d.URL)
		}
	}
	return nil
}

// stageUser creates the temporal system user with its home, 0750, when it does not exist.
func (h Host) stageUser(ctx context.Context, r *wire.StageResult) error {
	users, _, err := readIDs(filepath.Join(h.Etc, "passwd"))
	if err != nil {
		return err
	}
	for _, name := range users {
		if name == "temporal" {
			return nil
		}
	}
	if err := h.AddUser(ctx, "temporal", temporalHome); err != nil {
		return fmt.Errorf("create the temporal user: %w", err)
	}
	if err := os.Chmod(filepath.Join(h.Root, temporalHome), 0o750); err != nil { //nolint:gosec	// the user's home, not secret
		return fmt.Errorf("create the temporal user: %w", err)
	}
	r.Changed = append(r.Changed, "user temporal")
	return nil
}

// addUser is AddUser on a real node.
func addUser(ctx context.Context, name, home string) error {
	out, err := exec.CommandContext(ctx, "useradd", //nolint:gosec	// name and home are stage's constants
		"--system", "--create-home", "--home-dir", home, "--shell", "/usr/sbin/nologin", name).CombinedOutput()
	if err != nil {
		return fmt.Errorf("useradd: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// releaseDir is where a release's binaries go: /opt/temporal/releases/<v> for the server,
// /opt/temporal/cli/<v> for the CLI.
func releaseDir(d wire.Download) string {
	if d.Kind == "cli" {
		return filepath.Join(cliDir, d.Version)
	}
	return filepath.Join(releasesDir, d.Version)
}

// stageRelease downloads, verifies and extracts one release, unless it is installed. It
// extracts into a temporary directory next to the release's and renames it into place, so a
// half-extracted release never looks installed. Then the links.
func (h Host) stageRelease(ctx context.Context, d wire.Download, r *wire.StageResult) error {
	dir := releaseDir(d)
	if _, err := os.Stat(filepath.Join(h.Root, dir)); errors.Is(err, fs.ErrNotExist) {
		if err := h.install(ctx, d, dir); err != nil {
			return err
		}
		r.Changed = append(r.Changed, dir)
	} else if err != nil {
		return fmt.Errorf("stat %s: %w", dir, err)
	}
	return h.links(d, dir, r)
}

func (h Host) install(ctx context.Context, d wire.Download, dir string) error {
	parent := filepath.Join(h.Root, filepath.Dir(dir))
	if err := os.MkdirAll(parent, 0o755); err != nil { //nolint:gosec	// release directories are world-readable, like /usr
		return fmt.Errorf("create %s: %w", filepath.Dir(dir), err)
	}
	archive, err := h.download(ctx, d, parent)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(archive) }()
	tmp, err := os.MkdirTemp(parent, "."+d.Version+"-")
	if err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	defer func() { _ = os.RemoveAll(tmp) }() // gone after the rename; cleans up after a failure
	if err := extract(archive, tmp, d.Extract); err != nil {
		return fmt.Errorf("%s: %w", d.URL, err)
	}
	if err := os.Chmod(tmp, 0o755); err != nil { //nolint:gosec	// as above
		return fmt.Errorf("create %s: %w", dir, err)
	}
	if err := os.Rename(tmp, filepath.Join(h.Root, dir)); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	return nil
}

// download writes the archive to a temporary file in dir, hashing as it goes, and returns the
// file only if its sha256 is the one the workstation read from checksums.txt.
func (h Host) download(ctx context.Context, d wire.Download, dir string) (string, error) {
	fail := func(err error) error {
		return &wire.Error{Code: codeDownload, Message: "download " + d.URL, Detail: err.Error()}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.URL, nil)
	if err != nil {
		return "", fail(err)
	}
	resp, err := h.HTTP.Do(req)
	if err != nil {
		return "", fail(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fail(errors.New(resp.Status))
	}
	f, err := os.CreateTemp(dir, ".download-")
	if err != nil {
		return "", fmt.Errorf("download %s: %w", d.URL, err)
	}
	sum := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, sum), io.LimitReader(resp.Body, maxDownload+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	switch {
	case err != nil:
		_ = os.Remove(f.Name())
		return "", fail(err)
	case n > maxDownload:
		_ = os.Remove(f.Name())
		return "", fail(fmt.Errorf("larger than %d bytes", maxDownload))
	}
	if got := hex.EncodeToString(sum.Sum(nil)); got != d.SHA256 {
		_ = os.Remove(f.Name())
		return "", &wire.Error{Code: codeChecksum, Message: d.URL + " does not match checksums.txt", Detail: "sha256" + got + ", checksums.txt says " + d.SHA256}
	}
	return f.Name(), nil
}

// extract writes the wanted binaries of a .tar.gz into dir, 0755. Only regular files at the
// archive's root with a wanted name are taken; every wanted one must be there.
func extract(archive, dir string, want []string) error {
	f, err := os.Open(filepath.Clean(archive))
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("read the archive: %w", err)
	}
	tr := tar.NewReader(gz)
	found := map[string]bool{}
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read the archive: %w", err)
		}
		name := strings.TrimPrefix(hdr.Name, "./")
		if hdr.Typeflag != tar.TypeReg || !slices.Contains(want, name) {
			continue // only wanted names, which binaryName allows: no path, no ".."
		}
		if err := extractOne(tr, filepath.Join(dir, name)); err != nil {
			return err
		}
		found[name] = true
	}
	if i := slices.IndexFunc(want, func(name string) bool { return !found[name] }); i >= 0 {
		return fmt.Errorf("no %s in the archive", want[i])
	}
	return nil
}

// extractOne writes the archive's current file to path, which must not exist yet.
func extractOne(r io.Reader, path string) error {
	out, err := os.OpenFile(filepath.Clean(path), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755) //nolint:gosec // a binary: executable by everyone, like /usr/bin
	if err != nil {
		return err
	}
	_, err = io.Copy(out, io.LimitReader(r, maxDownload))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	return err
}

// links points the server's current at its release on the first install only (a later
// version changes current through upgrade), and puts the tools on PATH: temporal at the
// staged CLI, tdbg and temporal-sql-tool at current's.
func (h Host) links(d wire.Download, dir string, r *wire.StageResult) error {
	if d.Kind == "cli" {
		return h.link(filepath.Join(binDir, "temporal"), filepath.Join(dir, "temporal"), r)
	}
	if _, err := os.Lstat(filepath.Join(h.Root, currentLink)); errors.Is(err, fs.ErrNotExist) {
		if err := h.link(currentLink, dir, r); err != nil {
			return err
		}
	}
	for _, tool := range []string{"tdbg", "temporal-sql-tool"} {
		if err := h.link(filepath.Join(binDir, tool), filepath.Join(currentLink, tool), r); err != nil {
			return err
		}
	}
	return nil
}

// link makes path a symlink to target, replacing it atomically, unless it already is one.
func (h Host) link(path, target string, r *wire.StageResult) error {
	full := filepath.Join(h.Root, path)
	if got, err := os.Readlink(full); err == nil && got == target {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil { //nolint:gosec	// /usr/local/bin,/opt/temporal
		return fmt.Errorf("link %s: %w", path, err)
	}
	tmp := full + ".tops"
	_ = os.Remove(tmp)
	if err := os.Symlink(target, tmp); err != nil {
		return fmt.Errorf("link %s: %w", path, err)
	}
	if err := os.Rename(tmp, full); err != nil {
		return fmt.Errorf("link %s: %w", path, err)
	}
	r.Changed = append(r.Changed, path+" -> "+target)
	return nil
}

// prone removes server releases beyond the newest keepReleases, never the one current points
// to. (From T3.5, also never one a kept snapshot references.)
func (h Host) prune(r *wire.StageResult) error {
	installed, err := h.releases()
	if err != nil {
		return err
	}
	current, err := h.current()
	if err != nil {
		return err
	}
	slices.SortFunc(installed, func(a, b string) int { return compareVersions(b, a) }) // newest first
	for i, v := range installed {
		dir := filepath.Join(releasesDir, v)
		if i < keepReleases || dir == current || !versionName.MatchString(v) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(h.Root, dir)); err != nil {
			return fmt.Errorf("remove %s: %w", dir, err)
		}
		r.Changed = append(r.Changed, "removed "+dir)
	}
	return nil
}

// compareVersions orders x.y.z numerically: 1.31.10 is newer than 1.31.9.
func compareVersions(a, b string) int {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := range min(len(pa), len(pb)) {
		x, _ := strconv.Atoi(pa[i])
		y, _ := strconv.Atoi(pb[i])
		if x != y {
			return x - y
		}
	}
	return len(pa) - len(pb)
}
