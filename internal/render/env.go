package render

import (
	"net"
	"slices"
	"strconv"

	"github.com/2SSK/tops/internal/secret"
	"github.com/2SSK/tops/internal/settings"
	"github.com/2SSK/tops/internal/sizing"
	"github.com/2SSK/tops/internal/spec"
)

// Env is everything an environment's files are rendered from: the spec, and what tops worked
// out from it and from the nodes.
type Env struct {
	Name    string
	Cluster *spec.Cluster
	Sizes   []sizing.NodeSize        // Sizes[i] is Cluster.Nodes[i]'s
	Dynamic map[string]settings.File // by role: settings.Layers.Merge
	Secrets map[string]secret.Value
}

// Inputs is every node's input, in the spec's order.
func (e Env) Inputs() []NodeInput {
	c := e.Cluster
	ins := make([]NodeInput, len(c.Nodes))
	for i, n := range c.Nodes {
		in := NodeInput{
			Env: e.Name, Cluster: c.Cluster, Node: n.Name, Address: n.Address,
			Shards:     c.Shards,
			LogLevel:   c.Temporal.LogLevel,
			Postgres:   *c.Persistence.Postgres,
			Frontend:   e.frontend(n),
			GoMaxProcs: e.Sizes[i].GoMaxProcs,
			Secrets:    e.Secrets,
		}
		for _, size := range e.Sizes[i].Roles {
			in.Roles = append(in.Roles, RoleInput{Role: size.Role, Size: size, Dynamic: e.Dynamic[size.Role]})
		}
		ins[i] = in
	}
	return ins
}

// frontend is  where n's roles reach a frontend: n's own, else the first node that runs one.
// validate guarantees the cluster has a frontend.
func (e Env) frontend(n spec.Node) string {
	port := strconv.Itoa(PortsOf("frontend").GRPC)
	if slices.Contains(n.Roles, "frontend") {
		return net.JoinHostPort("127.0.0.1", port)
	}
	for _, other := range e.Cluster.Nodes {
		if slices.Contains(other.Roles, "frontend") {
			return net.JoinHostPort(other.Address, port)
		}
	}
	return ""
}
