package sizing

import (
	"errors"
	"testing"

	"github.com/2SSK/tops/internal/term"
)

// historyOn is a sized node whose history role has the given GOMEMLIMIT, plus a frontend that
// headroom must ignore.
func historyOn(name string, limitMiB int64) NodeSize {
	return NodeSize{Name: name, Roles: []RoleSize{
		{Role: "frontend", GoMemLimit: 1 * mib},
		{Role: "history", GoMemLimit: limitMiB * mib},
	}}
}

// headroomConstants: history's floor is 100M and each shard costs 512K, so a survivor owning
// 256 shards is predicted at 100 + 128 = 228M, and one owning 512 at 100 + 256 = 356M.
var headroomConstants = func() Constants {
	c := testConstants
	c.PerShard = 512 << 10
	return c
}()

func TestHeadroom(t *testing.T) {
	tests := []struct {
		name   string
		shards int
		nodes  []NodeSize
		want   Headroom
	}{
		{"three history nodes: survivors take 256 each", 512,
			[]NodeSize{historyOn("vps-01", 273), historyOn("vps-02", 300), historyOn("vps-04", 600)},
			Headroom{HistoryNodes: 3, Down: "vps-02", WorstSurvivor: "vps-01", Shards: 256, Predicted: 228 * mib, Limit: 273 * mib, OK: true}},
		{"the smallest is not first", 512,
			[]NodeSize{historyOn("vps-01", 600), historyOn("vps-02", 250), {Name: "vps-03", Roles: []RoleSize{{Role: "matching"}}}, historyOn("vps-04", 300)},
			Headroom{HistoryNodes: 3, Down: "vps-01", WorstSurvivor: "vps-02", Shards: 256, Predicted: 228 * mib, Limit: 250 * mib, OK: true}},
		{"shards that do not divide round up", 513,
			[]NodeSize{historyOn("vps-01", 273), historyOn("vps-02", 300), historyOn("vps-04", 600)},
			Headroom{HistoryNodes: 3, Down: "vps-02", WorstSurvivor: "vps-01", Shards: 257, Predicted: 228*mib + 512<<10, Limit: 273 * mib, OK: true}},
		{"two history nodes: the survivor takes all 512", 512,
			[]NodeSize{historyOn("vps-01", 273), historyOn("vps-02", 273)},
			Headroom{HistoryNodes: 2, Down: "vps-02", WorstSurvivor: "vps-01", Shards: 512, Predicted: 356 * mib, Limit: 273 * mib, OK: false}},
		{"predicted exactly at the limit fits", 512,
			[]NodeSize{historyOn("vps-01", 356), historyOn("vps-02", 400)},
			Headroom{HistoryNodes: 2, Down: "vps-02", WorstSurvivor: "vps-01", Shards: 512, Predicted: 356 * mib, Limit: 356 * mib, OK: true}},
		{"one history node: nothing to fail over to", 512,
			[]NodeSize{historyOn("vps-01", 273), {Name: "vps-03", Roles: []RoleSize{{Role: "frontend"}}}},
			Headroom{HistoryNodes: 1, OK: true, Skipped: "one history node: no survivor to take its shards"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CheckHeadroom(headroomConstants, tt.shards, tt.nodes)
			if got != tt.want {
				t.Errorf("CheckHeadroom =\n  %+v\nwant\n  %+v", got, tt.want)
			}
			if err := got.Refusal(); (err == nil) != got.OK {
				t.Errorf("Refusal() = %v with OK %v", err, got.OK)
			}
		})
	}
}

func TestHeadroomRefusal(t *testing.T) {
	h := CheckHeadroom(headroomConstants, 512, []NodeSize{historyOn("vps-01", 273), historyOn("vps-02", 273)})
	var ue *term.UserError
	if !errors.As(h.Refusal(), &ue) {
		t.Fatalf("Refusal() = %v, want a refusal", h.Refusal())
	}
	want := term.UserError{
		Code: "refused.headroom",
		What: "not applying",
		Why:  "with vps-02 down, vps-01 history would own 512 shards: predicted 356M, GOMEMLIMIT 273M",
		Fix:  "more history nodes or more memory on vps-01",
	}
	if *ue != want {
		t.Errorf("refusal =\n  %+v\nwant\n  %+v", *ue, want)
	}
	if term.ExitCode(ue) != 3 {
		t.Errorf("exit code = %d, want 3", term.ExitCode(ue))
	}
}

// TestHeadroomOnSizedNodes runs headroom on Size's own output: the lab's two all-role nodes
// (history GOMEMLIMIT 273M each at 1300M) cannot each take all 512 shards.
func TestHeadroomOnSizedNodes(t *testing.T) {
	var sized []NodeSize
	for _, name := range []string{"vps-01", "vps-02"} {
		ns, err := Size(headroomConstants, Node{Name: name, Memory: 1300 * mib, Cores: 1.5, Roles: allRoles})
		if err != nil {
			t.Fatal(err)
		}
		sized = append(sized, ns)
	}
	if h := CheckHeadroom(headroomConstants, 512, sized); h.OK || h.Limit != 273*mib || h.Predicted != 356*mib {
		t.Errorf("headroom = %+v, want 356M predicted against 273M: refused", h)
	}
	if h := CheckHeadroom(headroomConstants, 128, sized); !h.OK {
		t.Errorf("with 128 shards (100 + 64 = 164M) headroom = %+v, want it to fit", h)
	}
}
