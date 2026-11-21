package sizing

import (
	"reflect"
	"testing"

	"github.com/2SSK/tops/internal/spec"
)

// TestCheckPins: a pinned memoryMax below the role's floor is a problem at its own line; one
// at the floor, a pin without memoryMax and a node without pins are not.
func TestCheckPins(t *testing.T) {
	cl, err := spec.Parse("cluster.yaml", []byte(`nodes:
  - name: vps-01
    roles: [history, matching]
    pin:
      matching: { memoryMax: 100M }
      history:
        memoryMax: 64M
  - name: vps-02
    roles: [history, worker]
    pin:
      history: { memoryMax: 100M, cpuWeight: 300 }
      worker: { cpuWeight: 50 }
  - name: vps-03
    roles: [frontend]
`))
	if err != nil {
		t.Fatal(err)
	}
	got := CheckPins(testConstants, cl) // every floor is 100M
	want := []spec.Problem{{Path: "nodes[0].pin.history.memoryMax", Line: 7, Message: "64M is below history's floor of 100M on Temporal 1.31"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("CheckPins =\n%+v\nwant\n%+v", got, want)
	}
}

// TestCheckPinsLeavesBadSizesToSpec: a size that does not parse is spec.Validate's problem,
// already reported; CheckPins does not repeat it.
func TestCheckPinsLeavesBadSizesToSpec(t *testing.T) {
	cl := &spec.Cluster{Nodes: []spec.Node{{Name: "a", Roles: []string{"history"}, Pin: map[string]spec.Pin{"history": {MemoryMax: "lots"}}}}}
	if got := CheckPins(testConstants, cl); len(got) != 0 {
		t.Errorf("CheckPins = %+v, want nothing", got)
	}
}
