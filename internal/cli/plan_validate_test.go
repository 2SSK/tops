package cli

import (
	"slices"
	"strings"
	"testing"

	"github.com/2SSK/tops/internal/term"
	"github.com/2SSK/tops/internal/wire"
)

// TestPlanValidates: on nodes with the spec's release staged, plan has the helper validate
// each role's files with the environment systemd gives the role; all accepted, every node
// is validated and the header says so.
func TestPlanValidates(t *testing.T) {
	env, sized := sizedLab(t, labHellos, limitsOf(vpsPG, nil))
	ff := &fakeNodes{answer: applied(env)}
	d, err := planEnv(t.Context(), env, sized, labSecrets, ff)
	if err != nil {
		t.Fatal(err)
	}
	for i, n := range d.Nodes {
		v := ff.validated[i]
		var roles []string
		for _, r := range v.Roles {
			roles = append(roles, r.Role)
			checkRoleFiles(t, n.Name, r)
		}
		if v.Version != "1.31.3" || !slices.Equal(roles, env.Cluster.Nodes[i].Roles) || !n.Validated.OK {
			t.Errorf("%s: validated %s with roles %v, ok %v", n.Name, v.Version, roles, n.Validated.OK)
		}
	}
	if page := planPage(d, term.Style{Unicode: true}); !strings.HasPrefix(page, "lab (tops-e2e)  plan for 3 nodes · validated with temporal-server 1.31.3 on 3 nodes\n") {
		t.Errorf("page:\n%s", page)
	}
	if err := d.exit(); err != nil {
		t.Errorf("exit = %v", err)
	}
}

// checkRoleFiles: a role is validated with its rendered config and the environment systemd
// gives it, temporal.env's secret first, its value masked by key.
func checkRoleFiles(t *testing.T, node string, r wire.RoleFiles) {
	t.Helper()
	if r.Env[0] != "TOPS_PG_PASSWORD="+labPassword || !strings.HasPrefix(r.Env[1], "GOMEMLIMIT=") || !slices.Equal(r.Secrets, []string{"TOPS_PG_PASSWORD"}) {
		t.Errorf("%s %s: env %q, secrets %q", node, r.Role, r.Env, r.Secrets)
	}
	if !strings.Contains(r.Config, "# enable-template") {
		t.Errorf("%s %s: config is not the rendered %s.yaml", node, r.Role, r.Role)
	}
}

// TestPlanNotStagedNotValidated: without the release on a node there is nothing to validate
// with; the plan says so and does not fail.
func TestPlanNotStagedNotValidated(t *testing.T) {
	env, sized := sizedLab(t, labHellos, limitsOf(vpsPG, nil))
	ff := &fakeNodes{answer: bare}
	d, err := planEnv(t.Context(), env, sized, labSecrets, ff)
	if err != nil {
		t.Fatal(err)
	}
	if len(ff.validated) != 0 || len(d.Refusals) != 0 || d.Nodes[0].Validated.OK {
		t.Errorf("validated %d nodes, refusals %+v", len(ff.validated), d.Refusals)
	}
}

// TestPlanRenderRejected: a file the target release refuses is refused.render_rejected, in
// node order, naming the file, the release and the node, with Temporal's message. The plan
// is still printed; exit 3.
func TestPlanRenderRejected(t *testing.T) {
	env, sized := sizedLab(t, labHellos, limitsOf(vpsPG, nil))
	ff := &fakeNodes{answer: applied(env), verdict: func(i int, _ wire.Validate) wire.ValidateResult {
		switch i {
		case 0:
			return wire.ValidateResult{Rejected: []wire.Rejection{{Role: "history", File: "config", Message: "Unable to load configuration: bad numHistoryShards"}}}
		case 1:
			return wire.ValidateResult{Rejected: []wire.Rejection{{Role: "frontend", File: "dynamicConfig", Message: "error: unknown key frontend.x"}}}
		}
		return wire.ValidateResult{Rejected: []wire.Rejection{}}
	}}
	d, err := planEnv(t.Context(), env, sized, labSecrets, ff)
	if err != nil {
		t.Fatal(err)
	}
	want := []refusal{
		{Code: "refused.render_rejected", What: "history.yaml rejected by temporal-server 1.31.3 on vps-01", Why: "Unable to load configuration: bad numHistoryShards", Fix: "nothing changed; fix the value named above"},
		{Code: "refused.render_rejected", What: "dynamicconfig-frontend.yaml rejected by temporal-server 1.31.3 on vps-02", Why: "error: unknown key frontend.x", Fix: "nothing changed; fix the value named above"},
	}
	if !slices.Equal(d.Refusals, want) {
		t.Errorf("refusals =\n%+v\nwant\n%+v", d.Refusals, want)
	}
	if ok := []bool{d.Nodes[0].Validated.OK, d.Nodes[1].Validated.OK, d.Nodes[2].Validated.OK}; !slices.Equal(ok, []bool{false, false, true}) {
		t.Errorf("validated ok = %v", ok)
	}
	if got := term.ExitCode(d.exit()); got != 3 {
		t.Errorf("exit = %d, want 3", got)
	}
	contains(t, planPage(d, term.Style{}),
		" · validated on 1 of 3 nodes\n",
		"history.yaml rejected by temporal-server 1.31.3 on vps-01: Unable to load configuration: bad numHistoryShards\n  nothing changed; fix the value named above\n",
		"\nvps-03  no changes\n",
		"\napply refuses until the refusals above are fixed\n",
	)
}

// TestPlanValidateFails: a node that cannot run validate fails the plan, as facts does.
func TestPlanValidateFails(t *testing.T) {
	env, sized := sizedLab(t, labHellos, limitsOf(vpsPG, nil))
	ff := &fakeNodes{answer: applied(env), vErr: &term.UserError{Code: "helper.failed", What: "validate on vps-02 failed"}}
	if _, err := planEnv(t.Context(), env, sized, labSecrets, ff); err == nil || !strings.Contains(err.Error(), "validate on vps-02") {
		t.Errorf("err = %v", err)
	}
}
