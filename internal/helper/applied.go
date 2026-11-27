package helper

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/2SSK/tops/internal/wire"
)

// recordApplied changes applied.json by change and saves it, as applyID's, when that changed
// anything. Callers record even when they changed nothing on the node: a write whose files
// went in but whose record did not is then recorded by its retry.
func (h Host) recordApplied(applyID string, change func(a *wire.Applied)) error {
	a, err := h.applied()
	if err != nil {
		return err
	}
	if a == nil {
		a = &wire.Applied{}
	}
	files, roles := maps.Clone(a.Files), slices.Clone(a.Roles)
	if a.Files == nil {
		a.Files = map[string]wire.AppliedFile{}
	}
	change(a)
	slices.Sort(a.Roles)
	if a.Roles = slices.Compact(a.Roles); maps.Equal(files, a.Files) && slices.Equal(roles, a.Roles) {
		return nil
	}
	a.ApplyID = applyID
	b, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	// /var/lib/tops and applied.json are the helper's alone, root's, whichever op creates the
	// directory first.
	if err := makeDir(filepath.Join(h.Root, stateDir), 0o700); err != nil {
		return fileError("create", stateDir, err)
	}
	if err := replaceFile(filepath.Join(h.Root, appliedFile), append(b, '\n'), 0o600, os.Geteuid(), os.Getegid()); err != nil {
		return fileError("write", appliedFile, err)
	}
	return nil
}

// recordFiles records each of w's files as put left it, and forgets each path w removed.
func (h Host) recordFiles(applyID string, w wire.Write) error {
	return h.recordApplied(applyID, func(a *wire.Applied) {
		for _, f := range w.Files {
			sum := sha256.Sum256(f.Content)
			a.Files[f.Path] = wire.AppliedFile{SHA256: hex.EncodeToString(sum[:]), Mode: f.Mode, Owner: f.Owner, Group: f.Group}
		}
		for _, path := range w.Remove {
			delete(a.Files, path)
		}
	})
}

// recordRoles records the role enable-now enabled, and forgets the one disable-now disabled.
func (h Host) recordRoles(applyID string, s wire.Service) error {
	role := strings.TrimSuffix(strings.TrimPrefix(s.Unit, "temporal@"), ".service")
	return h.recordApplied(applyID, func(a *wire.Applied) {
		switch s.Action {
		case wire.ServiceEnableNow:
			a.Roles = append(a.Roles, role)
		case wire.ServiceDisableNow:
			a.Roles = slices.DeleteFunc(a.Roles, func(r string) bool { return r == role })
		}
	})
}
