package spec

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// Roles are the Temporal services a node can run, in stage order: frontends start first,
// because every later gate reads the cluster through them.
var Roles = []string{"frontend", "history", "matching", "worker"}

var roleList = strings.Join(Roles, ", ")

// validateRoles checks one node's roles list; path is "nodes[i].roles".
func validateRoles(ps *problems, path string, roles []string) {
	if len(roles) == 0 {
		ps.addf(path, "at least one role is required (%s)", roleList)
		return
	}

	seen := map[string]int{}
	for i, r := range roles {
		rpath := fmt.Sprintf("%s[%d]", path, i)
		if !slices.Contains(Roles, r) {
			ps.addf(rpath, "%q is not a role (%s)", r, roleList)
		} else if j, dup := seen[r]; dup {
			ps.addf(rpath, "%s is also %s[%d]", r, path, j)
		} else {
			seen[r] = i
		}
	}
}

// validateRoleCoverage checks that every role runs on at least one node.
// With no nodes at all, validateNodes has already said no.
// roleList is Roles as printed in problems: "frontend, history, matching, worker".
func validateRoleCoverage(ps *problems, nodes []Node) {
	if len(nodes) == 0 {
		return
	}
	for _, r := range Roles {
		runs := func(n Node) bool { return slices.Contains(n.Roles, r) }
		if !slices.ContainsFunc(nodes, runs) {
			ps.addf("nodes", "no node runs %s", r)
		}
	}
}

// validatePin checks one node's pins; path is "nodes[i].pin". Whether a pinned memoryMax
// still fits the role's floor needs the sizing constants, so internal/sizing checks that.
func validatePin(ps *problems, path string, n Node) {
	for _, role := range slices.Sorted(maps.Keys(n.Pin)) {
		rpath := path + "." + role
		if !slices.Contains(n.Roles, role) {
			ps.addf(rpath, "%s is not one of this node's roles (%s)", role, strings.Join(n.Roles, ", "))
			continue
		}
		checkPin(ps, rpath, n.Pin[role])
	}
}

// checkPin checks one role's pinned values; path is "nodes[i].pin.<role>".
func checkPin(ps *problems, path string, p Pin) {
	if p.MemoryMax != "" {
		if _, err := ParseSize(p.MemoryMax); err != nil {
			ps.addf(path+".memoryMax", "%v", err)
		}
	}
	if w := p.CPUWeight; w != nil && (*w < 1 || *w > 10000) {
		ps.addf(path+".cpuWeight", "%d must be between 1 and 10000", *w)
	}
	atLeastOne(ps, path+".dbPool", p.DBPool)
	atLeastOne(ps, path+".dbVisibilityPool", p.DBVisibilityPool)
}

// atLeastOne checks a pinned pool size, when it is set.
func atLeastOne(ps *problems, path string, n *int) {
	if n != nil && *n < 1 {
		ps.addf(path, "%d must be at least 1", *n)
	}
}
