package helper

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/2SSK/tops/internal/wire"
)

const limitsPath = "/etc/systemd/system/temporal@matching.service.d/limits.conf"

// state is each path as it is on h: its mode and content, its link target, or "absent".
func state(t *testing.T, h Host, paths ...string) []string {
	t.Helper()
	var s []string
	for _, path := range paths {
		full := filepath.Join(h.Root, path)
		if target, err := os.Readlink(full); err == nil {
			s = append(s, "-> "+target)
			continue
		}
		b, err := os.ReadFile(full) //nolint:gosec // the test's own temp dir
		if errors.Is(err, os.ErrNotExist) {
			s = append(s, "absent")
			continue
		}
		fi, serr := os.Stat(full)
		if err != nil || serr != nil {
			t.Fatal(err, serr)
		}
		s = append(s, fmt.Sprintf("%04o %s", fi.Mode().Perm(), b))
	}
	return s
}

// TestRestore: an apply that created, replaced and removed paths is undone: each is back to
// its content and mode, its link, or its absence. The restore is saved too, so restoring the
// restore brings the apply's state back.
func TestRestore(t *testing.T) {
	h := newWriteNode(t)
	role := wire.File{Path: "/etc/temporal/worker.yaml", Content: []byte("worker\n"), Mode: "0640", Owner: "root", Group: "temporal"}
	paths := []string{historyYAML.Path, sharedEnv.Path, limitsPath, role.Path}
	writeFile(t, filepath.Join(h.Root, historyYAML.Path), "hand edit\n")
	writeFile(t, filepath.Join(h.Root, limitsPath), "[Service]\n")
	if err := os.Symlink("/elsewhere", filepath.Join(h.Root, role.Path)); err != nil {
		t.Fatal(err)
	}
	before := state(t, h, paths...)
	mustWrite(t, h, applyA, wire.Write{Files: []wire.File{historyYAML, sharedEnv, role}, Remove: []string{limitsPath}})
	after := state(t, h, paths...)

	r, err := h.Restore(applyB, applyA)
	if err != nil || r.Snapshot != applyB || len(r.Written) != 3 || !slices.Equal(r.Removed, []string{sharedEnv.Path}) {
		t.Errorf("restore = %+v, %v", r, err)
	}
	wantState(t, h, before, paths...)
	if r, err := h.Restore(applyB, applyA); err != nil || !reflect.DeepEqual(r, wire.WriteResult{}) {
		t.Errorf("restoring again = %+v, %v; want no change", r, err)
	}
	if _, err := h.Restore("20261124T140000Z-cccc", applyB); err != nil {
		t.Fatal(err)
	}
	wantState(t, h, after, paths...)
}

func wantState(t *testing.T, h Host, want []string, paths ...string) {
	t.Helper()
	if got := state(t, h, paths...); !slices.Equal(got, want) {
		t.Errorf("state %q, want %q", got, want)
	}
}

// TestRestoreNumericOwner: a file whose owner has no account is saved by its numbers, and
// restored by them.
func TestRestoreNumericOwner(t *testing.T) {
	h := newWriteNode(t)
	writeFile(t, filepath.Join(h.Root, historyYAML.Path), "old\n")
	passwd, _ := os.ReadFile(filepath.Join(h.Etc, "passwd"))
	writeFile(t, filepath.Join(h.Etc, "passwd"), "root:x:4000000:0::/root:/bin/sh\n")
	if _, err := h.Write(applyA, wire.Write{Remove: []string{historyYAML.Path}}); err != nil {
		t.Fatal(err)
	}
	if e := manifest(t, h, applyA).Entries[0]; e.Owner != fmt.Sprint(os.Getuid()) {
		t.Fatalf("saved owner %q, want the uid", e.Owner)
	}
	if _, err := h.Restore(applyB, applyA); err != nil {
		t.Error(err)
	}
	writeFile(t, filepath.Join(h.Etc, "passwd"), string(passwd))
	if got := state(t, h, historyYAML.Path); got[0] != "0644 old\n" {
		t.Errorf("restored %q", got)
	}
}

// TestRestoreMissing: a snapshot the node does not have, or whose content is gone or damaged,
// is snapshot.missing, and nothing is changed: not even the paths that could be.
func TestRestoreMissing(t *testing.T) {
	saved := filepath.Join(snapshotsDir, applyA, "files", sum([]byte("old\n")))
	for name, c := range map[string]struct {
		spoil  func(t *testing.T, h Host)
		detail string
	}{
		"rotated out": {func(t *testing.T, h Host) { must(t, os.RemoveAll(filepath.Join(h.Root, snapshotsDir, applyA))) }, "no such snapshot"},
		"gone":        {func(t *testing.T, h Host) { must(t, os.Remove(filepath.Join(h.Root, saved))) }, "no such file"},
		"damaged":     {func(t *testing.T, h Host) { writeFile(t, filepath.Join(h.Root, saved), "0ld\n") }, "damaged"},
	} {
		t.Run(name, func(t *testing.T) {
			h := newWriteNode(t)
			writeFile(t, filepath.Join(h.Root, historyYAML.Path), "old\n")
			mustWrite(t, h, applyA, wire.Write{Files: []wire.File{sharedEnv, historyYAML}})
			c.spoil(t, h)
			before := state(t, h, sharedEnv.Path, historyYAML.Path)
			_, err := h.Restore(applyB, applyA)
			var we *wire.Error
			if !errors.As(err, &we) || we.Code != codeSnapshotMissing || !strings.Contains(we.Detail, c.detail) {
				t.Errorf("err = %#v, want snapshot.missing: %s", err, c.detail)
			}
			if got := state(t, h, sharedEnv.Path, historyYAML.Path); !slices.Equal(got, before) {
				t.Errorf("changed to %q", got)
			}
		})
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// snapshots are the apply ids h holds snapshots of, oldest first.
func snapshots(t *testing.T, h Host) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(h.Root, snapshotsDir))
	must(t, err)
	var ids []string
	for _, e := range entries {
		ids = append(ids, e.Name())
	}
	return ids
}

// TestSnapshotsPruned: write keeps the newest ten snapshots, its own even when a slow
// workstation clock makes it the oldest. Restore drops none: the one it restores may be the
// oldest, and a retry needs it.
func TestSnapshotsPruned(t *testing.T) {
	h := newWriteNode(t)
	id := func(i int) string { return fmt.Sprintf("20261124T12%02d00Z-aaaa", i) }
	put := func(applyID string, i int) {
		f := historyYAML
		f.Content = fmt.Appendf(nil, "v%d\n", i)
		mustWrite(t, h, applyID, wire.Write{Files: []wire.File{f}})
	}
	for i := range 12 {
		put(id(i), i)
	}
	if got := snapshots(t, h); len(got) != 10 || got[0] != id(2) || got[9] != id(11) {
		t.Errorf("kept %q, want the newest ten", got)
	}
	if _, err := h.Restore(id(12), id(2)); err != nil {
		t.Fatal(err)
	}
	if got := snapshots(t, h); len(got) != 11 || got[0] != id(2) {
		t.Errorf("after a restore: %q, want the restored one kept", got)
	}
	const slow = "20200101T000000Z-aaaa"
	put(slow, 99)
	if got := snapshots(t, h); len(got) != 10 || got[0] != slow {
		t.Errorf("a slow clock's write kept %q, want its own among ten", got)
	}
}

// TestWriteSweeps: the temporary files of a killed write, which may hold secrets, go with the
// next write or restore, even one that changes nothing; no other file does.
func TestWriteSweeps(t *testing.T) {
	h := newWriteNode(t)
	mustWrite(t, h, applyA, wire.Write{Files: []wire.File{historyYAML}})
	writeFile(t, filepath.Join(h.Root, limitsPath), "[Service]\n")
	left := []string{"/etc/temporal/.temporal.env.tops-123", "/etc/systemd/system/.temporal@.service.tops-123", filepath.Join(filepath.Dir(limitsPath), ".limits.conf.tops-123")}
	for _, path := range append(left, "/etc/temporal/.hidden") {
		writeFile(t, filepath.Join(h.Root, path), "x")
	}
	mustWrite(t, h, applyB, wire.Write{Files: []wire.File{historyYAML}})
	if got := state(t, h, append(left, "/etc/temporal/.hidden", limitsPath)...); !slices.Equal(got, []string{"absent", "absent", "absent", "0644 x", "0644 [Service]\n"}) {
		t.Errorf("after a write: %q", got)
	}
}

func restoreRequest(t *testing.T, applyID, snapshot string) string {
	t.Helper()
	args, err := json.Marshal(wire.Restore{Snapshot: snapshot})
	must(t, err)
	b, _ := json.Marshal(wire.Request{RunID: "r-1", ApplyID: applyID, By: "ssk@thinkpad", Args: args})
	return string(b)
}

// TestRunRestore: through the op, only for the lease holder; ids that are not apply ids, or
// are the same, are a bad request; the secret it restores is in neither the response nor the
// log.
func TestRunRestore(t *testing.T) {
	h := newWriteNode(t)
	h.Now = func() time.Time { return t0 }
	mustWrite(t, h, applyA, wire.Write{Files: []wire.File{sharedEnv}})
	mustWrite(t, h, applyB, wire.Write{Remove: []string{sharedEnv.Path}})
	const undo = "20261124T140000Z-cccc"
	for _, ids := range [][2]string{{"", applyB}, {undo, "../../etc"}, {applyB, applyB}} {
		if code, resp, _ := run(t, Env{Host: h, Root: true}, wire.OpRestore, restoreRequest(t, ids[0], ids[1])); code != ExitBadRequest || resp.Error.Code != codeBadRequest {
			t.Errorf("%q: exit %d, %+v, want a bad request", ids, code, resp.Error)
		}
	}
	if code, resp, _ := run(t, Env{Host: h, Root: true}, wire.OpRestore, restoreRequest(t, undo, applyB)); code != ExitNotOK || resp.Error.Code != codeLockLost {
		t.Errorf("without the lease: exit %d, %+v", code, resp.Error)
	}
	leased(t, &h, "r-1")
	code, resp, log := run(t, Env{Host: h, Root: true}, wire.OpRestore, restoreRequest(t, undo, applyB))
	if got := state(t, h, sharedEnv.Path); code != ExitOK || got[0] != "0600 "+string(sharedEnv.Content) {
		t.Errorf("exit %d, %+v, restored %q", code, resp.Error, got)
	}
	if strings.Contains(string(resp.Result)+log, "s3cret") {
		t.Errorf("the secret is in the response or the log:\n%s\n%s", resp.Result, log)
	}
}
