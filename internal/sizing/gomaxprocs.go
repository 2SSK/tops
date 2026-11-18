package sizing

import "math"

// GoMaxProcs is GOMAXPROCS for every role on a node with this many effective cores: Go's own
// rule for a CPU limit, max(ceil(cores), 2). Go reads cpu.max only from the process's own
// cgroup, and a role's unit has none, so a limit on the node's container goes unseen.
func GoMaxProcs(cores float64) int {
	return max(int(math.Ceil(cores)), 2)
}
