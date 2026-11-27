package helper

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"

	"github.com/2SSK/tops/internal/wire"
)

const (
	historyDir    = "/var/lib/tops/history"
	keepHistory   = 100
	defaultListed = 20
)

// sha256Hex is a sha256 as a change names its content: nothing else fits.
var sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)

// AppendHistory keeps r as history/<apply-id>.json, root's alone, replacing the run's own
// earlier record, then drops the records beyond the newest hundred.
func (h Host) AppendHistory(r wire.History) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	if err := makeDir(filepath.Join(h.Root, historyDir), 0o700); err != nil {
		return fileError("create", historyDir, err)
	}
	path := filepath.Join(historyDir, r.ApplyID+".json")
	if err := replaceFile(filepath.Join(h.Root, path), append(b, '\n'), 0o600, os.Geteuid(), os.Getegid()); err != nil {
		return fileError("write", path, err)
	}
	return h.keepNewest(historyDir, ".json", keepHistory, r.ApplyID)
}

// ListHistory returns the newest limit records, the newest apply id first, each saying whether
// its apply's snapshot is still on the node.
func (h Host) ListHistory(limit int) (wire.HistoryResult, error) {
	res := wire.HistoryResult{Records: []wire.History{}}
	ids, err := h.applyIDs(historyDir, ".json")
	if errors.Is(err, fs.ErrNotExist) {
		return res, nil
	}
	if err != nil {
		return res, fmt.Errorf("read %s: %w", historyDir, err)
	}
	slices.Reverse(ids)
	for _, id := range ids[:min(limit, len(ids))] {
		r, err := h.record(id)
		if err != nil {
			return wire.HistoryResult{}, err
		}
		res.Records = append(res.Records, r)
	}
	return res, nil
}

// record reads one apply's record. The helper wrote it itself, so anything it cannot decode
// strictly is corrupt.
func (h Host) record(applyID string) (wire.History, error) {
	path := filepath.Join(historyDir, applyID+".json")
	f, err := os.Open(filepath.Join(h.Root, path)) //nolint:gosec // our own state, named by an apply id
	if err != nil {
		return wire.History{}, fmt.Errorf("read %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	var r wire.History
	if err := decodeStrict(f, &r); err != nil {
		return wire.History{}, fmt.Errorf("%s is corrupt: %w", path, err)
	}
	_, err = os.Stat(filepath.Join(h.Root, snapshotsDir, applyID, "manifest.json"))
	r.Snapshot = err == nil
	return r, nil
}

func checkHistory(a wire.HistoryArgs) error {
	switch a.Action {
	case wire.HistoryList:
		if a.Record != nil || a.Limit < 0 || a.Limit > keepHistory {
			return fmt.Errorf("list takes a limit from 0 to %d, and no record", keepHistory)
		}
		return nil
	case wire.HistoryAppend:
		if a.Record == nil || a.Limit != 0 {
			return errors.New("append takes a record, and no limit")
		}
		return checkRecord(*a.Record)
	}
	return fmt.Errorf("action %q is not one", a.Action)
}

// checkRecord refuses a record without an apply id to name it by, or a result, and a change
// that is not a path and its hashes.
func checkRecord(r wire.History) error {
	if !applyIDName.MatchString(r.ApplyID) {
		return fmt.Errorf("apply id %q is not one", r.ApplyID)
	}
	if !slices.Contains([]string{wire.ResultOK, wire.ResultFailed, wire.ResultInterrupted}, r.Result) {
		return fmt.Errorf("result %q is not ok, failed or interrupted", r.Result)
	}
	for _, c := range r.Changes {
		if err := checkChange(c); err != nil {
			return fmt.Errorf("change of %q: %w", c.Path, err)
		}
	}
	return nil
}

// checkChange refuses a path that is not absolute and clean, and hashes that are not sha256s
// or do not fit the action: a create has no hash before, a remove none after.
func checkChange(c wire.Change) error {
	if !filepath.IsAbs(c.Path) || filepath.Clean(c.Path) != c.Path {
		return errors.New("the path is not absolute and clean")
	}
	before, after := c.BeforeSHA256 != "", c.AfterSHA256 != ""
	fits := map[string]bool{wire.ChangeCreate: !before && after, wire.ChangeUpdate: before && after, wire.ChangeRemove: before && !after}
	if !fits[c.Action] {
		return fmt.Errorf("%q with these hashes is not a change", c.Action)
	}
	for _, sum := range []string{c.BeforeSHA256, c.AfterSHA256} {
		if sum != "" && !sha256Hex.MatchString(sum) {
			return fmt.Errorf("%q is not a sha256", sum)
		}
	}
	return nil
}
