package render

import (
	"github.com/2SSK/tops/internal/secret"
	"github.com/2SSK/tops/internal/sizing"
	"github.com/2SSK/tops/internal/spec"
)

// NodeInput is everything one node's files are rendered from.
type NodeInput struct {
	Env        string
	Cluster    string
	Node       string
	Address    string // the node's own: its membership broadcast address
	Shards     int
	LogLevel   string
	Postgres   spec.Postgres
	Frontend   string                  // where this node's roles reach a frontend: 127.0.0.1:7233 here, else the first frontend node
	GoMaxProcs int                     // sizing.GoMaxProcs of the node's cores, the same for every role
	Roles      []RoleInput             // this node's, in stage order
	Secrets    map[string]secret.Value // rendered into temporal.env only
}

// RoleInput is one role on the node: its sizes.
type RoleInput struct {
	Role string
	Size sizing.RoleSize
}
