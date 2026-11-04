package fleet

import (
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/2SSK/tops/internal/spec"
)

// EnvDiff is what differs between two environments, by group (TRD "Diff and promote").
// Promotable differences are what tops promote would write; by-design ones are each
// environment's own, such as its cluster name and nodes.
type EnvDiff struct {
	Promotable []Difference `json:"promotable"`
	ByDesign   []Difference `json:"byDesign"`
}

// Difference is one path whose value differs. A nil From or To is a path the
// environment does not have: a namespace it does not keep, a key its overrides do not set.
type Difference struct {
	Path string `json:"path"`
	From any    `json:"from"`
	To   any    `json:"to"`
}

// Diff compares the resolved values of from and to: defaults applied, and each
// environment's overrides checked against its own catalog. base/ is shared, so the
// overrides are the only settings that can differ.
func Diff(from, to *Env) (EnvDiff, error) {
	fromValues, err := promotable(from)
	if err != nil {
		return EnvDiff{}, err
	}
	toValues, err := promotable(to)
	if err != nil {
		return EnvDiff{}, err
	}
	return EnvDiff{
		Promotable: compare(fromValues, toValues),
		ByDesign:   compare(byDesign(from.Cluster), byDesign(to.Cluster)),
	}, nil
}

// promotable are the values of env that tops promote copies, by path.
func promotable(env *Env) (map[string]any, error) {
	c := env.Cluster
	values := map[string]any{
		"temporal.version":  c.Temporal.Version,
		"temporal.cli":      c.Temporal.CLI,
		"temporal.logLevel": c.Temporal.LogLevel,
	}
	for _, ns := range c.Namespaces {
		values["namespaces."+ns.Name] = ns.Retention
	}

	cat, err := env.Catalog()
	if err != nil {
		return nil, err
	}
	layers, _, err := env.Settings(cat)
	if err != nil {
		return nil, err
	}
	for key, e := range layers.Overrides {
		values["overrides."+key] = e.Values
	}
	return values, nil
}

// byDesign are the values of c that belong to its environment, by path. Nodes are
// compared by shape: how many, and the roles of each.
func byDesign(c *spec.Cluster) map[string]any {
	values := map[string]any{
		"cluster":             c.Cluster,
		"shards":              strconv.Itoa(c.Shards),
		"ssh.user":            c.SSH.User,
		"ssh.port":            strconv.Itoa(c.SSH.Port),
		"ssh.key":             c.SSH.Key,
		"ssh.knownHosts":      c.SSH.KnownHosts,
		"access.clients":      strings.Join(c.Access.Clients, ","),
		"access.monitors":     strings.Join(c.Access.Monitors, ","),
		"access.admins":       strings.Join(c.Access.Admins, ","),
		"access.healthchecks": strings.Join(c.Access.Healthchecks, ","),
		"nodes":               shape(c.Nodes),
	}
	if p := c.Persistence.Postgres; p != nil {
		values["persistence.postgres.host"] = p.Host
		values["persistence.postgres.port"] = strconv.Itoa(p.Port)
		values["persistence.postgres.user"] = p.User
		values["persistence.postgres.tls"] = strconv.FormatBool(p.TLS)
		values["persistence.postgres.adminDatabase"] = p.AdminDatabase
	}
	return values
}

// shape is the nodes without their names and addresses: 2 nodes: frontend,history · worker.
func shape(nodes []spec.Node) string {
	roles := make([]string, len(nodes))
	for i, n := range nodes {
		roles[i] = strings.Join(n.Roles, ",")
	}
	return fmt.Sprintf("%d nodes: %s", len(nodes), strings.Join(roles, " · "))
}

// compare lists the paths whose values differ, sorted. A path only one side has is
// a difference with nil on the other side.
func compare(from, to map[string]any) []Difference {
	paths := slices.Sorted(maps.Keys(from))
	for path := range to {
		if _, ok := from[path]; !ok {
			paths = append(paths, path)
		}
	}
	slices.Sort(paths)

	diffs := []Difference{}
	for _, path := range paths {
		if !reflect.DeepEqual(from[path], to[path]) {
			diffs = append(diffs, Difference{Path: path, From: from[path], To: to[path]})
		}
	}
	return diffs
}
