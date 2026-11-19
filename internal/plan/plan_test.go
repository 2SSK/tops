package plan

import (
	"reflect"
	"strings"
	"testing"

	"github.com/2SSK/tops/internal/render"
	"github.com/2SSK/tops/internal/wire"
)

const password = "s3cret-pass-1"

// files is a small node: one shared file holding the secret, and two roles' env files.
func files() render.FileSet {
	return render.FileSet{
		{Path: "/etc/temporal/temporal.env", Content: []byte("# header\nTOPS_PG_PASSWORD=" + password + "\n"), Mode: 0o600, Owner: "root", Group: "root", Effect: render.RestartAll, Secret: true},
		{Path: "/etc/temporal/frontend.env", Content: []byte("GOMEMLIMIT=400MiB\n"), Mode: 0o640, Owner: "root", Group: "temporal", Role: "frontend", Effect: render.RestartRole},
		{Path: "/etc/temporal/history.env", Content: []byte("GOMEMLIMIT=1000MiB\n"), Mode: 0o640, Owner: "root", Group: "temporal", Role: "history", Effect: render.RestartRole},
	}
}

// installed is a node where tops applied files() and enabled both roles: every file as
// tops would write it, recorded in applied.json, and 1.31.3 staged.
func installed() wire.Facts {
	f := wire.Facts{Files: map[string]wire.FileState{}, Unknown: []string{}, Releases: []string{"1.31.3"},
		Applied: &wire.Applied{Files: map[string]wire.AppliedFile{}, Roles: []string{"frontend", "history"}}}
	for _, file := range files() {
		f.Files[file.Path] = wire.FileState{Exists: true, SHA256: sha(file.Content), Mode: mode(file), Owner: file.Owner, Group: file.Group}
		f.Applied.Files[file.Path] = wire.AppliedFile{SHA256: sha(file.Content)}
	}
	return f
}

// edit gives the node's copy of path other content, as facts reports it: content travels
// because it differs from what tops would write.
func edit(f wire.Facts, path, content string) {
	st := f.Files[path]
	st.SHA256, st.Content = sha([]byte(content)), []byte(content)
	f.Files[path] = st
}

func TestWant(t *testing.T) {
	got := Want(files())
	if got.Want["/etc/temporal/history.env"] != sha([]byte("GOMEMLIMIT=1000MiB\n")) || len(got.Want) != 3 {
		t.Errorf("want = %v", got.Want)
	}
	if !reflect.DeepEqual(got.Dirs, []string{"/etc/temporal"}) {
		t.Errorf("dirs = %v", got.Dirs)
	}
}

// TestBareNode: a node tops never touched. Every file is created, every role started, and
// the files cannot be validated because no release is staged yet.
func TestBareNode(t *testing.T) {
	bare := wire.Facts{Files: map[string]wire.FileState{}, Unknown: []string{}, Releases: []string{}}
	p := Node("vps-01", "1.31.3", files(), bare, []string{password})
	if p.Name != "vps-01" || !p.Reachable || !p.Changes() {
		t.Errorf("plan = %+v", p)
	}
	var creates []FileChange
	for _, f := range files() {
		creates = append(creates, FileChange{Path: f.Path, Role: f.Role, Action: Create, Effect: f.Effect})
	}
	if !reflect.DeepEqual(p.Files, creates) {
		t.Errorf("files = %+v, want a create without a diff for each", p.Files)
	}
	want := []RoleChange{{Role: "frontend", Action: Start}, {Role: "history", Action: Start}}
	if !reflect.DeepEqual(p.Roles, want) {
		t.Errorf("roles = %+v", p.Roles)
	}
	if p.Validated.OK || p.Validated.Release != "1.31.3" || !reflect.DeepEqual(p.Validated.Checks, []string{"not validated: 1.31.3 not staged"}) {
		t.Errorf("validated = %+v", p.Validated)
	}
}

// TestNothingToDo: the node holds exactly what tops would write: no file, no restart.
func TestNothingToDo(t *testing.T) {
	p := Node("vps-01", "1.31.3", files(), installed(), nil)
	if p.Changes() || len(p.Files) != 0 || len(p.Validated.Checks) != 0 {
		t.Errorf("plan = %+v", p)
	}
	want := []RoleChange{{Role: "frontend", Action: Keep}, {Role: "history", Action: Keep}}
	if !reflect.DeepEqual(p.Roles, want) {
		t.Errorf("roles = %+v", p.Roles)
	}
}

// TestUpdateRestartsItsRole: a role's file changes: an update with a diff, and that role
// restarts, not the other.
func TestUpdateRestartsItsRole(t *testing.T) {
	f := installed()
	edit(f, "/etc/temporal/history.env", "GOMEMLIMIT=900MiB\n")
	f.Applied.Files["/etc/temporal/history.env"] = wire.AppliedFile{SHA256: f.Files["/etc/temporal/history.env"].SHA256} // an earlier apply wrote 900
	p := Node("vps-01", "1.31.3", files(), f, nil)
	if len(p.Files) != 1 || p.Files[0].Action != Update || p.Files[0].Drift {
		t.Fatalf("files = %+v", p.Files)
	}
	if d := p.Files[0].Diff; !strings.Contains(d, "-GOMEMLIMIT=900MiB") || !strings.Contains(d, "+GOMEMLIMIT=1000MiB") || !strings.HasPrefix(d, "--- /etc/temporal/history.env\n+++ /etc/temporal/history.env\n") {
		t.Errorf("diff:\n%s", d)
	}
	want := []RoleChange{{Role: "frontend", Action: Keep}, {Role: "history", Action: Keep, Restart: true}}
	if !reflect.DeepEqual(p.Roles, want) {
		t.Errorf("roles = %+v", p.Roles)
	}
}

// TestSecretsMasked: a password change restarts every role, and the diff names the key
// without either value. tops does not know the node's old password, so the diff cannot rely
// on knowing what to hide.
func TestSecretsMasked(t *testing.T) {
	const old = "old-pass-77"
	f := installed()
	edit(f, "/etc/temporal/temporal.env", "# header\nTOPS_PG_PASSWORD="+old+"\n")
	p := Node("vps-01", "1.31.3", files(), f, []string{password})
	d := p.Files[0].Diff
	if strings.Contains(d, password) || strings.Contains(d, old) {
		t.Errorf("diff shows a secret:\n%s", d)
	}
	if !strings.Contains(d, "\n-TOPS_PG_PASSWORD=***\n+TOPS_PG_PASSWORD=*** (changed)\n") || !strings.Contains(d, " # header\n") {
		t.Errorf("diff does not show which key changed:\n%s", d)
	}
	for _, r := range p.Roles {
		if !r.Restart {
			t.Errorf("%s not restarted for a shared file", r.Role)
		}
	}
}

// TestSecretKeyAdded: a key the node does not have yet is added, masked, not "changed".
func TestSecretKeyAdded(t *testing.T) {
	f := installed()
	edit(f, "/etc/temporal/temporal.env", "# header\n")
	d := Node("vps-01", "1.31.3", files(), f, nil).Files[0].Diff
	if !strings.Contains(d, "+TOPS_PG_PASSWORD=***\n") || strings.Contains(d, "changed") || strings.Contains(d, password) {
		t.Errorf("diff:\n%s", d)
	}
}

// TestOtherFilesMasked: a secret that turns up in any other file is masked too.
func TestOtherFilesMasked(t *testing.T) {
	f := installed()
	edit(f, "/etc/temporal/history.env", "PW="+password+"\n")
	d := Node("vps-01", "1.31.3", files(), f, []string{password, ""}).Files[0].Diff
	if strings.Contains(d, password) || !strings.Contains(d, "-PW=***") {
		t.Errorf("diff:\n%s", d)
	}
}

// TestModeOrOwnerOnly: the right content with the wrong mode or owner is still an update,
// with nothing to diff.
func TestModeOrOwnerOnly(t *testing.T) {
	for name, change := range map[string]func(*wire.FileState){
		"mode":  func(s *wire.FileState) { s.Mode = "0644" },
		"owner": func(s *wire.FileState) { s.Owner = "temporal" },
		"group": func(s *wire.FileState) { s.Group = "root" },
	} {
		f := installed()
		st := f.Files["/etc/temporal/frontend.env"]
		change(&st)
		f.Files["/etc/temporal/frontend.env"] = st
		p := Node("vps-01", "1.31.3", files(), f, nil)
		if len(p.Files) != 1 || p.Files[0].Action != Update || p.Files[0].Diff != "" {
			t.Errorf("%s: files = %+v", name, p.Files)
		}
	}
}

// TestDrift: a file edited by hand, or deleted, since the last apply is drift; apply writes
// tops' version back.
func TestDrift(t *testing.T) {
	f := installed()
	edit(f, "/etc/temporal/history.env", "GOMEMLIMIT=5000MiB # tuned by hand\n")
	f.Files["/etc/temporal/frontend.env"] = wire.FileState{Exists: false}
	p := Node("vps-01", "1.31.3", files(), f, nil)
	got := map[string]FileChange{}
	for _, c := range p.Files {
		got[c.Path] = c
	}
	if c := got["/etc/temporal/history.env"]; c.Action != Update || !c.Drift {
		t.Errorf("edited: %+v", c)
	}
	if c := got["/etc/temporal/frontend.env"]; c.Action != Create || !c.Drift {
		t.Errorf("deleted: %+v", c)
	}
}

// TestRoleDropped: a role the spec no longer lists stops, and a file in a tops-owned
// directory that tops does not render is removed.
func TestRoleDropped(t *testing.T) {
	f := installed()
	f.Applied.Roles = append(f.Applied.Roles, "worker")
	f.Unknown = []string{"/etc/temporal/worker.env"}
	p := Node("vps-01", "1.31.3", files(), f, nil)
	if len(p.Files) != 1 || p.Files[0] != (FileChange{Path: "/etc/temporal/worker.env", Action: Remove}) {
		t.Errorf("files = %+v", p.Files)
	}
	if last := p.Roles[len(p.Roles)-1]; last != (RoleChange{Role: "worker", Action: Stop}) || !p.Changes() {
		t.Errorf("roles = %+v", p.Roles)
	}
}
