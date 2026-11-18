package render

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/2SSK/tops/internal/fleet"
	"github.com/2SSK/tops/internal/secret"
	"github.com/2SSK/tops/internal/settings"
	"github.com/2SSK/tops/internal/sizing"
	"github.com/2SSK/tops/internal/spec"
)

// fixture is the lab environment of the fleet fixture, sized as VPSLAB's nodes are (2 GiB,
// 1.5 cores) with the constants of its Temporal minor. When the constants are measured
// (T2.1b), these goldens change: read the diff, then -update.
func fixture(t *testing.T) Env {
	t.Helper()
	env, err := fleet.Load("../fleet/testdata/fleet", "lab")
	if err != nil {
		t.Fatal(err)
	}
	cat, err := env.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	layers, problems, err := env.Settings(cat)
	if err != nil || len(problems) > 0 {
		t.Fatal(err, problems)
	}
	consts, err := sizing.For("1.31")
	if err != nil {
		t.Fatal(err)
	}
	e := Env{
		Name: env.Name, Cluster: env.Cluster, Dynamic: map[string]settings.File{},
		Secrets: map[string]secret.Value{"TOPS_PG_PASSWORD": secret.New("lab-password-1")},
	}
	for _, role := range spec.Roles {
		e.Dynamic[role] = layers.Merge(role, cat)
	}
	for _, n := range env.Cluster.Nodes {
		size, err := sizing.Size(consts, sizing.Node{Name: n.Name, Memory: 2 << 30, Cores: 1.5, Roles: n.Roles, Pin: n.Pin})
		if err != nil {
			t.Fatal(err)
		}
		e.Sizes = append(e.Sizes, size)
	}
	return e
}

// TestEnvGolden: every file of every node of the fixture, one golden per node, each file
// under a line naming how tops installs it. This is what plan will show for a bare node.
func TestEnvGolden(t *testing.T) {
	for _, in := range fixture(t).Inputs() {
		t.Run(in.Node, func(t *testing.T) {
			var b strings.Builder
			for _, f := range mustRender(t, in) {
				fmt.Fprintf(&b, "==> %s (%04o %s:%s, role %q, %s) <==\n%s\n", f.Path, f.Mode, f.Owner, f.Group, f.Role, f.Effect, f.Content)
			}
			checkGolden(t, "lab-"+in.Node+".txt", []byte(b.String()))
		})
	}
}

// TestInputsFromSpec: each node's input comes from its own spec entry and sizes; the dynamic
// config is the merge of the role, not of the node.
func TestInputsFromSpec(t *testing.T) {
	ins := fixture(t).Inputs()
	if len(ins) != 3 {
		t.Fatalf("%d inputs, want one per node", len(ins))
	}
	vps3 := ins[2]
	got := vps3
	got.Roles, got.Secrets = nil, nil
	want := NodeInput{
		Env: "lab", Cluster: "tops-e2e", Node: "vps-03", Address: "10.80.0.13", Shards: 512, LogLevel: "info",
		Postgres: spec.Postgres{Host: "10.80.0.20", Port: 5432, User: "temporal", AdminDatabase: "postgres"},
		Frontend: "127.0.0.1:7233", GoMaxProcs: 2,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("vps-03 input:\n got %+v\nwant %+v", got, want)
	}
	if vps3.Secrets["TOPS_PG_PASSWORD"].Reveal() != "lab-password-1" {
		t.Error("vps-03 has not the environment's secrets")
	}
}

// TestInputsRoles: a node's roles come from its sizes, in stage order; each role's dynamic
// config is the merge for that role.
func TestInputsRoles(t *testing.T) {
	vps3 := fixture(t).Inputs()[2]
	if len(vps3.Roles) != 2 || vps3.Roles[0].Role != "frontend" || vps3.Roles[1].Role != "matching" {
		t.Fatalf("vps-03 roles: %+v", vps3.Roles)
	}
	if vps3.Roles[0].Size.MemoryMax == 0 || vps3.Roles[0].Size.Role != "frontend" {
		t.Errorf("frontend size on vps-03: %+v", vps3.Roles[0].Size)
	}
	if _, ok := vps3.Roles[0].Dynamic["frontend.namespaceRPS"]; !ok {
		t.Errorf("frontend's dynamic config: %v", vps3.Roles[0].Dynamic)
	}
	if _, ok := vps3.Roles[1].Dynamic["frontend.namespaceRPS"]; ok {
		t.Error("matching got a key only frontend reads")
	}
}

// TestInputsFrontendElsewhere: a node without a frontend reaches the first node that runs one.
func TestInputsFrontendElsewhere(t *testing.T) {
	e := fixture(t)
	e.Cluster.Nodes[0].Roles = []string{"history"}
	e.Cluster.Nodes[2].Roles = []string{"matching"}
	ins := e.Inputs()
	if ins[0].Frontend != "10.80.0.12:7233" || ins[2].Frontend != "10.80.0.12:7233" || ins[1].Frontend != "127.0.0.1:7233" {
		t.Errorf("frontends: %s, %s, %s", ins[0].Frontend, ins[1].Frontend, ins[2].Frontend)
	}
	for i := range e.Cluster.Nodes {
		e.Cluster.Nodes[i].Roles = []string{"history"}
	}
	if got := e.Inputs()[0].Frontend; got != "" {
		t.Errorf("no frontend anywhere: %q", got)
	}
}

// TestWorkerFindsFrontendByMembership settles TRD open point 6: in Temporal 1.31.3 a role
// without publicClient.hostPort resolves frontend through membership, and reads frontend's
// httpPort from its own services block (common/resource/fx.go getFrontendConnectionDetails).
// So no file sets publicClient, and every role's file lists frontend's ports.
func TestWorkerFindsFrontendByMembership(t *testing.T) {
	in := fixture(t).Inputs()[0]
	files := mustRender(t, in)
	for _, f := range files {
		if strings.Contains(string(f.Content), "publicClient") {
			t.Errorf("%s sets publicClient", f.Path)
		}
	}
	worker := string(file(t, files, "/etc/temporal/worker.yaml").Content)
	if !strings.Contains(worker, "  frontend: { rpc: { grpcPort: 7233, membershipPort: 6933, httpPort: 7243, bindOnIP: 0.0.0.0 } }") {
		t.Errorf("worker.yaml does not list frontend's ports:\n%s", worker)
	}
}
