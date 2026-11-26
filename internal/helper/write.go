package helper

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"syscall"

	"github.com/2SSK/tops/internal/wire"
)

const (
	snapshotsDir  = "/var/lib/tops/snapshots"
	keepSnapshots = 10
	codeFileWrite = "file.write"
	tempMark      = ".tops-" // in the name of every temporary file
)

var (
	// writable is every path write may change: the files render makes, and nothing else, so
	// a request can never make root write elsewhere.
	writable = regexp.MustCompile(`^(/etc/temporal/[a-z][a-z0-9.-]*|/etc/systemd/system/temporal@\.service|/etc/systemd/system/temporal@[a-z]+\.service\.d/limits\.conf)$`)
	// applyIDName is an apply id, "20261023T153012Z-7f3a": a directory name under snapshots.
	applyIDName = regexp.MustCompile(`^[0-9]{8}T[0-9]{6}Z-[0-9a-f]{4}$`)
)

// snapshotManifest is snapshots/<apply-id>/manifest.json: every path the apply changed on
// this node, as it was before the apply's first write to it.
type snapshotManifest struct {
	ApplyID string          `json:"applyId"`
	Entries []snapshotEntry `json:"entries"`
}

type snapshotEntry struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`             // "file", "symlink", "absent"
	SHA256 string `json:"sha256,omitempty"` // file: its content is files/<sha256>
	Target string `json:"target,omitempty"` // symlink
	Mode   string `json:"mode,omitempty"`
	Owner  string `json:"owner,omitempty"`
	Group  string `json:"group,omitempty"`
}

// Write puts each file in place and removes the paths in Remove, then drops the snapshots
// beyond the newest ten.
func (h Host) Write(applyID string, w wire.Write) (wire.WriteResult, error) {
	r, err := h.put(applyID, w, nil)
	if err != nil || r.Snapshot == "" {
		return r, err
	}
	return r, h.pruneSnapshots(applyID)
}

// link is a symlink restore puts back; write makes none.
type link struct{ path, target string }

// put makes the node's files, links and absences what the caller wants. First it saves every
// path it is about to change in the apply's snapshot; a path already as wanted is left alone,
// so a second put changes nothing and saves nothing.
func (h Host) put(applyID string, w wire.Write, links []link) (wire.WriteResult, error) {
	if err := h.sweep(); err != nil {
		return wire.WriteResult{}, err
	}
	n, err := h.names()
	if err != nil {
		return wire.WriteResult{}, err
	}
	r, err := h.changes(w, links, n)
	if err != nil || len(r.Written)+len(r.Removed) == 0 {
		return r, err
	}
	if err := h.snapshot(applyID, slices.Concat(r.Written, r.Removed), n); err != nil {
		return wire.WriteResult{}, err
	}
	r.Snapshot = applyID
	return r, h.apply(w, links, r, n)
}

// apply makes the changes in r, each path on its own.
func (h Host) apply(w wire.Write, links []link, r wire.WriteResult, n names) error {
	for _, f := range w.Files {
		if !slices.Contains(r.Written, f.Path) {
			continue
		}
		uid, gid, err := n.ids(f)
		if err != nil {
			return err
		}
		mode, _ := strconv.ParseUint(f.Mode, 8, 32)
		if err := replaceFile(filepath.Join(h.Root, f.Path), f.Content, fs.FileMode(mode), uid, gid); err != nil {
			return fileError("write", f.Path, err)
		}
	}
	for _, l := range links {
		if !slices.Contains(r.Written, l.path) {
			continue
		}
		if err := replaceLink(filepath.Join(h.Root, l.path), l.target); err != nil {
			return fileError("link", l.path, err)
		}
	}
	for _, path := range r.Removed {
		if err := removeFile(filepath.Join(h.Root, path)); err != nil {
			return fileError("remove", path, err)
		}
	}
	return nil
}

// sweep removes the temporary files a killed write left behind: they may hold secrets. The
// fence is held, so no other write is using them.
func (h Host) sweep() error {
	for _, dir := range []string{"etc/temporal", "etc/systemd/system", "etc/systemd/system/temporal@*.service.d"} {
		left, err := filepath.Glob(filepath.Join(h.Root, dir, ".*"+tempMark+"*"))
		if err != nil {
			return err
		}
		for _, full := range left {
			if err := removeFile(full); err != nil {
				return fileError("remove", full, err)
			}
		}
	}
	return nil
}

// changes is what put would change: the files and links not already in place, the paths
// still there.
func (h Host) changes(w wire.Write, links []link, n names) (wire.WriteResult, error) {
	var r wire.WriteResult
	for _, f := range w.Files {
		same, err := h.inPlace(f, n)
		if err != nil {
			return wire.WriteResult{}, err
		}
		if !same {
			r.Written = append(r.Written, f.Path)
		}
	}
	for _, l := range links {
		if target, err := os.Readlink(filepath.Join(h.Root, l.path)); err != nil || target != l.target {
			r.Written = append(r.Written, l.path)
		}
	}
	for _, path := range w.Remove {
		if _, err := os.Lstat(filepath.Join(h.Root, path)); err == nil {
			r.Removed = append(r.Removed, path)
		}
	}
	return r, nil
}

// inPlace says whether f is already on the node: same content, mode, owner and group.
func (h Host) inPlace(f wire.File, n names) (bool, error) {
	uid, gid, err := n.ids(f)
	if err != nil {
		return false, err
	}
	full := filepath.Join(h.Root, f.Path)
	info, err := os.Lstat(full)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fileError("stat", f.Path, err)
	}
	if st, ok := info.Sys().(*syscall.Stat_t); !ok || !info.Mode().IsRegular() || fmt.Sprintf("%04o", info.Mode().Perm()) != f.Mode || int(st.Uid) != uid || int(st.Gid) != gid {
		return false, nil
	}
	b, err := os.ReadFile(full) //nolint:gosec // a writable path, checked
	if err != nil {
		return false, fileError("read", f.Path, err)
	}
	return bytes.Equal(b, f.Content), nil
}

// snapshot saves each path as it is now in snapshots/<apply-id>/, unless the apply's
// snapshot already holds it: an apply writes a node several times, and rollback must bring
// back the state before its first write. Contents are stored by their sha256, so a file is
// stored once however many paths had it.
func (h Host) snapshot(applyID string, paths []string, n names) error {
	m, _, err := h.manifest(applyID)
	if err != nil {
		return err
	}
	dir := filepath.Join(h.Root, snapshotsDir, applyID)
	if err := makeDir(filepath.Join(dir, "files"), 0o700); err != nil {
		return fileError("create", filepath.Join(snapshotsDir, applyID), err)
	}
	for _, path := range paths {
		if slices.ContainsFunc(m.Entries, func(e snapshotEntry) bool {
			return e.Path == path
		}) {
			continue
		}
		e, err := h.saveEntry(dir, path, n)
		if err != nil {
			return fileError("snapshot", path, err)
		}
		m.Entries = append(m.Entries, e)
	}
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if err := replaceFile(filepath.Join(dir, "manifest.json"), b, 0o600, -1, -1); err != nil {
		return fileError("write", filepath.Join(snapshotsDir, applyID, "manifest.json"), err)
	}
	return nil
}

// manifest is the apply's snapshot manifest; false when the node has no such snapshot.
func (h Host) manifest(applyID string) (snapshotManifest, bool, error) {
	m := snapshotManifest{ApplyID: applyID}
	path := filepath.Join(snapshotsDir, applyID, "manifest.json")
	b, err := os.ReadFile(filepath.Join(h.Root, path)) //nolint:gosec // our own state, the id checked
	if errors.Is(err, fs.ErrNotExist) {
		return m, false, nil
	}
	if err == nil {
		err = json.Unmarshal(b, &m)
	}
	if err != nil {
		return m, false, fileError("read", path, err)
	}
	return m, true, nil
}

// pruneSnapshots keeps the newest ten snapshots, applyID's own always among them: apply ids
// carry the workstation's clock, and a slow one must not cost an apply its own undo.
func (h Host) pruneSnapshots(applyID string) error {
	entries, err := os.ReadDir(filepath.Join(h.Root, snapshotsDir)) // by name, so oldest first
	if err != nil {
		return fileError("read", snapshotsDir, err)
	}
	var others []string
	for _, e := range entries {
		if e.Name() != applyID && applyIDName.MatchString(e.Name()) {
			others = append(others, e.Name())
		}
	}
	for _, id := range others[:max(0, len(others)-(keepSnapshots-1))] {
		if err := os.RemoveAll(filepath.Join(h.Root, snapshotsDir, id)); err != nil {
			return fileError("remove", filepath.Join(snapshotsDir, id), err)
		}
	}
	return nil
}

// saveEntry records path as it is: absent, a symlink and its target, or a file whose content
// it copies to files/<sha256>.
func (h Host) saveEntry(dir, path string, n names) (snapshotEntry, error) {
	full := filepath.Join(h.Root, path)
	info, err := os.Lstat(full)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return snapshotEntry{Path: path, Kind: "absent"}, nil
	case err != nil:
		return snapshotEntry{}, err
	case info.Mode()&fs.ModeSymlink != 0:
		target, err := os.Readlink(full)
		return snapshotEntry{Path: path, Kind: "symlink", Target: target}, err
	case !info.Mode().IsRegular():
		return snapshotEntry{}, errors.New("not a regular file")
	}
	b, err := os.ReadFile(full) //nolint:gosec // a writable path, checked
	if err != nil {
		return snapshotEntry{}, err
	}
	sum := sha256.Sum256(b)
	e := snapshotEntry{Path: path, Kind: "file", SHA256: hex.EncodeToString(sum[:]), Mode: fmt.Sprintf("%04o", info.Mode().Perm())}
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		e.Owner, e.Group = n.user(st.Uid), n.group(st.Gid)
	}
	return e, replaceFile(filepath.Join(dir, "files", e.SHA256), b, 0o600, -1, -1) // -1: root's already, as the helper is
}

// replaceFile puts content at full atomically and durably: a reader, or the node after a
// crash, sees the old file or the new one, whole. The content goes to a temporary file in
// the same directory (0600 until it is complete), is synced, renamed over full, and the
// directory is synced so the rename itself survives a power loss.
func replaceFile(full string, content []byte, mode fs.FileMode, uid, gid int) error {
	dir := filepath.Dir(full)
	if err := makeDir(dir, 0o755); err != nil { // the temporal user must reach /etc/temporal; each file has its own mode
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(full)+tempMark+"*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // after the rename there is nothing left to remove
	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := errors.Join(tmp.Chmod(mode), tmp.Chown(uid, gid), tmp.Sync(), tmp.Close()); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), full); err != nil {
		return err
	}
	return syncDir(dir)
}

// replaceLink makes full a symlink to target, atomically and durably as replaceFile does.
func replaceLink(full, target string) error {
	tmp := filepath.Join(filepath.Dir(full), "."+filepath.Base(full)+tempMark+"link")
	if err := os.Symlink(target, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, full); err != nil {
		return err
	}
	return syncDir(filepath.Dir(full))
}

// makeDir is os.MkdirAll that also syncs each parent it adds to: without that, a power loss
// can take a new directory away with the files synced inside it.
func makeDir(dir string, mode fs.FileMode) error {
	if _, err := os.Stat(dir); !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	parent := filepath.Dir(dir)
	if err := makeDir(parent, mode); err != nil {
		return err
	}
	if err := os.Mkdir(dir, mode); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	return syncDir(parent)
}

// removeFile removes full and syncs its directory, so the removal survives a crash.
func removeFile(full string) error {
	if err := os.Remove(full); err != nil {
		return err
	}
	return syncDir(filepath.Dir(full))
}

func syncDir(dir string) error {
	d, err := os.Open(dir) //nolint:gosec // the directory of a path we just wrote
	if err != nil {
		return err
	}
	return errors.Join(d.Sync(), d.Close())
}

func fileError(verb, path string, err error) error {
	return &wire.Error{Code: codeFileWrite, Message: verb + " " + path, Detail: err.Error()}
}

// ids are f's owner and group as the node numbers them. A snapshot names an owner without an
// account by its number.
func (n names) ids(f wire.File) (int, int, error) {
	uid, okU := idOf(n.uids, f.Owner)
	gid, okG := idOf(n.gids, f.Group)
	if !okU || !okG {
		return 0, 0, &wire.Error{Code: codeFileWrite, Message: "write " + f.Path, Detail: fmt.Sprintf("no user %s or group %s on this node", f.Owner, f.Group)}
	}
	return uid, gid, nil
}

// idOf is the id nameOr gave name for.
func idOf(m map[string]uint32, name string) (int, bool) {
	if id, ok := m[name]; ok {
		return int(id), true
	}
	id, err := strconv.ParseUint(name, 10, 32)
	return int(id), err == nil //nolint:gosec // G115: 32 bits, parsed as such
}

// checkWrite refuses a write without an apply id to snapshot under, a path render does not
// make, a mode that is not plain permissions, a file without an owner, and a path twice.
func checkWrite(applyID string, w wire.Write) error {
	if !applyIDName.MatchString(applyID) {
		return fmt.Errorf("apply id %q is not one", applyID)
	}
	seen := map[string]bool{}
	for _, f := range w.Files {
		if err := checkFile(f, seen); err != nil {
			return err
		}
	}
	for _, path := range w.Remove {
		if err := checkWritable(path, seen); err != nil {
			return err
		}
	}
	return nil
}

func checkFile(f wire.File, seen map[string]bool) error {
	if err := checkWritable(f.Path, seen); err != nil {
		return err
	}
	if m, err := strconv.ParseUint(f.Mode, 8, 32); err != nil || len(f.Mode) != 4 || m > 0o777 {
		return fmt.Errorf("%s: mode %q is not permissions like 0640", f.Path, f.Mode)
	}
	if f.Owner == "" || f.Group == "" {
		return fmt.Errorf("%s: no owner or group", f.Path)
	}
	return nil
}

func checkWritable(path string, seen map[string]bool) error {
	if !writable.MatchString(path) {
		return fmt.Errorf("%q is not a file tops writes", path)
	}
	if seen[path] {
		return fmt.Errorf("%s is named twice", path)
	}
	seen[path] = true
	return nil
}
