package helper

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/2SSK/tops/internal/wire"
)

const applyA, applyB = "20261124T120000Z-aaaa", "20261124T130000Z-bbbb"

// newWriteNode is a fake node whose /etc/passwd and /etc/group name the test's own uid and
// gid root and temporal, so write can chown without being root.
func newWriteNode(t *testing.T) Host {
	t.Helper()
	root := t.TempDir()
	etc := filepath.Join(root, "etc")
	uid, gid := os.Getuid(), os.Getgid()
	writeFile(t, filepath.Join(etc, "passwd"), fmt.Sprintf("root:x:%d:%d::/root:/bin/sh\n", uid, gid))
	writeFile(t, filepath.Join(etc, "group"), fmt.Sprintf("root:x:%d:\ntemporal:x:%d:\n", gid, gid))
	return Host{Root: root, Etc: etc}
}

var (
	sharedEnv   = wire.File{Path: "/etc/temporal/temporal.env", Content: []byte("TEMPORAL_DB_PASSWORD=s3cret\n"), Mode: "0600", Owner: "root", Group: "root"}
	historyYAML = wire.File{Path: "/etc/temporal/history.yaml", Content: []byte("persistence: {}\n"), Mode: "0640", Owner: "root", Group: "temporal"}
)

func mustWrite(t *testing.T, h Host, applyID string, w wire.Write) wire.WriteResult {
	t.Helper()
	r, err := h.Write(applyID, w)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// manifest is the apply's snapshot manifest on h.
func manifest(t *testing.T, h Host, applyID string) snapshotManifest {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(h.Root, snapshotsDir, applyID, "manifest.json")) //nolint:gosec // the test's own temp dir
	if err != nil {
		t.Fatal(err)
	}
	var m snapshotManifest
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// TestWriteFresh: on a node without the files, write puts each in place with its mode, and
// first records that they were absent; it leaves no temporary file behind.
func TestWriteFresh(t *testing.T) {
	h := newWriteNode(t)
	r := mustWrite(t, h, applyA, wire.Write{Files: []wire.File{sharedEnv, historyYAML}})
	if want := (wire.WriteResult{Snapshot: applyA, Written: []string{sharedEnv.Path, historyYAML.Path}}); !reflect.DeepEqual(r, want) {
		t.Errorf("result %+v, want %+v", r, want)
	}
	for _, f := range []wire.File{sharedEnv, historyYAML} {
		full := filepath.Join(h.Root, f.Path)
		b, _ := os.ReadFile(full) //nolint:gosec // the test's own temp dir
		fi, err := os.Stat(full)
		if err != nil || string(b) != string(f.Content) || fmt.Sprintf("%04o", fi.Mode().Perm()) != f.Mode {
			t.Errorf("%s: %q %v, want %q %s", f.Path, b, err, f.Content, f.Mode)
		}
	}
	want := snapshotManifest{ApplyID: applyA, Entries: []snapshotEntry{{Path: sharedEnv.Path, Kind: "absent"}, {Path: historyYAML.Path, Kind: "absent"}}}
	if m := manifest(t, h, applyA); !reflect.DeepEqual(m, want) {
		t.Errorf("manifest %+v, want %+v", m, want)
	}
	if fi, err := os.Stat(filepath.Join(h.Root, snapshotsDir, applyA)); err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("snapshot dir: %v %v, want 0700", fi.Mode(), err)
	}
	if left, _ := filepath.Glob(filepath.Join(h.Root, "etc/temporal/.*")); len(left) != 0 {
		t.Errorf("temporary files left: %q", left)
	}
}

// TestWriteAgain: a write of what is already there changes nothing and saves no snapshot; a
// mode that differs alone is a change.
func TestWriteAgain(t *testing.T) {
	h := newWriteNode(t)
	w := wire.Write{Files: []wire.File{sharedEnv, historyYAML}}
	mustWrite(t, h, applyA, w)
	if r := mustWrite(t, h, applyB, w); !reflect.DeepEqual(r, wire.WriteResult{}) {
		t.Errorf("second write = %+v, want no change", r)
	}
	if _, err := os.Stat(filepath.Join(h.Root, snapshotsDir, applyB)); !errors.Is(err, os.ErrNotExist) {
		t.Error("a write that changed nothing saved a snapshot")
	}
	loose := historyYAML
	loose.Mode = "0644"
	if r := mustWrite(t, h, applyB, wire.Write{Files: []wire.File{sharedEnv, loose}}); !reflect.DeepEqual(r.Written, []string{loose.Path}) {
		t.Errorf("mode change: written %q, want only %s", r.Written, loose.Path)
	}
}

// TestWriteSnapshotsBefore: a file tops replaces is saved first, content by its sha256 with
// its mode, owner and group, and a link with its target.
func TestWriteSnapshotsBefore(t *testing.T) {
	h := newWriteNode(t)
	writeFile(t, filepath.Join(h.Root, historyYAML.Path), "hand edit\n")
	if err := os.Symlink("/elsewhere", filepath.Join(h.Root, sharedEnv.Path)); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, h, applyA, wire.Write{Files: []wire.File{historyYAML, sharedEnv}})
	want := []snapshotEntry{
		{Path: historyYAML.Path, Kind: "file", SHA256: sum([]byte("hand edit\n")), Mode: "0644", Owner: "root", Group: "temporal"},
		{Path: sharedEnv.Path, Kind: "symlink", Target: "/elsewhere"},
	}
	if m := manifest(t, h, applyA); !reflect.DeepEqual(m.Entries, want) {
		t.Errorf("entries %+v, want %+v", m.Entries, want)
	}
	saved := filepath.Join(h.Root, snapshotsDir, applyA, "files", sum([]byte("hand edit\n")))
	if b, err := os.ReadFile(saved); err != nil || string(b) != "hand edit\n" { //nolint:gosec // the test's own temp dir
		t.Errorf("saved content %q, %v", b, err)
	}
	if fi, err := os.Lstat(filepath.Join(h.Root, sharedEnv.Path)); err != nil || !fi.Mode().IsRegular() {
		t.Errorf("%s is still a link: %v", sharedEnv.Path, err)
	}
}

// TestWriteSnapshotKeepsFirst: an apply writes a node several times; its snapshot keeps each
// path as it was before the apply's first write, which is what rollback must bring back. The
// next apply's snapshot starts from what this one wrote.
func TestWriteSnapshotKeepsFirst(t *testing.T) {
	h := newWriteNode(t)
	mustWrite(t, h, applyA, wire.Write{Files: []wire.File{historyYAML}})
	v2 := historyYAML
	v2.Content = []byte("v2\n")
	mustWrite(t, h, applyA, wire.Write{Files: []wire.File{v2, sharedEnv}})
	want := []snapshotEntry{{Path: historyYAML.Path, Kind: "absent"}, {Path: sharedEnv.Path, Kind: "absent"}}
	if m := manifest(t, h, applyA); !reflect.DeepEqual(m.Entries, want) {
		t.Errorf("entries %+v, want %+v", m.Entries, want)
	}
	mustWrite(t, h, applyB, wire.Write{Files: []wire.File{historyYAML}})
	if e := manifest(t, h, applyB).Entries; len(e) != 1 || e[0].SHA256 != sum([]byte("v2\n")) {
		t.Errorf("next apply's entries %+v, want v2", e)
	}
}

// TestWriteRemove: a path tops no longer renders is saved, then removed; one already absent
// is no change.
func TestWriteRemove(t *testing.T) {
	h := newWriteNode(t)
	limits := "/etc/systemd/system/temporal@matching.service.d/limits.conf"
	writeFile(t, filepath.Join(h.Root, limits), "[Service]\n")
	r := mustWrite(t, h, applyA, wire.Write{Remove: []string{limits, "/etc/temporal/matching.yaml"}})
	if want := (wire.WriteResult{Snapshot: applyA, Removed: []string{limits}}); !reflect.DeepEqual(r, want) {
		t.Errorf("result %+v, want %+v", r, want)
	}
	if _, err := os.Lstat(filepath.Join(h.Root, limits)); !errors.Is(err, os.ErrNotExist) {
		t.Error("not removed")
	}
	if e := manifest(t, h, applyA).Entries; len(e) != 1 || e[0].SHA256 != sum([]byte("[Service]\n")) {
		t.Errorf("entries %+v, want the removed file", e)
	}
	if r := mustWrite(t, h, applyB, wire.Write{Remove: []string{limits}}); !reflect.DeepEqual(r, wire.WriteResult{}) {
		t.Errorf("removing again = %+v", r)
	}
}

// TestWriteUnknownOwner: an owner the node does not have fails before anything is saved or
// written.
func TestWriteUnknownOwner(t *testing.T) {
	h := newWriteNode(t)
	f := historyYAML
	f.Group = "nobody"
	_, err := h.Write(applyA, wire.Write{Files: []wire.File{sharedEnv, f}})
	var we *wire.Error
	if !errors.As(err, &we) || we.Code != codeFileWrite || !strings.Contains(we.Detail, "group nobody") {
		t.Errorf("err = %v, want file.write naming the group", err)
	}
	if _, err := os.Stat(filepath.Join(h.Root, "etc/temporal")); !errors.Is(err, os.ErrNotExist) {
		t.Error("something was written")
	}
}

func writeRequest(t *testing.T, applyID string, w wire.Write) string {
	t.Helper()
	args, err := json.Marshal(w)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(wire.Request{RunID: "r-1", ApplyID: applyID, By: "ssk@thinkpad", Args: args})
	return string(b)
}

// TestRunWrite: through the op, only for the lease holder; the secret it writes is in neither
// the response nor the log.
func TestRunWrite(t *testing.T) {
	h := newWriteNode(t)
	h.Now = func() time.Time { return t0 }
	req := writeRequest(t, applyA, wire.Write{Files: []wire.File{sharedEnv}})
	if code, resp, _ := run(t, Env{Host: h, Root: true}, wire.OpWrite, req); code != ExitNotOK || resp.Error.Code != codeLockLost {
		t.Errorf("without the lease: exit %d, %+v", code, resp.Error)
	}
	leased(t, &h, "r-1")
	code, resp, log := run(t, Env{Host: h, Root: true}, wire.OpWrite, req)
	var r wire.WriteResult
	if code != ExitOK || json.Unmarshal(resp.Result, &r) != nil || r.Snapshot != applyA {
		t.Fatalf("exit %d, result %s", code, resp.Result)
	}
	if strings.Contains(string(resp.Result)+log, "s3cret") {
		t.Errorf("the secret is in the response or the log:\n%s\n%s", resp.Result, log)
	}
}

// TestWriteRefusesBadArgs: root writes only the files render makes, with plain permissions,
// an owner, under an apply id; anything else is a bad request before anything changes.
func TestWriteRefusesBadArgs(t *testing.T) {
	setuid, short, noOwner := historyYAML, historyYAML, historyYAML
	setuid.Mode, short.Mode, noOwner.Owner = "4755", "640", ""
	other := func(path string) wire.File { f := historyYAML; f.Path = path; return f }
	for name, c := range map[string]struct {
		applyID string
		w       wire.Write
	}{
		"no apply id": {"", wire.Write{Files: []wire.File{historyYAML}}},
		"passwd":      {applyA, wire.Write{Files: []wire.File{other("/etc/passwd")}}},
		"dot dot":     {applyA, wire.Write{Files: []wire.File{other("/etc/temporal/../passwd")}}},
		"nested":      {applyA, wire.Write{Files: []wire.File{other("/etc/temporal/a/b.yaml")}}},
		"remove":      {applyA, wire.Write{Remove: []string{"/etc/shadow"}}},
		"setuid":      {applyA, wire.Write{Files: []wire.File{setuid}}},
		"short mode":  {applyA, wire.Write{Files: []wire.File{short}}},
		"no owner":    {applyA, wire.Write{Files: []wire.File{noOwner}}},
		"twice":       {applyA, wire.Write{Files: []wire.File{historyYAML}, Remove: []string{historyYAML.Path}}},
	} {
		t.Run(name, func(t *testing.T) {
			h := newWriteNode(t)
			code, resp, _ := run(t, Env{Host: h, Root: true}, wire.OpWrite, writeRequest(t, c.applyID, c.w))
			if code != ExitBadRequest || resp.Error == nil || resp.Error.Code != codeBadRequest {
				t.Errorf("exit %d, error %+v, want a bad request", code, resp.Error)
			}
			if _, err := os.Stat(filepath.Join(h.Root, "var/lib/tops")); !errors.Is(err, os.ErrNotExist) {
				t.Error("the request reached the node's state")
			}
		})
	}
}
