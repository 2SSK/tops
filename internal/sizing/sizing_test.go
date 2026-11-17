package sizing

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/2SSK/tops/internal/spec"
	"github.com/2SSK/tops/internal/term"
)

// testConstants are round numbers so every expected value below can be worked out by hand.
// They are not the measured constants, which change with each measurement.
var testConstants = Constants{
	Temporal: "1.31",
	OSBase:   200 * mib,
	Floor:    map[string]int64{"frontend": 100 * mib, "history": 100 * mib, "matching": 100 * mib, "worker": 100 * mib},
	Share:    map[string]float64{"frontend": 0.2, "history": 0.4, "matching": 0.3, "worker": 0.1},
	Burst:    map[string]float64{"frontend": 0.1, "history": 0.1, "matching": 0.1, "worker": 0.1},
	NonHeap:  map[string]float64{"frontend": 0.2, "history": 0.2, "matching": 0.2, "worker": 0.2},
	CPU:      map[string]float64{"frontend": 1, "history": 2, "matching": 1, "worker": 0.5}, // mean 1.125
	Conn:     map[string]float64{"frontend": 0.5, "history": 2, "matching": 1, "worker": 0.5},
	VisConn:  map[string]float64{"frontend": 1, "history": 0.5, "matching": 0.25, "worker": 0.25},
	MaxPool:  20,
}

var allRoles = []string{"frontend", "history", "matching", "worker"}

func ptr(n int) *int { return &n }

// row is one role's result in MiB, for short tables.
type row struct {
	role                  string
	max, high, goMemLimit int64 // MiB
	weight, pool, vis     int
	pinned                string // comma-joined
	formula               string
}

func check(t *testing.T, got NodeSize, want []row) {
	t.Helper()
	if len(got.Roles) != len(want) {
		t.Fatalf("roles = %+v, want %d", got.Roles, len(want))
	}
	for i, w := range want {
		g := got.Roles[i]
		gr := row{g.Role, g.MemoryMax / mib, g.MemoryHigh / mib, g.GoMemLimit / mib, g.CPUWeight, g.DBPool, g.DBVisPool, strings.Join(g.Pinned, ","), g.Formula}
		if gr != w {
			t.Errorf("%s =\n  %+v\nwant\n  %+v", w.role, gr, w)
		}
		for _, b := range []int64{g.MemoryMax, g.MemoryHigh, g.GoMemLimit} {
			if b%mib != 0 {
				t.Errorf("%s: %d is not a whole MiB", w.role, b)
			}
		}
	}
}

// TestSizeAllRoles works through vps-01 by hand: 1300M, 1.5 cores, every role.
//
//	free     = 1300 − 200 reserve − 4 × 100 floors = 700M
//	frontend = 100 + 700 × 0.2 = 240; high = 240 × 0.9 = 216; GOMEMLIMIT = 216 × 0.8 = 172.8 → 172
//	weight   = round(100 × 1 / 1.125) = 89;  pool = ceil(0.5 × 1.5) = 1 → at least 2;  vis = ceil(1.5) = 2
func TestSizeAllRoles(t *testing.T) {
	got, err := Size(testConstants, Node{Name: "vps-01", Memory: 1300 * mib, Cores: 1.5, Roles: allRoles})
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "vps-01" || got.Reserve != 200*mib || got.Free != 700*mib {
		t.Errorf("node = %s reserve %d free %d", got.Name, got.Reserve/mib, got.Free/mib)
	}
	check(t, got, []row{
		{"frontend", 240, 216, 172, 89, 2, 2, "", "floor 100M + free 700M × share 0.20"},
		{"history", 380, 342, 273, 178, 3, 1, "", "floor 100M + free 700M × share 0.40"},
		{"matching", 310, 279, 223, 89, 2, 1, "", "floor 100M + free 700M × share 0.30"},
		{"worker", 170, 153, 122, 44, 2, 1, "", "floor 100M + free 700M × share 0.10"},
	})
	if sum := int64(240 + 380 + 310 + 170); sum+200 != 1300 {
		t.Errorf("roles and reserve use %dM, want the whole node", sum+200)
	}
}

// TestSizeTwoRoles: vps-03 runs frontend and matching, so their shares are renormalised over
// the two (0.2 and 0.3 of 0.5): free = 768 − 200 − 200 = 368M.
func TestSizeTwoRoles(t *testing.T) {
	got, err := Size(testConstants, Node{Name: "vps-03", Memory: 768 * mib, Cores: 1, Roles: []string{"matching", "frontend"}})
	if err != nil {
		t.Fatal(err)
	}
	check(t, got, []row{ // stage order, whatever the spec's order
		{"frontend", 247, 222, 177, 89, 2, 1, "", "floor 100M + free 368M × share 0.40"}, // 247.2 → 222.48 → 177.98
		{"matching", 320, 288, 230, 89, 2, 1, "", "floor 100M + free 368M × share 0.60"}, // 320.8 → 288.72 → 230.98
	})
}

// TestSizePinned: a pinned memoryMax leaves the formula; the rest share what is left.
//
//	free = 1300 − 200 − (100 + 100 + 100 floors) − 512 pin = 288M, over shares 0.2 + 0.4 + 0.1 = 0.7
func TestSizePinned(t *testing.T) {
	pin := map[string]spec.Pin{"matching": {MemoryMax: "512M", CPUWeight: ptr(300), DBPool: ptr(7), DBVisibilityPool: ptr(3)}}
	got, err := Size(testConstants, Node{Name: "vps-01", Memory: 1300 * mib, Cores: 1.5, Roles: allRoles, Pin: pin})
	if err != nil {
		t.Fatal(err)
	}
	check(t, got, []row{
		{"frontend", 182, 164, 131, 89, 2, 2, "", "floor 100M + free 288M × share 0.29"},
		{"history", 264, 238, 190, 178, 3, 1, "", "floor 100M + free 288M × share 0.57"},
		{"matching", 512, 460, 368, 300, 7, 3, "memoryMax,cpuWeight,dbPool,dbVisibilityPool", "pinned 512M"},
		{"worker", 141, 127, 101, 44, 2, 1, "", "floor 100M + free 288M × share 0.14"},
	})
}

// TestSizeEveryMemoryPinned: with no role left to share it, free memory stays unused and
// nothing divides by zero.
func TestSizeEveryMemoryPinned(t *testing.T) {
	pin := map[string]spec.Pin{"frontend": {MemoryMax: "200M"}, "matching": {MemoryMax: "300M"}}
	got, err := Size(testConstants, Node{Name: "vps-03", Memory: 768 * mib, Cores: 1, Roles: []string{"frontend", "matching"}, Pin: pin})
	if err != nil {
		t.Fatal(err)
	}
	if got.Free != 68*mib {
		t.Errorf("free = %dM, want 68M left over", got.Free/mib)
	}
	check(t, got, []row{
		{"frontend", 200, 180, 144, 89, 2, 1, "memoryMax", "pinned 200M"},
		{"matching", 300, 270, 216, 89, 2, 1, "memoryMax", "pinned 300M"},
	})
}

// TestSizePoolsClamp: pools stop at MaxPool on a large node and never drop below their minimum.
func TestSizePoolsClamp(t *testing.T) {
	got, err := Size(testConstants, Node{Name: "big", Memory: 64 << 30, Cores: 32, Roles: []string{"history"}})
	if err != nil {
		t.Fatal(err)
	}
	if h := got.Roles[0]; h.DBPool != 20 || h.DBVisPool != 16 {
		t.Errorf("history pools = %d, %d; want 20 (MaxPool) and ceil(0.5 × 32) = 16", h.DBPool, h.DBVisPool)
	}
}

func TestSizeRefuses(t *testing.T) {
	tests := []struct {
		name  string
		node  Node
		roles string // as the refusal names them, in stage order
		why   string
	}{
		{"floors do not fit", Node{Name: "tiny", Memory: 500 * mib, Cores: 1, Roles: allRoles}, "frontend, history, matching, worker",
			"its roles need 400M (floors and pins) and the OS 200M, but it has 500M"},
		{"a pin does not fit", Node{Name: "vps-03", Memory: 768 * mib, Cores: 1, Roles: []string{"frontend", "matching"},
			Pin: map[string]spec.Pin{"matching": {MemoryMax: "500M"}}}, "frontend, matching",
			"its roles need 600M (floors and pins) and the OS 200M, but it has 768M"},
		{"a pin below the floor", Node{Name: "vps-03", Memory: 768 * mib, Cores: 1, Roles: []string{"frontend", "matching"},
			Pin: map[string]spec.Pin{"matching": {MemoryMax: "64M"}}}, "frontend, matching",
			"pin.matching.memoryMax 64M is below the matching floor 100M"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Size(testConstants, tt.node)
			var ue *term.UserError
			if !errors.As(err, &ue) || ue.Code != "refused.node_too_small" {
				t.Fatalf("err = %v, want refused.node_too_small", err)
			}
			if want := tt.node.Name + " cannot run " + tt.roles; ue.What != want || ue.Why != tt.why {
				t.Errorf("error = %q / %q, want why %q", ue.What, ue.Why, tt.why)
			}
			if term.ExitCode(err) != 3 {
				t.Errorf("exit code = %d, want 3 (refused)", term.ExitCode(err))
			}
		})
	}
}

func TestSizeBadPin(t *testing.T) {
	_, err := Size(testConstants, Node{Name: "vps-01", Memory: 1300 * mib, Roles: allRoles, Pin: map[string]spec.Pin{"history": {MemoryMax: "lots"}}})
	if err == nil || !strings.Contains(err.Error(), "vps-01 pin.history.memoryMax") {
		t.Fatalf("err = %v, want the pin named (validate catches this first)", err)
	}
}

func TestRoleSizeJSON(t *testing.T) {
	got, err := Size(testConstants, Node{Name: "vps-03", Memory: 768 * mib, Cores: 1, Roles: []string{"frontend"}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(got.Roles[0])
	if err != nil {
		t.Fatal(err)
	}
	// 768 − 200 − 100 = 468 free, all frontend's: 568M, high 511.2 → 511M, GOMEMLIMIT 408.96 → 408M
	want := `{"role":"frontend","memoryMaxBytes":595591168,"memoryHighBytes":535822336,"goMemLimitBytes":427819008,` +
		`"cpuWeight":89,"dbPool":2,"dbVisibilityPool":1,"pinned":[],"formula":"floor 100M + free 468M × share 1.00"}`
	if string(b) != want {
		t.Errorf("json =\n%s\nwant\n%s", b, want)
	}
}

func TestBytes(t *testing.T) {
	for b, want := range map[int64]string{512 * mib: "512M", 1023 * mib: "1023M", 1 << 30: "1.0G", 1288 * mib: "1.3G", 0: "0M"} {
		if got := Bytes(b); got != want {
			t.Errorf("Bytes(%d) = %q, want %q", b, got, want)
		}
	}
}

// TestPlaceholderFitsTheLab: until T2.1b measures them, the placeholder constants must at least
// size VPSLAB's nodes (1280M with every role, 768M with frontend and matching) and pass
// headroom for the lab's 512 shards.
func TestPlaceholderFitsTheLab(t *testing.T) {
	for _, minor := range []string{"1.30", "1.31"} {
		c, err := For(minor)
		if err != nil {
			t.Fatal(err)
		}
		if c.Temporal != minor || c.MeasuredAt != "" {
			t.Errorf("For(%s) = %s measured %q, want the placeholder", minor, c.Temporal, c.MeasuredAt)
		}
		var sized []NodeSize
		for _, n := range []Node{
			{Name: "vps-01", Memory: 1280 * mib, Cores: 1.5, Roles: allRoles},
			{Name: "vps-02", Memory: 1280 * mib, Cores: 1.5, Roles: allRoles},
			{Name: "vps-03", Memory: 768 * mib, Cores: 1, Roles: []string{"frontend", "matching"}, Pin: map[string]spec.Pin{"matching": {MemoryMax: "512M"}}},
		} {
			ns, err := Size(c, n)
			if err != nil {
				t.Errorf("Temporal %s, %s: %v", minor, n.Name, err)
			}
			sized = append(sized, ns)
		}
		if h := CheckHeadroom(c, 512, sized); !h.OK {
			t.Errorf("Temporal %s: the lab's 512 shards fail headroom: %+v", minor, h)
		} else {
			t.Logf("Temporal %s headroom: %s predicted of %s", minor, Bytes(h.Predicted), Bytes(h.Limit))
		}
	}
	if _, err := For("1.29"); err == nil {
		t.Error("For(1.29) found constants for an unsupported minor")
	}
}

// TestFloorMiB: rounding down must not lose a MiB to floating-point error. The four shares
// sum to 1.0000000000000002 in binary, so frontend's share is 0.19999999999999996, and
// 100M + 700M × that share is a hair under 240M.
func TestFloorMiB(t *testing.T) {
	shares := 0.0
	for _, s := range []float64{0.2, 0.4, 0.3, 0.1} {
		shares += s
	}
	if got := floorMiB(float64(100*mib) + float64(700*mib)*(0.2/shares)); got != 240*mib {
		t.Errorf("floorMiB(100M + 700M × 0.2/Σshares) = %dM, want 240M", got/mib)
	}
	if got := floorMiB(172.8 * mib); got != 172*mib {
		t.Errorf("floorMiB(172.8M) = %dM, want 172M", got/mib)
	}
}
