package sizing

import (
	"fmt"
	"maps"
	"slices"

	"github.com/2SSK/tops/internal/spec"
)

// CheckPins reports every pinned memoryMax below its role's floor, offline, for validate. The
// spec's own checks have passed: every pin names one of its node's roles and parses.
func CheckPins(c Constants, cl *spec.Cluster) []spec.Problem {
	var ps []spec.Problem
	for i, n := range cl.Nodes {
		for _, role := range slices.Sorted(maps.Keys(n.Pin)) {
			size := n.Pin[role].MemoryMax
			if size == "" {
				continue
			}
			b, err := spec.ParseSize(size)
			if err != nil || b >= c.Floor[role] {
				continue
			}
			path := fmt.Sprintf("nodes[%d].pin.%s.memoryMax", i, role)
			ps = append(ps, spec.Problem{Path: path, Line: cl.Line(path), Message: fmt.Sprintf("%s is below %s's floor of %s on Temporal %s", size, role, Bytes(c.Floor[role]), c.Temporal)})
		}
	}
	return ps
}
