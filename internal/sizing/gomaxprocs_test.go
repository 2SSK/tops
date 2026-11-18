package sizing

import "testing"

// TestGoMaxProcs: Go's own rule for a CPU limit, max(ceil(cores), 2), because Go cannot see
// the node's limit from inside a role's unit (TRD "Sizing", measured in T2.1).
func TestGoMaxProcs(t *testing.T) {
	for _, tt := range []struct {
		cores float64
		want  int
	}{
		{0.5, 2}, {1, 2}, {1.5, 2}, {2, 2}, {2.1, 3}, {3.5, 4}, {12, 12},
	} {
		if got := GoMaxProcs(tt.cores); got != tt.want {
			t.Errorf("GoMaxProcs(%v) = %d, want %d", tt.cores, got, tt.want)
		}
	}
}
