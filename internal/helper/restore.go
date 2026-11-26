package helper

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/2SSK/tops/internal/wire"
)

const codeSnapshotMissing = "snapshot.missing"

// Restore brings back every path the snapshot's apply changed, as it was before that apply:
// content, mode, owner and group, link, or absence. It saves what is there now under applyID,
// so a restore can be restored. Every saved content is read and checked before the first
// change. It drops no snapshot: a retry must still find the one it restores.
func (h Host) Restore(applyID, snapshot string) (wire.WriteResult, error) {
	m, ok, err := h.manifest(snapshot)
	if err != nil {
		return wire.WriteResult{}, err
	}
	if !ok {
		return wire.WriteResult{}, snapshotMissing(snapshot, errors.New("this node has no such snapshot"))
	}
	var w wire.Write
	var links []link
	for _, e := range m.Entries {
		switch e.Kind {
		case "absent":
			w.Remove = append(w.Remove, e.Path)
		case "symlink":
			links = append(links, link{e.Path, e.Target})
		default:
			b, err := h.saved(snapshot, e.SHA256)
			if err != nil {
				return wire.WriteResult{}, snapshotMissing(snapshot, fmt.Errorf("%s: %w", e.Path, err))
			}
			w.Files = append(w.Files, wire.File{Path: e.Path, Content: b, Mode: e.Mode, Owner: e.Owner, Group: e.Group})
		}
	}
	return h.put(applyID, w, links)
}

// saved is the content a snapshot holds by its sha256.
func (h Host) saved(snapshot, sum string) ([]byte, error) {
	b, err := os.ReadFile(filepath.Join(h.Root, snapshotsDir, snapshot, "files", sum)) //nolint:gosec // our own state, the id checked
	if err != nil {
		return nil, err
	}
	if got := sha256.Sum256(b); hex.EncodeToString(got[:]) != sum {
		return nil, errors.New("the saved content is damaged")
	}
	return b, nil
}

func snapshotMissing(snapshot string, err error) error {
	return &wire.Error{Code: codeSnapshotMissing, Message: "restore " + snapshot, Detail: err.Error()}
}

// checkRestore refuses ids that are not apply ids, and a restore of the run's own snapshot,
// which it is about to add to.
func checkRestore(applyID string, r wire.Restore) error {
	for _, id := range []string{applyID, r.Snapshot} {
		if !applyIDName.MatchString(id) {
			return fmt.Errorf("apply id %q is not one", id)
		}
	}
	if applyID == r.Snapshot {
		return fmt.Errorf("%s would restore its own snapshot", applyID)
	}
	return nil
}
