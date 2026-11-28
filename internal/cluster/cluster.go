// Package cluster reads Temporal through its frontends: whether each serves, the membership
// rings and the cluster's identity as each one sees them, and the shards a history host owns.
// Every read goes to a frontend node's own 127.0.0.1:7233, through that node's SSH tunnel.
package cluster

import (
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
	"sync"

	"go.temporal.io/server/api/adminservice/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

const (
	// frontendAddr is where every frontend listens, as its own node reaches it.
	frontendAddr = "127.0.0.1:7233"
	// workflowService is the name a frontend reports SERVING for once it takes requests.
	workflowService = "temporal.api.workflowservice.v1.WorkflowService"
)

// DialFunc opens a TCP connection from a node: the node's remote.Client.DialContext.
type DialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

// View is the cluster as one frontend sees it. Views of different frontends are never merged:
// a gate compares them.
type View struct {
	Frontend string
	Rings    map[string][]string
	Identity Identity
}

// Identity is what the cluster says it is.
type Identity struct {
	ClusterName      string
	ClusterID        string
	Shards           int // history_shard_count
	PersistenceStore string
	VisibilityStore  string
	ServerVersion    string
}

// Reader reads through each frontend node it was given, over one connection per node, made
// on the first read through it. Its reads take no time limit of their own: the caller's
// context sets it.
type Reader struct {
	dial  map[string]DialFunc
	mu    sync.Mutex
	conns map[string]*grpc.ClientConn
}

// New reads through the frontend nodes in dial, by name.
func New(dial map[string]DialFunc) *Reader {
	return &Reader{dial: dial, conns: map[string]*grpc.ClientConn{}}
}

// Serving says whether node's frontend takes requests. A frontend still starting, which has
// not registered the service yet, is not serving; one that cannot be reached is an error.
func (r *Reader) Serving(ctx context.Context, node string) (bool, error) {
	conn, err := r.conn(node)
	if err != nil {
		return false, err
	}
	resp, err := healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{Service: workflowService})
	if status.Code(err) == codes.NotFound {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("health of the frontend on %s: %w", node, err)
	}
	return resp.GetStatus() == healthpb.HealthCheckResponse_SERVING, nil
}

// Describe is the cluster as node's frontend sees it.
func (r *Reader) Describe(ctx context.Context, node string) (View, error) {
	conn, err := r.conn(node)
	if err != nil {
		return View{}, err
	}
	resp, err := adminservice.NewAdminServiceClient(conn).DescribeCluster(ctx, &adminservice.DescribeClusterRequest{})
	if err != nil {
		return View{}, fmt.Errorf("describe the cluster through %s: %w", node, err)
	}
	v := View{Frontend: node, Rings: map[string][]string{}, Identity: Identity{
		ClusterName:      resp.GetClusterName(),
		ClusterID:        resp.GetClusterId(),
		Shards:           int(resp.GetHistoryShardCount()),
		PersistenceStore: resp.GetPersistenceStore(),
		VisibilityStore:  resp.GetVisibilityStore(),
		ServerVersion:    resp.GetServerVersion(),
	}}
	for _, ring := range resp.GetMembershipInfo().GetRings() {
		members := []string{}
		for _, m := range ring.GetMembers() {
			members = append(members, m.GetIdentity())
		}
		slices.Sort(members)
		v.Rings[ring.GetRole()] = members
	}
	return v, nil
}

// Shards is how many shards the history host at addr ("10.80.0.11:7234") owns, as node's
// frontend reads it.
func (r *Reader) Shards(ctx context.Context, node, addr string) (int, error) {
	conn, err := r.conn(node)
	if err != nil {
		return 0, err
	}
	resp, err := adminservice.NewAdminServiceClient(conn).DescribeHistoryHost(ctx, &adminservice.DescribeHistoryHostRequest{HostAddress: addr})
	if err != nil {
		return 0, fmt.Errorf("shards of %s through %s: %w", addr, node, err)
	}
	return len(resp.GetShardIds()), nil
}

// Close closes every connection the reader made.
func (r *Reader) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	var errs []error
	for node, conn := range r.conns {
		errs = append(errs, conn.Close())
		delete(r.conns, node)
	}
	return errors.Join(errs...)
}

// conn is node's connection, made on its first use. grpc.NewClient does not dial: the tunnel
// opens on the first call, and again whenever the connection breaks.
func (r *Reader) conn(node string) (*grpc.ClientConn, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if conn, ok := r.conns[node]; ok {
		return conn, nil
	}
	dial, ok := r.dial[node]
	if !ok {
		return nil, fmt.Errorf("%s is not a frontend node tops reads through", node)
	}
	conn, err := grpc.NewClient("passthrough:///"+frontendAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, addr string) (net.Conn, error) { return dial(ctx, "tcp", addr) }),
	)
	if err != nil {
		return nil, fmt.Errorf("connect to the frontend on %s: %w", node, err)
	}
	r.conns[node] = conn
	return conn, nil
}
