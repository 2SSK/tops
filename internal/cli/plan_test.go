package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/2SSK/tops/internal/fleet"
	"github.com/2SSK/tops/internal/plan"
	"github.com/2SSK/tops/internal/secret"
	"github.com/2SSK/tops/internal/sizing"
	"github.com/2SSK/tops/internal/term"
	"github.com/2SSK/tops/internal/wire"
)

const labPassword = "lab-password-1"

var labSecrets = fleet.Secrets{"TOPS_PG_PASSWORD": secret.New(labPassword)}

// sizedLab is capacity's result for the lab fixture on VPSLAB-shaped nodes.
func sizedLab(t *testing.T, hellos []wire.Hello, limits func() (sizing.DBLimits, error)) (*fleet.Env, *capacityDocument) {
	t.Helper()
	env := labEnv(t)
	d, err := sizeEnv(env, placeholderConstants(t), hellos, limits)
	if err != nil {
		t.Fatal(err)
	}
	return env, d
}

// fakeFacts answers facts for every node from a function, and records what each node was
// asked. It is called from several goroutines, as the nodes are.
type fakeFacts struct {
	mu     sync.Mutex
	asked  map[int]wire.FactsArgs
	answer func(i int, args wire.FactsArgs) wire.Facts
	err    error
}

func (f *fakeFacts) facts(_ context.Context, i int, args wire.FactsArgs) (wire.Facts, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.asked == nil {
		f.asked = map[int]wire.FactsArgs{}
	}
	f.asked[i] = args
	if f.err != nil {
		return wire.Facts{}, f.err
	}
	return f.answer(i, args), nil
}

// bare is a node tops never touched: none of the files, no release, no applied.json.
func bare(_ int, args wire.FactsArgs) wire.Facts {
	f := wire.Facts{Files: map[string]wire.FileState{}, Unknown: []string{}, Releases: []string{}}
	for path := range args.Want {
		f.Files[path] = wire.FileState{}
	}
	return f
}

// applied is a node where apply wrote exactly what tops renders: each file as the backend
// schema's "Files on a node" table says it is installed, its roles enabled, 1.31.3 staged.
func applied(env *fleet.Env) func(int, wire.FactsArgs) wire.Facts {
	return func(i int, args wire.FactsArgs) wire.Facts {
		f := wire.Facts{Files: map[string]wire.FileState{}, Unknown: []string{}, Releases: []string{"1.31.3"},
			Applied: &wire.Applied{Files: map[string]wire.AppliedFile{}, Roles: env.Cluster.Nodes[i].Roles}}
		for path, sum := range args.Want {
			st := wire.FileState{Exists: true, SHA256: sum, Mode: "0640", Owner: "root", Group: "temporal"}
			switch {
			case path == "/etc/temporal/temporal.env":
				st.Mode, st.Group = "0600", "root"
			case strings.HasPrefix(path, "/etc/systemd/"):
				st.Mode, st.Group = "0644", "root"
			}
			f.Files[path] = st
			f.Applied.Files[path] = wire.AppliedFile{SHA256: sum}
		}
		return f
	}
}

// contains fails the test for each of wants that out does not contain.
func contains(t *testing.T, out string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(out, want) {
			t.Errorf("output has no\n%s\n---\n%s", want, out)
		}
	}
}

// actions are the file and role actions of a node plan.
func actions(n plan.NodePlan) (files []plan.Action, roles []plan.RoleAction) {
	for _, f := range n.Files {
		files = append(files, f.Action)
	}
	for _, r := range n.Roles {
		roles = append(roles, r.Action)
	}
	return files, roles
}

func shaOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// TestPlanBareNodes is T2's plan: every file of every node is created, every role started,
// nothing validated, exit 2.
func TestPlanBareNodes(t *testing.T) {
	env, sized := sizedLab(t, labHellos, limitsOf(vpsPG, nil))
	ff := &fakeFacts{answer: bare}
	d, err := planEnv(t.Context(), env, sized, labSecrets, ff.facts)
	if err != nil {
		t.Fatal(err)
	}
	type shape struct {
		kind, env, cluster    string
		nodes, refusals       int
		changes, drift, exit2 bool
	}
	got := shape{d.Kind, d.Env, d.Cluster, len(d.Nodes), len(d.Refusals), d.Changes, d.Drift, term.ExitCode(d.exit()) == 2}
	if want := (shape{"Plan", "lab", "tops-e2e", 3, 0, true, false, true}); got != want {
		t.Fatalf("plan = %+v, want %+v", got, want)
	}
	// 2 shared + 4 per role: 18 on vps-01 and vps-02, 10 on vps-03.
	for i, want := range []int{18, 18, 10} {
		n := d.Nodes[i]
		files, roles := actions(n)
		if len(ff.asked[i].Want) != want || n.Name != env.Cluster.Nodes[i].Name ||
			!slices.Equal(files, slices.Repeat([]plan.Action{plan.Create}, want)) ||
			!slices.Equal(roles, slices.Repeat([]plan.RoleAction{plan.Start}, len(env.Cluster.Nodes[i].Roles))) {
			t.Errorf("%s: asked for %d files, want %d; files %v, roles %v", n.Name, len(ff.asked[i].Want), want, files, roles)
		}
	}
}

// TestPlanPageBare: the human plan of bare nodes, as app-flow "plan" draws it.
func TestPlanPageBare(t *testing.T) {
	env, sized := sizedLab(t, labHellos, limitsOf(vpsPG, nil))
	d, err := planEnv(t.Context(), env, sized, labSecrets, (&fakeFacts{answer: bare}).facts)
	if err != nil {
		t.Fatal(err)
	}
	page := planPage(d, term.Style{Unicode: true})
	contains(t, page,
		"lab (tops-e2e)  plan for 3 nodes · validated on 0 of 3 nodes\n",
		"\nvps-03\n  ! not validated: 1.31.3 not staged\n  shared\n    + /etc/systemd/system/temporal@.service",
		"\n  matching\n    + /etc/temporal/matching.yaml",
		"    + starts matching\n",
		"    + /etc/systemd/system/temporal@matching.service.d/limits.conf  create\n", // the longest path still lines up
		"    + /etc/temporal/history.env                                    create\n",
		"\n3 nodes change · 0 restarts · 0 drift\napply with: tops -e lab apply\n",
	)
	if strings.Contains(page, labPassword) {
		t.Error("the page shows the password")
	}
}

// TestPlanNothingToDo: nodes holding exactly what tops renders: no change, exit 0.
func TestPlanNothingToDo(t *testing.T) {
	env, sized := sizedLab(t, labHellos, limitsOf(vpsPG, nil))
	d, err := planEnv(t.Context(), env, sized, labSecrets, (&fakeFacts{answer: applied(env)}).facts)
	if err != nil {
		t.Fatal(err)
	}
	if d.Changes || d.Drift {
		t.Errorf("plan = %+v", d.Nodes)
	}
	if err := d.exit(); err != nil {
		t.Errorf("exit = %v", err)
	}
	if page := planPage(d, term.Style{}); !strings.HasSuffix(page, "\nnothing to do\n") {
		t.Errorf("page:\n%s", page)
	}
}

// editedPlan plans the lab where vps-01's history.env was edited by hand and its
// temporal.env holds an older password that the last apply wrote.
func editedPlan(t *testing.T) *planDocument {
	t.Helper()
	env, sized := sizedLab(t, labHellos, limitsOf(vpsPG, nil))
	answer := func(i int, args wire.FactsArgs) wire.Facts {
		f := applied(env)(i, args)
		if i == 0 {
			edited := "GOMEMLIMIT=9999MiB\n"
			f.Files["/etc/temporal/history.env"] = wire.FileState{Exists: true, SHA256: shaOf(edited), Mode: "0640", Owner: "root", Group: "temporal", Content: []byte(edited)}
			old := "TOPS_PG_PASSWORD=old-password-9\n"
			f.Files["/etc/temporal/temporal.env"] = wire.FileState{Exists: true, SHA256: shaOf(old), Mode: "0600", Owner: "root", Group: "root", Content: []byte(old)}
			f.Applied.Files["/etc/temporal/temporal.env"] = wire.AppliedFile{SHA256: shaOf(old)}
		}
		return f
	}
	d, err := planEnv(t.Context(), env, sized, labSecrets, (&fakeFacts{answer: answer}).facts)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// TestPlanUpdateAndDrift: the diffs, the restarts, the drift mark, exit 2.
func TestPlanUpdateAndDrift(t *testing.T) {
	d := editedPlan(t)
	contains(t, planPage(d, term.Style{Unicode: true}),
		"\n  shared\n    ~ /etc/temporal/temporal.env",
		"restart all roles\n",
		"        +TOPS_PG_PASSWORD=*** (changed)\n",
		"    ~ /etc/temporal/history.env",
		"        -GOMEMLIMIT=9999MiB\n",
		"    → restarts history\n",
		"\nvps-02  no changes\n",
		"  ! drift: /etc/temporal/history.env is not what tops last wrote; apply overwrites it\n",
		"\n1 nodes change · 4 restarts · 1 drift\n",
	)
	if got := term.ExitCode(d.exit()); got != 2 {
		t.Errorf("exit = %d, want 2", got)
	}
}

// TestPlanShowsNoSecret: neither the spec's password nor the node's old one is on the page
// or in --json.
func TestPlanShowsNoSecret(t *testing.T) {
	d := editedPlan(t)
	var b strings.Builder
	if err := term.WriteJSON(&b, d); err != nil {
		t.Fatal(err)
	}
	for _, out := range []string{planPage(d, term.Style{}), b.String()} {
		if strings.Contains(out, labPassword) || strings.Contains(out, "old-password-9") {
			t.Errorf("a password is in the output:\n%s", out)
		}
	}
	var doc map[string]any
	_ = json.Unmarshal([]byte(b.String()), &doc)
	if doc["kind"] != "Plan" || doc["changes"] != true || doc["drift"] != true {
		t.Errorf("json = %v", doc)
	}
}

// TestPlanNodeTooSmall: a sizing refusal plans nothing and asks no node: exit 3, with the
// refusal in --json for the T2 acceptance check.
func TestPlanNodeTooSmall(t *testing.T) {
	env, sized := sizedLab(t, []wire.Hello{hello(1280, 1.5), hello(1280, 1.5), hello(200, 1)}, limitsOf(vpsPG, nil))
	ff := &fakeFacts{answer: bare}
	d, err := planEnv(t.Context(), env, sized, labSecrets, ff.facts)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Refusals) == 0 || d.Refusals[0].Code != "refused.node_too_small" || len(d.Nodes) != 0 || len(ff.asked) != 0 {
		t.Errorf("plan = %+v, asked %d nodes", d, len(ff.asked))
	}
	if got := term.ExitCode(d.exit()); got != 3 {
		t.Errorf("exit = %d, want 3", got)
	}
	if page := planPage(d, term.Style{}); !strings.Contains(page, "vps-03 cannot run") || !strings.HasSuffix(page, "\nnothing planned until the refusals above are fixed\n") {
		t.Errorf("page:\n%s", page)
	}
}

// TestPlanStoreUnreachable: without PostgreSQL's limits the budget cannot be checked: plan
// stops there, exit 5.
func TestPlanStoreUnreachable(t *testing.T) {
	env, sized := sizedLab(t, labHellos, limitsOf(sizing.DBLimits{}, errors.New("cannot reach 10.80.0.20:5432 on vps-01")))
	d, err := planEnv(t.Context(), env, sized, labSecrets, (&fakeFacts{answer: bare}).facts)
	if err != nil {
		t.Fatal(err)
	}
	if got := term.ExitCode(d.exit()); got != 5 || len(d.Nodes) != 0 {
		t.Errorf("exit = %d, nodes %d", got, len(d.Nodes))
	}
	if page := planPage(d, term.Style{}); !strings.Contains(page, "database unreachable: cannot reach 10.80.0.20:5432") {
		t.Errorf("page:\n%s", page)
	}
}

// TestPlanFactsFails: a node that cannot report its facts fails the plan.
func TestPlanFactsFails(t *testing.T) {
	env, sized := sizedLab(t, labHellos, limitsOf(vpsPG, nil))
	ff := &fakeFacts{err: &term.UserError{Code: "helper.failed", What: "facts on vps-02 failed"}}
	if _, err := planEnv(t.Context(), env, sized, labSecrets, ff.facts); err == nil || !strings.Contains(err.Error(), "facts on vps-02") {
		t.Errorf("err = %v", err)
	}
}

func TestToRefusal(t *testing.T) {
	if r := toRefusal(errors.New("boom")); r != (refusal{Code: "unexpected", What: "boom"}) {
		t.Errorf("refusal = %+v", r)
	}
}

func TestPlanNeedsEnv(t *testing.T) {
	cmd := NewRootCommand(&Options{Getwd: func() (string, error) { return fixtureFleet(t), nil }, Getenv: func(string) string { return "" }})
	cmd.SetArgs([]string{"plan"})
	if got := term.ExitCode(cmd.Execute()); got != 4 {
		t.Errorf("exit = %d, want 4", got)
	}
}

// TestNodePageStopAndRemove: a role the spec dropped stops, and its leftover file, which no
// role owns any more, is removed under shared.
func TestNodePageStopAndRemove(t *testing.T) {
	n := plan.NodePlan{Name: "vps-03",
		Roles: []plan.RoleChange{{Role: "frontend", Action: plan.Keep}, {Role: "worker", Action: plan.Stop}},
		Files: []plan.FileChange{{Path: "/etc/temporal/worker.yaml", Action: plan.Remove}},
	}
	got := nodePage(n, term.Style{})
	want := "\nvps-03\n  shared\n    - /etc/temporal/worker.yaml" + strings.Repeat(" ", 61-len("/etc/temporal/worker.yaml")) + "remove\n  worker\n    - stops worker\n"
	if got != want {
		t.Errorf("page:\n%q\nwant\n%q", got, want)
	}
}

// TestValidatedOn: the header counts the nodes whose files the target Temporal checked.
func TestValidatedOn(t *testing.T) {
	ok := plan.NodePlan{Validated: plan.Validated{OK: true, Release: "1.31.3"}}
	for _, tt := range []struct {
		nodes []plan.NodePlan
		want  string
	}{
		{nil, ""},
		{[]plan.NodePlan{ok, ok}, " · validated with temporal-server 1.31.3 on 2 nodes"},
		{[]plan.NodePlan{ok, {}}, " · validated on 1 of 2 nodes"},
	} {
		if got := validatedOn(tt.nodes); got != tt.want {
			t.Errorf("validatedOn(%d nodes) = %q, want %q", len(tt.nodes), got, tt.want)
		}
	}
}
