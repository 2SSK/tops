package spec

import "testing"

// TestClusterLine: a problem found outside this package (a pin below its floor, in
// internal/sizing) gets the same line Validate would give it.
func TestClusterLine(t *testing.T) {
	c, err := Parse("cluster.yaml", []byte("cluster: x\nnodes:\n  - name: a\n    pin:\n      history:\n        memoryMax: 64M\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Line("nodes[0].pin.history.memoryMax"); got != 6 {
		t.Errorf("Line = %d, want 6", got)
	}
	if got := (&Cluster{}).Line("nodes[0]"); got != 0 {
		t.Errorf("Line of a cluster with no file = %d, want 0", got)
	}
}
