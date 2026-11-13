package helper

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/2SSK/tops/internal/wire"
)

func sha(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// factsNode is a node after one apply: two rendered files (one edited by hand since), a
// file tops never rendered, two releases, and current on the newer one. The test's own
// user owns every file, named deploy in /etc/passwd and /etc/group.
func factsNode(t *testing.T) Host {
	t.Helper()
	uid, gid := os.Getuid(), os.Getgid()
	h := withAffinity(fakeHost(t, map[string]string{
		"etc/passwd":                     fmt.Sprintf("root:x:0:0::/root:/bin/sh\ndeploy:x:%d:%d::/home/deploy:/bin/sh\n", uid, gid),
		"etc/group":                      fmt.Sprintf("root:x:0:\ndeploy:x:%d:\n", gid),
		"etc/temporal/history.yaml":      "rendered\n",
		"etc/temporal/temporal.env":      "edited by hand\n",
		"etc/temporal/old.yaml":          "left over\n",
		"etc/temporal/sub/extra.yaml":    "left over too\n",
		"opt/temporal/releases/1.30.2/x": "",
		"opt/temporal/releases/1.31.3/x": "",
		"opt/temporal/releases/notes":    "a file, not a release\n",
	}), 12)
	for name, mode := range map[string]os.FileMode{"etc/temporal/history.yaml": 0o640, "etc/temporal/temporal.env": 0o600} {
		if err := os.Chmod(filepath.Join(h.Root, name), mode); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("/opt/temporal/releases/1.31.3", filepath.Join(h.Root, "opt/temporal/current")); err != nil {
		t.Fatal(err)
	}
	return h
}

func TestFacts(t *testing.T) {
	got, err := factsNode(t).Facts(t.Context(), wire.FactsArgs{
		Want: map[string]string{
			"/etc/temporal/history.yaml":  sha("rendered\n"),
			"/etc/temporal/temporal.env":  sha("what tops would write\n"),
			"/etc/temporal/matching.yaml": sha("not there yet\n"),
		},
		Dirs: []string{"/etc/temporal", "/etc/temporal-ui"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := wire.Facts{
		Files: map[string]wire.FileState{
			"/etc/temporal/history.yaml": {Exists: true, SHA256: sha("rendered\n"), Mode: "0640", Owner: "deploy", Group: "deploy"},
			"/etc/temporal/temporal.env": {Exists: true, SHA256: sha("edited by hand\n"), Mode: "0600", Owner: "deploy", Group: "deploy",
				Content: []byte("edited by hand\n")},
			"/etc/temporal/matching.yaml": {Exists: false},
		},
		Unknown:  []string{"/etc/temporal/old.yaml", "/etc/temporal/sub/extra.yaml"},
		Current:  "/opt/temporal/releases/1.31.3",
		Releases: []string{"1.30.2", "1.31.3"},
		Units:    map[string]wire.UnitState{},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
}

func TestFactsBareNode(t *testing.T) {
	h := withAffinity(fakeHost(t, map[string]string{"etc/passwd": "", "etc/group": ""}), 12)
	got, err := h.Facts(t.Context(), wire.FactsArgs{Dirs: []string{"/etc/temporal"}})
	if err != nil {
		t.Fatal(err)
	}
	// Empty lists, not nil: JSON writes [] so the workstation never sees null.
	want := wire.Facts{Files: map[string]wire.FileState{}, Unknown: []string{}, Releases: []string{}, Units: map[string]wire.UnitState{}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v", got)
	}
}

func TestFactsOwnerWithoutName(t *testing.T) {
	h := factsNode(t)
	if err := os.WriteFile(filepath.Join(h.Etc, "passwd"), []byte("root:x:0:0::/root:/bin/sh\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := h.Facts(t.Context(), wire.FactsArgs{Want: map[string]string{"/etc/temporal/history.yaml": ""}})
	if err != nil {
		t.Fatal(err)
	}
	if st := got.Files["/etc/temporal/history.yaml"]; st.Owner != fmt.Sprint(os.Getuid()) || st.Group != "deploy" {
		t.Errorf("owner %q group %q: an id without a name is the number", st.Owner, st.Group)
	}
}

func TestFactsErrors(t *testing.T) {
	tests := []struct {
		name   string
		damage func(t *testing.T, h Host)
		args   wire.FactsArgs
	}{
		{"no passwd", func(t *testing.T, h Host) { remove(t, h, "etc/passwd") }, wire.FactsArgs{}},
		{"no group", func(t *testing.T, h Host) { remove(t, h, "etc/group") }, wire.FactsArgs{}},
		{"a wanted path is a directory", nil, wire.FactsArgs{Want: map[string]string{"/etc/temporal": ""}}},
		{"current is not a link", func(t *testing.T, h Host) {
			remove(t, h, "opt/temporal/current")
			write(t, h, "opt/temporal/current", "")
		}, wire.FactsArgs{}},
		{"releases is a file", func(t *testing.T, h Host) {
			remove(t, h, "opt/temporal/releases")
			write(t, h, "opt/temporal/releases", "")
		}, wire.FactsArgs{}},
		{"a tops-owned dir is a file", nil, wire.FactsArgs{Dirs: []string{"/etc/temporal/old.yaml/x"}}},
		{"applied.json cut short", func(t *testing.T, h Host) { writeApplied(t, h, `{"applyId":"20261023T153012Z-7f3a"`) }, wire.FactsArgs{}},
		{"applied.json with a field tops never writes", func(t *testing.T, h Host) {
			writeApplied(t, h, `{"applyId":"20261023T153012Z-7f3a","files":{},"roles":[],"current":"","by":"me"}`)
		}, wire.FactsArgs{}},
		{"applied.json unreadable", func(t *testing.T, h Host) {
			writeApplied(t, h, "{}")
			if err := os.Chmod(filepath.Join(h.Root, "var/lib/tops/applied.json"), 0); err != nil {
				t.Fatal(err)
			}
			if os.Getuid() == 0 {
				t.Skip("root reads a 0000 file")
			}
		}, wire.FactsArgs{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := factsNode(t)
			if tt.damage != nil {
				tt.damage(t, h)
			}
			if got, err := h.Facts(t.Context(), tt.args); err == nil {
				t.Errorf("got %+v, want an error", got)
			}
		})
	}
}

// writeApplied writes /var/lib/tops/applied.json on h, 0600 as the helper does.
func writeApplied(t *testing.T, h Host, content string) {
	t.Helper()
	p := filepath.Join(h.Root, "var/lib/tops/applied.json")
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestFactsApplied(t *testing.T) {
	h := factsNode(t)
	writeApplied(t, h, `{
		"applyId": "20261023T153012Z-7f3a",
		"files": {"/etc/temporal/history.yaml": {"sha256": "`+sha("rendered\n")+`", "mode": "0640", "owner": "root", "group": "temporal"}},
		"roles": ["history"],
		"current": "/opt/temporal/releases/1.31.3"
	}`)
	got, err := h.Facts(t.Context(), wire.FactsArgs{})
	if err != nil {
		t.Fatal(err)
	}
	want := &wire.Applied{
		ApplyID: "20261023T153012Z-7f3a",
		Files: map[string]wire.AppliedFile{
			"/etc/temporal/history.yaml": {SHA256: sha("rendered\n"), Mode: "0640", Owner: "root", Group: "temporal"},
		},
		Roles:   []string{"history"},
		Current: "/opt/temporal/releases/1.31.3",
	}
	if !reflect.DeepEqual(got.Applied, want) {
		t.Errorf("got  %+v\nwant %+v", got.Applied, want)
	}
}

func remove(t *testing.T, h Host, name string) {
	t.Helper()
	if err := os.RemoveAll(filepath.Join(h.Root, name)); err != nil {
		t.Fatal(err)
	}
}

func write(t *testing.T, h Host, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(h.Root, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRunFacts(t *testing.T) {
	env := rootEnv(t, node())
	env.Host = factsNode(t)
	code, resp, _ := run(t, env, wire.OpFacts,
		`{"runId":"r-1","args":{"want":{"/etc/temporal/temporal.env":"`+sha("other")+`"},"dirs":["/etc/temporal"]}}`)
	if code != ExitOK {
		t.Fatalf("exit %d, response %+v", code, resp.Error)
	}
	// A node tops never managed says so explicitly: applied is null, not missing.
	if !strings.Contains(string(resp.Result), `"applied":null`) {
		t.Errorf("no \"applied\":null in %s", resp.Result)
	}
	// Content crosses the wire as base64 and comes back byte for byte.
	if !strings.Contains(string(resp.Result), `"content":"ZWRpdGVkIGJ5IGhhbmQK"`) {
		t.Errorf("content is not base64 in %s", resp.Result)
	}
	var got wire.Facts
	if err := json.Unmarshal(resp.Result, &got); err != nil {
		t.Fatal(err)
	}
	if string(got.Files["/etc/temporal/temporal.env"].Content) != "edited by hand\n" {
		t.Errorf("content %q", got.Files["/etc/temporal/temporal.env"].Content)
	}
}

func TestRunFactsRefusesPaths(t *testing.T) {
	for _, args := range []string{
		`{"want":{"etc/temporal/a.yaml":""}}`,
		`{"want":{"/etc/temporal/../shadow":""}}`,
		`{"want":{"/etc/temporal/":""}}`,
		`{"dirs":["/etc/temporal/.."]}`,
		`{"dirs":["temporal"]}`,
		`{"wants":{}}`,
	} {
		t.Run(args, func(t *testing.T) {
			code, resp, _ := run(t, rootEnv(t, node()), wire.OpFacts, `{"runId":"r-1","args":`+args+`}`)
			if code != ExitBadRequest || resp.Error == nil || resp.Error.Code != "helper.bad_request" {
				t.Errorf("exit %d, response %+v", code, resp.Error)
			}
		})
	}
}
