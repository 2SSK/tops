package helper

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/2SSK/tops/internal/wire"
)

// applyC is a third apply, after applyA and applyB.
const applyC = "20261124T140000Z-cccc"

// appliedOn is h's applied.json, decoded as facts reads it; nil when there is none.
func appliedOn(t *testing.T, h Host) *wire.Applied {
	t.Helper()
	a, err := h.applied()
	must(t, err)
	return a
}

// recorded is f as applied.json records it.
func recorded(f wire.File) wire.AppliedFile {
	return wire.AppliedFile{SHA256: sha(string(f.Content)), Mode: f.Mode, Owner: f.Owner, Group: f.Group}
}

// TestWriteRecordsApplied: applied.json holds every file as the last write left it, root's
// alone, and forgets a removed one; a write that changes nothing keeps the apply id that last
// changed it.
func TestWriteRecordsApplied(t *testing.T) {
	h := newWriteNode(t)
	mustWrite(t, h, applyA, wire.Write{Files: []wire.File{sharedEnv, historyYAML}})
	want := &wire.Applied{ApplyID: applyA, Files: map[string]wire.AppliedFile{sharedEnv.Path: recorded(sharedEnv), historyYAML.Path: recorded(historyYAML)}}
	if got := appliedOn(t, h); !reflect.DeepEqual(got, want) {
		t.Errorf("after the first write:\n got %+v\nwant %+v", got, want)
	}
	for path, want := range map[string]os.FileMode{stateDir: os.ModeDir | 0o700, appliedFile: 0o600} {
		if info, err := os.Stat(filepath.Join(h.Root, path)); err != nil || info.Mode() != want {
			t.Errorf("%s: %v, %v; want %v", path, info.Mode(), err, want)
		}
	}
	mustWrite(t, h, applyB, wire.Write{Files: []wire.File{historyYAML}})
	if got := appliedOn(t, h); !reflect.DeepEqual(got, want) {
		t.Errorf("a write that changed nothing changed applied.json:\n got %+v", got)
	}
	mustWrite(t, h, applyC, wire.Write{Remove: []string{sharedEnv.Path}})
	want = &wire.Applied{ApplyID: applyC, Files: map[string]wire.AppliedFile{historyYAML.Path: recorded(historyYAML)}}
	if got := appliedOn(t, h); !reflect.DeepEqual(got, want) {
		t.Errorf("after a removal:\n got %+v\nwant %+v", got, want)
	}
}

// TestWriteRecordsRetry: files already as wanted, but never recorded, as after a write whose
// record failed: the retry changes no file and records them.
func TestWriteRecordsRetry(t *testing.T) {
	h := newWriteNode(t)
	mustWrite(t, h, applyA, wire.Write{Files: []wire.File{historyYAML}})
	must(t, os.Remove(filepath.Join(h.Root, appliedFile)))
	if r := mustWrite(t, h, applyA, wire.Write{Files: []wire.File{historyYAML}}); len(r.Written) != 0 {
		t.Fatalf("retry = %+v, want no change", r)
	}
	if got := appliedOn(t, h); got == nil || got.Files[historyYAML.Path] != recorded(historyYAML) {
		t.Errorf("after the retry: %+v", got)
	}
}

// TestRestoreRecordsApplied: after a restore applied.json records the files as the snapshot
// brought them back, under the restore's apply id, so drift does not take them for hand edits.
func TestRestoreRecordsApplied(t *testing.T) {
	h := newWriteNode(t)
	// Both of the test node's groups have the test's gid: a snapshot names it temporal.
	matchingYAML := wire.File{Path: "/etc/temporal/matching.yaml", Content: []byte("persistence: {}\n"), Mode: "0640", Owner: "root", Group: "temporal"}
	mustWrite(t, h, applyA, wire.Write{Files: []wire.File{matchingYAML, historyYAML}})
	before := appliedOn(t, h).Files
	changed := historyYAML
	changed.Content = []byte("persistence: {numHistoryShards: 4}\n")
	mustWrite(t, h, applyB, wire.Write{Files: []wire.File{changed}, Remove: []string{matchingYAML.Path}})
	if _, err := h.Restore(applyC, applyB); err != nil {
		t.Fatal(err)
	}
	if got := appliedOn(t, h); got.ApplyID != applyC || !reflect.DeepEqual(got.Files, before) {
		t.Errorf("after the restore:\n got %+v\nwant files %+v", got, before)
	}
}

// TestServiceRecordsRoles: enable-now records its role, disable-now forgets it, each once;
// the other actions and a failed job record nothing.
func TestServiceRecordsRoles(t *testing.T) {
	sd := twoRoles()
	h := hostWith(t, sd)
	steps := []struct {
		unit, action, result string
		roles                []string
		applyID              string
	}{
		{historyUnit, wire.ServiceEnableNow, "", []string{"history"}, applyA},
		{"temporal@matching.service", wire.ServiceEnableNow, "", []string{"history", "matching"}, applyB},
		{historyUnit, wire.ServiceEnableNow, "", []string{"history", "matching"}, applyB},
		{historyUnit, wire.ServiceRestart, "", []string{"history", "matching"}, applyB},
		{historyUnit, wire.ServiceDisableNow, "failed", []string{"history", "matching"}, applyB},
		{historyUnit, wire.ServiceDisableNow, "", []string{"matching"}, applyC},
		{"temporal@worker.service", wire.ServiceEnableNow, "failed", []string{"matching"}, applyC},
	}
	for _, step := range steps {
		sd.result = step.result
		_, err := h.Service(t.Context(), step.applyID, wire.Service{Unit: step.unit, Action: step.action})
		if (err != nil) != (step.result != "") {
			t.Fatalf("%s %s: %v", step.action, step.unit, err)
		}
		if got := appliedOn(t, h); !reflect.DeepEqual(got.Roles, step.roles) || got.ApplyID != step.applyID {
			t.Errorf("after %s %s (%s): %+v, want roles %q by %s", step.action, step.unit, step.result, got, step.roles, step.applyID)
		}
	}
}

// TestRecordCorrupt: an applied.json the helper cannot read stops the write before it records,
// and is left as it is for an operator to see.
func TestRecordCorrupt(t *testing.T) {
	h := newWriteNode(t)
	writeApplied(t, h, `{"applyId":`)
	if _, err := h.Write(applyA, wire.Write{Files: []wire.File{historyYAML}}); err == nil {
		t.Fatal("a write over a corrupt applied.json succeeded")
	}
	if b, _ := os.ReadFile(filepath.Join(h.Root, appliedFile)); string(b) != `{"applyId":` {
		t.Errorf("applied.json is now %q", b)
	}
}
