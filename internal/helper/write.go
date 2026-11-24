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
	codeFileWrite = "file.write"
)

var (
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

// Write puts each file in place and removes the paths in Remove. First it saves every path
// it is about to change in the apply's snapshot; a file already as wanted, or a path already
// absent, is left alone, so a second write changes nothing and saves nothing.
func (h Host) Write(applyID string, w wire.Write) (wire.WriteResult, error) {
	n, err := h.names()
	if err != nil {
		return wire.WriteResult{}, err
	}
	r, err := h.changes(w, n)
	if err != nil || len(r.Written)+len(r.Removed) == 0 {
		return r, err
	}
	if err := h.snapshot(applyID, append(slices.Clone(r.Written), r.Removed...), n); err != nil {
		return wire.WriteResult{}, err
	}
	r.Snapshot = applyID
	for _, f := range w.Files {
		if !slices.Contains(r.Written, f.Path) {
			continue
		}
		mode, _ := strconv.ParseUint(f.Mode, 8, 32)
		if err := replaceFile(filepath.Join(h.Root, f.Path), f.Content, fs.FileMode(mode), int(n.uids[f.Owner]), int(n.gids[f.Group])); err != nil {
			return wire.WriteResult{}, fileError("write", f.Path, err)
		}
	}
	for _, path := range r.Removed {
		if err := removeFile(filepath.Join(h.Root, path)); err != nil {
			return wire.WriteResult{}, fileError("remove", path, err)
		}
	}
	return r, nil
}

// changes is what w would change: the files not already in place, the paths still there.
func (h Host) changes(w wire.Write, n names) (wire.WriteResult, error) {
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
	dir := filepath.Join(h.Root, snapshotsDir, applyID)
	if err := os.MkdirAll(filepath.Join(dir, "files"), 0o700); err != nil {
		return fileError("create", filepath.Join(snapshotsDir, applyID), err)
	}
	m := snapshotManifest{ApplyID: applyID}
	if b, err := os.ReadFile(filepath.Join(dir, "manifest.json")); err == nil { //nolint:gosec // under our own state dir
		if err := json.Unmarshal(b, &m); err != nil {
			return fileError("read", filepath.Join(snapshotsDir, applyID, "manifest.json"), err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fileError("read", filepath.Join(snapshotsDir, applyID, "manifest.json"), err)
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
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // G301: the temporal user must reach /etc/temporal; each file has its own mode
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(full)+".tops-*")
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

// ids are f's owner and group as the node numbers them.
func (n names) ids(f wire.File) (int, int, error) {
	uid, okU := n.uids[f.Owner]
	gid, okG := n.gids[f.Group]
	if !okU || !okG {
		return 0, 0, &wire.Error{Code: codeFileWrite, Message: "write " + f.Path, Detail: fmt.Sprintf("no user %s or group %s on this node", f.Owner, f.Group)}
	}
	return int(uid), int(gid), nil
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
