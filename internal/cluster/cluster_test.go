package cluster

import (
	"context"
	"errors"
	"net"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"go.temporal.io/server/api/adminservice/v1"
	clusterspb "go.temporal.io/server/api/cluster/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

// frontend is a fake frontend node: a health service and the two admin calls tops makes,
// answering what the test sets, behind a tunnel that counts its dials.
type frontend struct {
	adminservice.UnimplementedAdminServiceServer
	health *health.Server
	lis    *bufconn.Listener

	mu      sync.Mutex
	rings   map[string][]string
	shards  []int // DescribeHistoryHost answers each in turn, then the last again
	hosts   []string
	dialed  []string // the address each dial asked the node for
	failAll bool
}

func newFrontend(t *testing.T, rings map[string][]string) *frontend {
	t.Helper()
	f := &frontend{health: health.NewServer(), lis: bufconn.Listen(1 << 20), rings: rings}
	s := grpc.NewServer()
	healthpb.RegisterHealthServer(s, f.health)
	adminservice.RegisterAdminServiceServer(s, f)
	go func() { _ = s.Serve(f.lis) }()
	t.Cleanup(s.Stop)
	return f
}

// dial is the node's tunnel: it records the address and connects to the fake.
func (f *frontend) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	f.mu.Lock()
	f.dialed = append(f.dialed, network+" "+addr)
	f.mu.Unlock()
	return f.lis.DialContext(ctx)
}

func (f *frontend) DescribeCluster(context.Context, *adminservice.DescribeClusterRequest) (*adminservice.DescribeClusterResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failAll {
		return nil, status.Error(codes.Unavailable, "membership not ready")
	}
	info := &clusterspb.MembershipInfo{}
	for role, members := range f.rings {
		ring := &clusterspb.RingInfo{Role: role, MemberCount: int32(len(members))} //nolint:gosec // a handful
		for _, m := range members {
			ring.Members = append(ring.Members, &clusterspb.HostInfo{Identity: m})
		}
		info.Rings = append(info.Rings, ring)
	}
	return &adminservice.DescribeClusterResponse{
		ClusterName: "tops-e2e", ClusterId: "6f1c", HistoryShardCount: 512,
		PersistenceStore: "postgres12", VisibilityStore: "postgres12", ServerVersion: "1.31.3",
		MembershipInfo: info,
	}, nil
}

func (f *frontend) DescribeHistoryHost(_ context.Context, req *adminservice.DescribeHistoryHostRequest) (*adminservice.DescribeHistoryHostResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hosts = append(f.hosts, req.GetHostAddress())
	n := f.shards[0]
	if len(f.shards) > 1 {
		f.shards = f.shards[1:]
	}
	ids := make([]int32, n)
	for i := range ids {
		ids[i] = int32(i) //nolint:gosec // at most 512
	}
	return &adminservice.DescribeHistoryHostResponse{ShardsNumber: int32(n), ShardIds: ids}, nil //nolint:gosec // at most 512
}

// reader reads through the fakes, by node name.
func reader(t *testing.T, fronts map[string]*frontend) *Reader {
	t.Helper()
	dial := map[string]DialFunc{}
	for node, f := range fronts {
		dial[node] = f.dial
	}
	r := New(dial)
	t.Cleanup(func() { _ = r.Close() })
	return r
}

var lab = map[string][]string{
	"frontend": {"10.80.0.12:7233", "10.80.0.11:7233"},
	"history":  {"10.80.0.11:7234", "10.80.0.12:7234"},
	"matching": {"10.80.0.11:7235"},
	"worker":   {},
}

// TestServing: SERVING is reported for the workflow service by name; a frontend that has not
// registered it yet is starting, not serving, and so is one shutting down.
func TestServing(t *testing.T) {
	f := newFrontend(t, lab)
	r := reader(t, map[string]*frontend{"vps-01": f})
	for _, step := range []struct {
		set  func()
		want bool
	}{
		{func() {}, false},
		{func() { f.health.SetServingStatus(adminServiceName, healthpb.HealthCheckResponse_SERVING) }, false},
		{func() { f.health.SetServingStatus(workflowService, healthpb.HealthCheckResponse_NOT_SERVING) }, false},
		{func() { f.health.SetServingStatus(workflowService, healthpb.HealthCheckResponse_SERVING) }, true},
	} {
		step.set()
		if got, err := r.Serving(t.Context(), "vps-01"); err != nil || got != step.want {
			t.Errorf("serving = %v, %v; want %v", got, err, step.want)
		}
	}
	f.health.Shutdown() // every service NOT_SERVING, as a frontend stopping
	if got, err := r.Serving(t.Context(), "vps-01"); err != nil || got {
		t.Errorf("stopping: serving = %v, %v", got, err)
	}
}

// adminServiceName is a service a frontend serves before the workflow service does.
const adminServiceName = "temporal.server.api.adminservice.v1.AdminService"

// TestDescribe: a view holds each ring's members sorted, an empty ring as empty, and the
// cluster's identity.
func TestDescribe(t *testing.T) {
	r := reader(t, map[string]*frontend{"vps-01": newFrontend(t, lab)})
	v, err := r.Describe(t.Context(), "vps-01")
	if err != nil {
		t.Fatal(err)
	}
	want := View{Frontend: "vps-01", Rings: map[string][]string{
		"frontend": {"10.80.0.11:7233", "10.80.0.12:7233"},
		"history":  {"10.80.0.11:7234", "10.80.0.12:7234"},
		"matching": {"10.80.0.11:7235"},
		"worker":   {},
	}, Identity: Identity{ClusterName: "tops-e2e", ClusterID: "6f1c", Shards: 512, PersistenceStore: "postgres12", VisibilityStore: "postgres12", ServerVersion: "1.31.3"}}
	if !reflect.DeepEqual(v, want) {
		t.Errorf("view:\n got %+v\nwant %+v", v, want)
	}
}

// TestDescribeViewsApart: two frontends that disagree, one missing a history member, give two
// views, each as its frontend said; a failing frontend fails only its own.
func TestDescribeViewsApart(t *testing.T) {
	partial := map[string][]string{"history": {"10.80.0.11:7234"}}
	fronts := map[string]*frontend{"vps-01": newFrontend(t, lab), "vps-02": newFrontend(t, partial), "vps-03": newFrontend(t, lab)}
	fronts["vps-03"].failAll = true
	r := reader(t, fronts)
	one, err1 := r.Describe(t.Context(), "vps-01")
	two, err2 := r.Describe(t.Context(), "vps-02")
	if err1 != nil || err2 != nil {
		t.Fatal(err1, err2)
	}
	if len(one.Rings["history"]) != 2 || !reflect.DeepEqual(two.Rings, partial) || two.Frontend != "vps-02" {
		t.Errorf("views merged or lost:\n%+v\n%+v", one, two)
	}
	if _, err := r.Describe(t.Context(), "vps-03"); status.Code(errors.Unwrap(err)) != codes.Unavailable || !strings.Contains(err.Error(), "vps-03") {
		t.Errorf("failing frontend: %v", err)
	}
}

// TestShards: a history host's shards are counted as each read finds them, through the
// frontend asked: a dip while shards move shows.
func TestShards(t *testing.T) {
	f := newFrontend(t, lab)
	f.shards = []int{512, 300, 512}
	r := reader(t, map[string]*frontend{"vps-01": f})
	var got []int
	for range 3 {
		n, err := r.Shards(t.Context(), "vps-01", "10.80.0.12:7234")
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, n)
	}
	if !reflect.DeepEqual(got, []int{512, 300, 512}) || !reflect.DeepEqual(f.hosts, []string{"10.80.0.12:7234", "10.80.0.12:7234", "10.80.0.12:7234"}) {
		t.Errorf("shards %v, asked for %q", got, f.hosts)
	}
}

// TestConnections: nothing is dialed before the first read; then one connection per node,
// to the node's own 127.0.0.1:7233, whatever is read; an unknown node is an error.
func TestConnections(t *testing.T) {
	one, two := newFrontend(t, lab), newFrontend(t, lab)
	one.shards = []int{512}
	one.health.SetServingStatus(workflowService, healthpb.HealthCheckResponse_SERVING)
	r := reader(t, map[string]*frontend{"vps-01": one, "vps-02": two})
	if len(one.dialed)+len(two.dialed) != 0 {
		t.Fatalf("dialed before a read: %q %q", one.dialed, two.dialed)
	}
	for range 2 {
		_, err1 := r.Serving(t.Context(), "vps-01")
		_, err2 := r.Describe(t.Context(), "vps-01")
		_, err3 := r.Shards(t.Context(), "vps-01", "10.80.0.11:7234")
		if err := errors.Join(err1, err2, err3); err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(one.dialed, []string{"tcp 127.0.0.1:7233"}) || len(two.dialed) != 0 {
		t.Errorf("dialed %q and %q, want one tunnel to vps-01's frontend", one.dialed, two.dialed)
	}
	for _, read := range []func() error{
		func() error { _, err := r.Serving(t.Context(), "vps-09"); return err },
		func() error { _, err := r.Describe(t.Context(), "vps-09"); return err },
		func() error { _, err := r.Shards(t.Context(), "vps-09", "10.80.0.11:7234"); return err },
	} {
		if err := read(); err == nil || !strings.Contains(err.Error(), "vps-09 is not a frontend node") {
			t.Errorf("unknown node: %v", err)
		}
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestTunnelFails: a frontend whose tunnel does not open is an error naming the node, for
// every read; the caller's context bounds the wait.
func TestTunnelFails(t *testing.T) {
	r := New(map[string]DialFunc{"vps-01": func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("ssh: rejected: connect failed (Connection refused)")
	}})
	t.Cleanup(func() { _ = r.Close() })
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	for _, read := range []func() error{
		func() error { _, err := r.Serving(ctx, "vps-01"); return err },
		func() error { _, err := r.Describe(ctx, "vps-01"); return err },
		func() error { _, err := r.Shards(ctx, "vps-01", "10.80.0.11:7234"); return err },
	} {
		if err := read(); status.Code(errors.Unwrap(err)) != codes.Unavailable || !strings.Contains(err.Error(), "vps-01") {
			t.Errorf("read = %v, want unavailable through vps-01", err)
		}
	}
}
