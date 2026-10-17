package spec

import (
	"fmt"
	"net/netip"
	"regexp"
	"slices"
	"strings"
	"time"
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
	semver      = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
	dnsName     = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*$`)
	pgUser      = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,62}$`)
	nsName      = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,254}$`)
)

var supportedMinors = []string{"1.30", "1.31"}
var logLevels = []string{"debug", "info", "warn", "error"}

// Validate checks a spec after Defaults and returns every problem, not only the first,
// so the operator can fix them all in one pass.
func Validate(c *Cluster) []Problem {
	var ps problems
	validateCluster(&ps, c)
	validateNodes(&ps, c.Nodes)
	validateTemporal(&ps, c.Temporal)
	validatePersistence(&ps, c.Persistence)
	validateSSH(&ps, c.SSH)
	validateAccess(&ps, c.Access)
	validateUI(&ps, c.UI, c.Nodes)
	validateNamespaces(&ps, c.Namespaces)
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

func validateTemporal(ps *problems, t Temporal) {
	switch {
	case !semver.MatchString(t.Version):
		ps.addf("temporal.version", "%q must be a version like 1.31.3", t.Version)
	case !slices.Contains(supportedMinors, minor(t.Version)):
		ps.addf("temporal.version", "%s is not supported; use %s.x", t.Version, strings.Join(supportedMinors, ".x or "))
	}
	if !semver.MatchString(t.CLI) {
		ps.addf("temporal.cli", "%q must be a version like 1.9.1", t.CLI)
	}

	if !slices.Contains(logLevels, t.LogLevel) {
		ps.addf("temporal.logLevel", "%q must be one of %s", t.LogLevel, strings.Join(logLevels, ", "))
	}
}

func checkPort(ps *problems, path string, port int) {
	if port < 1 || port > 65535 {
		ps.addf(path, "%d must be between 1 and 65535", port)
	}
}

func minor(version string) string {
	return version[:strings.LastIndex(version, ".")]
}

func validatePersistence(ps *problems, p Persistence) {
	pg := p.Postgres
	if pg == nil {
		ps.addf("persistence", "postgres is required (v1 supports only postgres)")
		return
	}

	if !isHost(pg.Host) {
		ps.addf("persistence.postgres.host", "%q must be an IPv4 address or a DNS name", pg.Host)
	}

	checkPort(ps, "persistence.postgres.port", pg.Port)

	if !pgUser.MatchString(pg.User) {
		ps.addf("persistence.postgres.user", "%q must match %s", pg.User, pgUser)
	}
}

func validateSSH(ps *problems, s SSH) {
	if s.User == "" {
		ps.addf("ssh.user", "is required")
	}
	checkPort(ps, "ssh.port", s.Port)
}

// isHost reports whether s is an IPv4 address or a DNS name.
func isHost(s string) bool {
	if addr, err := netip.ParseAddr(s); err == nil {
		return addr.Is4()
	}
	return dnsName.MatchString(s)
}

// validateSources checks one firewall source list: IPv4 addresses or CIDRs, no duplicates.
func validateSources(ps *problems, path string, sources []string) {
	seen := map[string]int{}
	for i, s := range sources {
		p := fmt.Sprintf("%s[%d]", path, i)

		if !isSource(s) {
			ps.addf(p, "%q must be an IPv4 address or CIDR", s)
		} else if j, dup := seen[s]; dup {
			ps.addf(p, "%s is also %s[%d]", s, path, j)
		}
		seen[s] = i
	}
}

func isSource(s string) bool {
	if addr, err := netip.ParseAddr(s); err == nil {
		return addr.Is4()
	}
	if prefix, err := netip.ParsePrefix(s); err == nil {
		return prefix.Addr().Is4()
	}
	return false
}

func validateAccess(ps *problems, a Access) {
	validateSources(ps, "access.clients", a.Clients)
	validateSources(ps, "access.monitors", a.Monitors)
	validateSources(ps, "access.admins", a.Admins)
}

func validateUI(ps *problems, ui *UI, nodes []Node) {
	if ui == nil {
		return
	}

	if !semver.MatchString(ui.Version) {
		ps.addf("ui.version", "%q must be a version like 2.54.1", ui.Version)
	}

	if ui.Node != "" && !nodeExists(ui.Node, nodes) {
		ps.addf("ui.node", "%q is not a name in nodes", ui.Node)
	}
}

func nodeExists(name string, nodes []Node) bool {
	for _, n := range nodes {
		if n.Name == name {
			return true
		}
	}
	return false
}

// minRetention is the shortest retention Temporal accepts for a local namespace.
const minRetention = time.Hour

func validateNamespaces(ps *problems, namespaces []Namespace) {
	seen := map[string]int{}
	for i, ns := range namespaces {
		path := fmt.Sprintf("namespaces[%d]", i)
		switch {
		case ns.Name == "":
			ps.addf(path+".name", "is required")
		case ns.Name == "temporal-system":
			ps.addf(path+".name", "temporal-system is Temporal's own namespace and cannot be managed")
		case !nsName.MatchString(ns.Name):
			ps.addf(path+".name", "%q must match %s", ns.Name, nsName)
		default:
			if j, dup := seen[ns.Name]; dup {
				ps.addf(path+".name", "%s is also namespaces[%d]", ns.Name, j)
			}
		}
		seen[ns.Name] = i

		validateRetention(ps, path+".retention", ns.Retention)
	}
}

func validateRetention(ps *problems, path, retention string) {
	d, err := ParseDuration(retention)
	switch {
	case err != nil:
		ps.addf(path, "%q is not a duration like 72h or 7d", retention)
	case d < minRetention:
		ps.addf(path, "%s is below Temporal's minimum of 1h", retention)
	}
}
