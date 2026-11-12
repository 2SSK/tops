package helper

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"

	"github.com/2SSK/tops/internal/wire"
)

// Where Temporal's releases live on a node (backend schema "Files tops installs").
const (
	releasesDir = "/opt/temporal/releases"
	currentLink = "/opt/temporal/current"
)

// Facts reads the files tops would write, the tops-owned directories, and the installed
// releases, and Temporal's systemd units.
func (h Host) Facts(ctx context.Context, args wire.FactsArgs) (wire.Facts, error) {
	names, err := h.names()
	if err != nil {
		return wire.Facts{}, err
	}
	f := wire.Facts{Files: map[string]wire.FileState{}, Unknown: []string{}, Releases: []string{}}
	for path, want := range args.Want {
		if f.Files[path], err = h.fileState(path, want, names); err != nil {
			return wire.Facts{}, err
		}
	}
	for _, dir := range args.Dirs {
		if f.Unknown, err = h.unknown(dir, args.Want, f.Unknown); err != nil {
			return wire.Facts{}, err
		}
	}
	slices.Sort(f.Unknown)
	if f.Current, err = h.current(); err != nil {
		return wire.Facts{}, err
	}
	if f.Releases, err = h.releases(); err != nil {
		return wire.Facts{}, err
	}
	if f.Units, err = h.units(ctx); err != nil {
		return wire.Facts{}, err
	}
	return f, nil
}

// fileState reads one file. Its content travels only when it differs from what tops
// would write, so the workstation can show the differ.
func (h Host) fileState(path, want string, n names) (wire.FileState, error) {
	full := filepath.Join(h.Root, path)
	info, err := os.Stat(full)
	if errors.Is(err, fs.ErrNotExist) {
		return wire.FileState{Exists: false}, nil
	}
	if err != nil {
		return wire.FileState{}, fmt.Errorf("stat %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return wire.FileState{}, fmt.Errorf("%s is not a regular file", path)
	}
	content, err := os.ReadFile(filepath.Clean(full))
	if err != nil {
		return wire.FileState{}, fmt.Errorf("read %s: %w", path, err)
	}
	sum := sha256.Sum256(content)
	st := wire.FileState{
		Exists: true,
		SHA256: hex.EncodeToString(sum[:]),
		Mode:   fmt.Sprintf("%04o", info.Mode().Perm()),
	}
	if sys, ok := info.Sys().(*syscall.Stat_t); ok {
		st.Owner = n.user(sys.Uid)
		st.Group = n.group(sys.Gid)
	}
	if st.SHA256 != want {
		st.Content = content
	}
	return st, nil
}

// unknown appends the files under dir that are not in want. A missing dir has none.
func (h Host) unknown(dir string, want map[string]string, found []string) ([]string, error) {
	err := filepath.WalkDir(filepath.Join(h.Root, dir), func(full string, d fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) {
			return fs.SkipDir
		}
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		path := "/" + strings.TrimPrefix(filepath.ToSlash(strings.TrimPrefix(full, h.Root)), "/")
		if _, ok := want[path]; !ok && !slices.Contains(found, path) {
			found = append(found, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", dir, err)
	}
	return found, nil
}

// current returns the release current points to, "" when there is none yet.
func (h Host) current() (string, error) {
	target, err := os.Readlink(filepath.Join(h.Root, currentLink))
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read %s: %w", currentLink, err)
	}
	return target, nil
}

// releases returns the installed release directories, sorted.
func (h Host) releases() ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(h.Root, releasesDir))
	if errors.Is(err, fs.ErrNotExist) {
		return []string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", releasesDir, err)
	}
	found := []string{}
	for _, e := range entries {
		if e.IsDir() {
			found = append(found, e.Name())
		}
	}
	return found, nil
}

// names maps user and group ids to names, from /etc/passwd and /etc/group.
type names struct {
	users, groups map[uint32]string
}

func (n names) user(id uint32) string  { return nameOr(n.users, id) }
func (n names) group(id uint32) string { return nameOr(n.groups, id) }

// nameOr returns the name of id, or the id itself when it has none.
func nameOr(m map[uint32]string, id uint32) string {
	if name, ok := m[id]; ok {
		return name
	}
	return strconv.FormatUint(uint64(id), 10)
}

func (h Host) names() (names, error) {
	users, err := readIDs(filepath.Join(h.Etc, "passwd"))
	if err != nil {
		return names{}, err
	}
	groups, err := readIDs(filepath.Join(h.Etc, "group"))
	if err != nil {
		return names{}, err
	}
	return names{users: users, groups: groups}, nil
}

// readIDs reads "name:x:id:..." lines, the format of both /etc/passwd and /etc/group.
func readIDs(file string) (map[uint32]string, error) {
	b, err := os.ReadFile(filepath.Clean(file))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", filepath.Base(file), err)
	}
	ids := map[uint32]string{}
	for line := range bytes.Lines(b) {
		f := strings.Split(strings.TrimSpace(string(line)), ":")
		if len(f) < 3 {
			continue
		}
		if id, err := strconv.ParseUint(f[2], 10, 32); err == nil {
			ids[uint32(id)] = f[0]
		}
	}
	return ids, nil
}
