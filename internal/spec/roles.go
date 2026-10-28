package spec

import (
	"fmt"
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
