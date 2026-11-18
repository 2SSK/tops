// Package render turns a node's spec, sizing and dynamic config into the exact files tops
// installs on it (TRD "Rendering per role"). It is pure, and its output is byte-stable: the
// same input renders the same bytes, so a file's hash says whether anything changed.
package render

import (
	"github.com/2SSK/tops/internal/spec"
)

// Ports are one role's ports. tops' scheme: the role's gRPC port decides the last digit of
// every port it owns (TRD "Ports"). This table is the only place they are written down.
type Ports struct {
	Role       string
	GRPC       int
	Membership int
	HTTP       int // 0: the role serves no HTTP
	Metrics    int // on 0.0.0.0, for Prometheus
	Pprof      int // on 127.0.0.1 only
}

var portTable = map[string]Ports{
	"frontend": {Role: "frontend", GRPC: 7233, Membership: 6933, HTTP: 7243, Metrics: 9233, Pprof: 7933},
	"history":  {Role: "history", GRPC: 7234, Membership: 6934, Metrics: 9234, Pprof: 7934},
	"matching": {Role: "matching", GRPC: 7235, Membership: 6935, Metrics: 9235, Pprof: 7935},
	"worker":   {Role: "worker", GRPC: 7239, Membership: 6939, Metrics: 9239, Pprof: 7939},
}

// PortsOf returns a role's ports.
func PortsOf(role string) Ports { return portTable[role] }

// allPorts is every role's ports in stage order: each config's services block lists all four,
// and Temporal reads only the block of the service it starts.
func allPorts() []Ports {
	ps := make([]Ports, len(spec.Roles))
	for i, r := range spec.Roles {
		ps[i] = portTable[r]
	}
	return ps
}
