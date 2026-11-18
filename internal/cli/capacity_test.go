package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/2SSK/tops/internal/fleet"
	"github.com/2SSK/tops/internal/sizing"
	"github.com/2SSK/tops/internal/spec"
	"github.com/2SSK/tops/internal/term"
	"github.com/2SSK/tops/internal/wire"
)

const mib = 1 << 20

// labCapacity is a capacity result shaped like VPSLAB's: two all-role nodes and vps-03 with a
// pinned matching.
func labCapacity() *capacityDocument {
	role := func(r string, maxM, highM, goM int64, w, pool, vis int, pinned ...string) sizing.RoleSize {
		return sizing.RoleSize{Role: r, MemoryMax: maxM * mib, MemoryHigh: highM * mib, GoMemLimit: goM * mib,
			CPUWeight: w, DBPool: pool, DBVisPool: vis, Pinned: append([]string{}, pinned...)}
	}
	node := func(name string, coresN float64, memM int64, roles ...sizing.RoleSize) nodeCapacity {
		return nodeCapacity{
			Name:    name,
			Memory:  wire.Detected{Value: float64(memM * mib), Source: "cgroup:/sys/fs/cgroup/memory.max"},
			Cores:   wire.Detected{Value: coresN, Source: "cgroup:/sys/fs/cgroup/cpu.max"},
			Reserve: 160 * mib, Roles: roles,
		}
	}
	all := []sizing.RoleSize{role("frontend", 233, 210, 157, 90, 2, 3), role("history", 473, 426, 319, 180, 6, 2),
		role("matching", 272, 244, 183, 108, 3, 1), role("worker", 140, 126, 95, 22, 2, 1)}
	return &capacityDocument{
		Kind: "Capacity", TopsVersion: "dev", Env: "lab", Cluster: "tops-e2e",
		Constants: capacityConstants{Temporal: "1.31"},
		Nodes: []nodeCapacity{
			node("vps-01", 1.5, 1280, all...), node("vps-02", 1.5, 1280, all...),
			node("vps-03", 1, 768, role("frontend", 279, 251, 188, 90, 2, 2), role("matching", 512, 460, 345, 108, 2, 1, "memoryMax")),
		},
		Headroom: sizing.Headroom{HistoryNodes: 2, Down: "vps-02", WorstSurvivor: "vps-01", Shards: 512, Predicted: 256 * mib, Limit: 319 * mib, OK: true},
		DB:       dbCapacity{DBBudget: sizing.CheckDB(sizing.DBLimits{Host: "10.80.0.20", MaxConnections: 300, SuperuserReserved: 3}, nil), Host: "10.80.0.20"},
	}
}

func TestCapacityPage(t *testing.T) {
	d := labCapacity()
	d.DB.Pools = 41
	got := capacityPage(d, term.Style{Unicode: true})
	want := `lab (tops-e2e)  3 nodes · constants not measured yet: placeholders (Temporal 1.31)

vps-01  1.5 cores (cgroup cpu.max) · 1.2G (cgroup memory.max) · reserve 160M
  role       MemoryMax  MemoryHigh  GOMEMLIMIT  CPUWeight  pool  vis
  frontend   233M       210M        157M        90         2     3
  history    473M       426M        319M        180        6     2
  matching   272M       244M        183M        108        3     1
  worker     140M       126M        95M         22         2     1

vps-02  1.5 cores (cgroup cpu.max) · 1.2G (cgroup memory.max) · reserve 160M
  role       MemoryMax  MemoryHigh  GOMEMLIMIT  CPUWeight  pool  vis
  frontend   233M       210M        157M        90         2     3
  history    473M       426M        319M        180        6     2
  matching   272M       244M        183M        108        3     1
  worker     140M       126M        95M         22         2     1

vps-03  1 core (cgroup cpu.max) · 768M (cgroup memory.max) · reserve 160M
  role       MemoryMax  MemoryHigh  GOMEMLIMIT  CPUWeight  pool  vis
  frontend   279M       251M        188M        90         2     2
  matching   512M       460M        345M        108        2     1  pin memoryMax

cluster
✓ headroom   vps-02 down: vps-01 history owns 512 shards, predicted 256M of 319M GOMEMLIMIT
✓ database   pools 41 of 295 available (10.80.0.20 max_connections 300 · reserved 3 · other sessions 0)
`
	if got != want {
		t.Errorf("page =\n%s\nwant\n%s", got, want)
	}
}

func TestCapacityPageProblems(t *testing.T) {
	d := labCapacity()
	d.Constants.MeasuredAt = "2026-11-20"
	d.Nodes[2] = nodeCapacity{Name: "vps-03", Memory: wire.Detected{Value: 512 * mib, Source: "meminfo"},
		Cores: wire.Detected{Value: 2, Source: "affinity"}, Refused: "its roles need 440M (floors and pins) and the OS 160M, but it has 512M"}
	d.Headroom = sizing.Headroom{HistoryNodes: 2, Down: "vps-02", WorstSurvivor: "vps-01", Shards: 512, Predicted: 400 * mib, Limit: 319 * mib}
	d.DB = dbCapacity{Host: "10.80.0.20", Error: "connect to 10.80.0.20:5432 as temporal: password authentication failed"}
	got := capacityPage(d, term.Style{}) // ASCII: no UTF-8 locale
	for _, want := range []string{
		"lab (tops-e2e)  3 nodes · constants measured 2026-11-20 on VPSLAB (Temporal 1.31)\n",
		"\nvps-03  2 cores (affinity) · 512M (meminfo)\nFAIL its roles need 440M (floors and pins) and the OS 160M, but it has 512M\n",
		"FAIL headroom   vps-02 down: vps-01 history owns 512 shards, predicted 400M of 319M GOMEMLIMIT\n",
		"FAIL database   unreachable: connect to 10.80.0.20:5432 as temporal: password authentication failed\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("page has no\n%q\n\n%s", want, got)
		}
	}
}

func TestHeadroomLine(t *testing.T) {
	st := term.Style{Unicode: true}
	if got := headroomLine(sizing.Headroom{HistoryNodes: 1, OK: true, Skipped: "one history node: no survivor to take its shards"}, st); got != "! headroom   one history node: no survivor to take its shards\n" {
		t.Errorf("one history node: %q", got)
	}
	if got := headroomLine(sizing.Headroom{}, st); got != "· headroom   no history node could be sized\n" {
		t.Errorf("nothing sized: %q", got)
	}
}

func TestCapacityExit(t *testing.T) {
	tooSmall := &term.UserError{Code: "refused.node_too_small", What: "vps-03 cannot run frontend"}
	headroom := &term.UserError{Code: "refused.headroom", What: "not applying"}
	tests := []struct {
		name     string
		refusals []error
		dbError  string
		code     int
		want     string
	}{
		{"fits", nil, "", 0, ""},
		{"a refusal", []error{tooSmall, headroom}, "", 3, "refused.node_too_small"},
		{"a refusal beats an unreadable store", []error{headroom}, "timeout", 3, "refused.headroom"},
		{"an unreadable store", nil, "timeout", 5, "unhealthy.unreachable"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := labCapacity()
			d.refusals, d.DB.Error = tt.refusals, tt.dbError
			err := d.exit()
			if got := term.ExitCode(err); got != tt.code {
				t.Errorf("exit code = %d, want %d", got, tt.code)
			}
			if err == nil {
				return
			}
			var reported *term.Reported
			if !errors.As(err, &reported) {
				t.Errorf("err = %v, want it Reported: the page already showed it", err)
			}
			var ue *term.UserError
			if !errors.As(err, &ue) || ue.Code != tt.want {
				t.Errorf("err = %v, want code %s", err, tt.want)
			}
		})
	}
}

func TestCapacityJSON(t *testing.T) {
	d := labCapacity()
	d.Nodes[2].Refused = "too small"
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	hasKeys(t, "document", got, "kind", "topsVersion", "env", "cluster", "constants", "nodes", "headroom", "db")
	// The budget's fields are inline in db, next to host.
	hasKeys(t, "db", got["db"].(map[string]any), "maxConnections", "reserved", "otherSessions", "budget", "pools", "ok", "host")
	if _, ok := got["refusals"]; ok {
		t.Error("refusals leaked into the JSON")
	}
	node := got["nodes"].([]any)[0].(map[string]any)
	if node["memory"].(map[string]any)["source"] != "cgroup:/sys/fs/cgroup/memory.max" || node["reserveBytes"] != float64(160*mib) {
		t.Errorf("node = %v", node)
	}
	if got["nodes"].([]any)[2].(map[string]any)["refused"] != "too small" {
		t.Error("a refused node does not say why")
	}
}

func hasKeys(t *testing.T, what string, m map[string]any, keys ...string) {
	t.Helper()
	for _, k := range keys {
		if _, ok := m[k]; !ok {
			t.Errorf("%s has no %q: %v", what, k, m)
		}
	}
}

// labEnv is the fixture's lab: vps-01 and vps-02 with every role, vps-03 with frontend and
// matching, 512 shards.
func labEnv(t *testing.T) *fleet.Env {
	t.Helper()
	env, err := fleet.Load(fixtureFleet(t), "lab")
	if err != nil {
		t.Fatal(err)
	}
	return env
}

func hello(memM int64, cores float64) wire.Hello {
	return wire.Hello{
		Memory: wire.Detected{Value: float64(memM * mib), Source: "cgroup:/sys/fs/cgroup/memory.max"},
		Cores:  wire.Detected{Value: cores, Source: "cgroup:/sys/fs/cgroup/cpu.max"},
	}
}

var labHellos = []wire.Hello{hello(1280, 1.5), hello(1280, 1.5), hello(768, 1)}

func limitsOf(l sizing.DBLimits, err error) func() (sizing.DBLimits, error) {
	return func() (sizing.DBLimits, error) { return l, err }
}

var vpsPG = sizing.DBLimits{Host: "10.80.0.20", MaxConnections: 300, SuperuserReserved: 3}

// The sizeEnv tests run the whole capacity check on the lab's shape. They check the structure,
// not the numbers: those belong to internal/sizing and change when the constants are measured.

func placeholderConstants(t *testing.T) sizing.Constants {
	t.Helper()
	c, err := sizing.For("1.31")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// TestSizeEnvFits: the lab fits.
func TestSizeEnvFits(t *testing.T) {
	consts := placeholderConstants(t)
	d, err := sizeEnv(labEnv(t), consts, labHellos, limitsOf(vpsPG, nil))
	if err != nil {
		t.Fatal(err)
	}
	type shape struct {
		env, cluster      string
		nodes, vps03Roles int
		historyNodes      int
		headroomOK, dbOK  bool
		host              string
		poolsCounted      bool
	}
	got := shape{d.Env, d.Cluster, len(d.Nodes), len(d.Nodes[2].Roles), d.Headroom.HistoryNodes, d.Headroom.OK, d.DB.OK, d.DB.Host, d.DB.Pools > 0}
	if want := (shape{"lab", "tops-e2e", 3, 2, 2, true, true, "10.80.0.20", true}); got != want {
		t.Errorf("capacity = %+v, want %+v", got, want)
	}
	if err := d.exit(); err != nil {
		t.Errorf("exit = %v", err)
	}
}

// TestSizeEnvNodeTooSmall: a node too small is shown, the rest sized.
func TestSizeEnvNodeTooSmall(t *testing.T) {
	consts := placeholderConstants(t)
	hellos := []wire.Hello{hello(1280, 1.5), hello(1280, 1.5), hello(200, 1)}
	d, err := sizeEnv(labEnv(t), consts, hellos, limitsOf(vpsPG, nil))
	if err != nil {
		t.Fatal(err)
	}
	if d.Nodes[2].Refused == "" || len(d.Nodes[2].Roles) != 0 || len(d.Nodes[0].Roles) != 4 {
		t.Errorf("nodes = %+v", d.Nodes)
	}
	if d.Headroom.HistoryNodes != 2 {
		t.Errorf("headroom left out a sized node: %+v", d.Headroom)
	}
	if got := term.ExitCode(d.exit()); got != 3 {
		t.Errorf("exit code = %d, want 3", got)
	}
}

// TestSizeEnvStoreUnreadable: an unreadable store.
func TestSizeEnvStoreUnreadable(t *testing.T) {
	consts := placeholderConstants(t)
	d, err := sizeEnv(labEnv(t), consts, labHellos, limitsOf(sizing.DBLimits{}, errors.New("cannot reach 10.80.0.20:5432 on vps-01")))
	if err != nil {
		t.Fatal(err)
	}
	if d.DB.Error != "cannot reach 10.80.0.20:5432 on vps-01" || d.DB.Host != "10.80.0.20" {
		t.Errorf("db = %+v", d.DB)
	}
	if got := term.ExitCode(d.exit()); got != 5 {
		t.Errorf("exit code = %d, want 5", got)
	}
}

// TestSizeEnvBudget: pools beyond the budget.
func TestSizeEnvBudget(t *testing.T) {
	consts := placeholderConstants(t)
	d, err := sizeEnv(labEnv(t), consts, labHellos, limitsOf(sizing.DBLimits{Host: "10.80.0.20", MaxConnections: 10, SuperuserReserved: 3}, nil))
	if err != nil {
		t.Fatal(err)
	}
	var ue *term.UserError
	if d.DB.OK || !errors.As(d.exit(), &ue) || ue.Code != "refused.db_budget" {
		t.Errorf("db = %+v, exit %v; want refused.db_budget", d.DB, d.exit())
	}
}

// TestSizeEnvBadPin: a pin validate would refuse.
func TestSizeEnvBadPin(t *testing.T) {
	consts := placeholderConstants(t)
	env := labEnv(t)
	env.Cluster.Nodes[2].Pin = map[string]spec.Pin{"matching": {MemoryMax: "lots"}}
	if _, err := sizeEnv(env, consts, labHellos, limitsOf(vpsPG, nil)); err == nil {
		t.Error("an unparseable pin was sized")
	}
}

func TestSourceAndCores(t *testing.T) {
	for in, want := range map[string]string{
		"cgroup:/sys/fs/cgroup/memory.max":           "cgroup memory.max",
		"cgroup:/sys/fs/cgroup/system.slice/cpu.max": "cgroup cpu.max",
		"meminfo":  "meminfo",
		"affinity": "affinity",
	} {
		if got := source(in); got != want {
			t.Errorf("source(%q) = %q, want %q", in, got, want)
		}
	}
	for n, want := range map[float64]string{1: "1 core", 1.5: "1.5 cores", 2: "2 cores", 0.5: "0.5 cores"} {
		if got := cores(n); got != want {
			t.Errorf("cores(%v) = %q, want %q", n, got, want)
		}
	}
}

// TestCapacityNeedsEnv: like every command that reads a cluster, capacity has no default.
func TestCapacityNeedsEnv(t *testing.T) {
	_, err := run(t, fixtureFleet(t), "capacity")
	var ue *term.UserError
	if !errors.As(err, &ue) || ue.Code != "usage.invalid" {
		t.Fatalf("err = %#v, want usage.invalid", err)
	}
}

// TestCapacityNeedsSecrets: secrets.env is checked before any node is dialled.
func TestCapacityNeedsSecrets(t *testing.T) {
	root := versionEnv(t, 22)
	if err := os.Remove(filepath.Join(root, "environments", "qa", "secrets.env")); err != nil {
		t.Fatal(err)
	}
	_, err := run(t, root, "-e", "qa", "capacity")
	if got := term.ExitCode(err); got != 4 {
		t.Fatalf("err = %v (exit %d), want the missing secrets.env (exit 4)", err, got)
	}
}
