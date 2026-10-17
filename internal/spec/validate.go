package spec

import (
	"fmt"
	"net/netip"
	"regexp"
)

// Problem is one thing wrong with  a spec, named by its path in cluster.yaml
type Problem struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

func (p Problem) String() string { return p.Path + ": " + p.Message }

var (
	clusterName = regexp.MustCompile(`^[a-z][a-z0-9-]{1,30}$`)
	nodeName    = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
)

// Validate checks a spec after Defaults and returns every problem, not only the first,
// so the operator can fix them all in one pass.
func Validate(c *Cluster) []Problem {
	var ps problems
	validateCluster(&ps, c)
	validateNodes(&ps, c.Nodes)
	return ps
}

// problems collects Problems in the order they are found.
type problems []Problem

func (ps *problems) addf(path, format string, args ...any) {
	*ps = append(*ps, Problem{Path: path, Message: fmt.Sprintf(format, args...)})
}

func validateCluster(ps *problems, c *Cluster) {
	if !clusterName.MatchString(c.Cluster) {
		ps.addf("cluster", "%q must match %s", c.Cluster, clusterName)
	}
	if c.Shards < 1 || c.Shards > 16384 {
		ps.addf("shards", "%d must be between 1 and 16384", c.Shards)
	}
}

func validateNodes(ps *problems, nodes []Node) {
	if len(nodes) == 0 {
		ps.addf("nodes", "at least one node is required")
	}

	names := map[string]int{}
	addresses := map[string]int{}
	for i, n := range nodes {
		path := fmt.Sprintf("nodes[%d]", i)

		if !nodeName.MatchString(n.Name) {
			ps.addf(path+".name", "%q must match %s", n.Name, nodeName)
		} else if j, seen := names[n.Name]; seen {
			ps.addf(path+".name", "%s is also nodes[%d]", n.Name, j)
		}

		names[n.Name] = i

		if addr, err := netip.ParseAddr(n.Address); err != nil || !addr.Is4() {
			ps.addf(path+".address", "%q is not an IPv4 address", n.Address)
		} else if j, seen := addresses[n.Address]; seen {
			ps.addf(path+".address", "%s is also nodes[%d]", n.Address, j)
		}

		addresses[n.Address] = i
	}
}
