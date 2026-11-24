//go:build helper

package remote

import (
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/2SSK/tops/internal/wire"
)

// TestHelperFence: through the real SSH path, a fenced op runs only for the run that holds the
// node's lease. Before any lease, and after another run took it, stage is lock.lost and says
// who holds the node; the holder's stage runs. The lease is root's alone.
func TestHelperFence(t *testing.T) {
	c := startContainer(t)
	d := c.dialer(t, c.cfg)
	n, err := c.connect(t, d)
	if err != nil {
		t.Fatal(err)
	}
	if ue := userError(t, stageErr(t, n), "lock.lost"); ue.Why != "no run holds it" {
		t.Errorf("before any lease: %s: %s", ue.What, ue.Why)
	}

	other, err := c.connectAs(t, d, wire.Request{RunID: "r-helper-other", By: "other@helper-test"})
	if err != nil {
		t.Fatal(err)
	}
	holdLease(t, other)
	if ue := userError(t, stageErr(t, n), "lock.lost"); ue.What != "stage on vps-01: this run does not hold the lock" || ue.Why != "run r-helper-other holds it: test by other@helper-test" {
		t.Errorf("another run's lease: %s: %s", ue.What, ue.Why)
	}
	if err := stageErr(t, other); err != nil {
		t.Errorf("the holder's stage: %v", err)
	}
	if got := docker(t, nil, "exec", c.id, "stat", "-c", "%n %a %U", "/var/lib/tops", "/var/lib/tops/lock"); got != "/var/lib/tops 700 root\n/var/lib/tops/lock 600 root" {
		t.Errorf("node state:\n%s", got)
	}
}

// stageErr is the error of a stage that has nothing to install.
func stageErr(t *testing.T, n *Node) error {
	t.Helper()
	_, err := n.Stage(t.Context(), wire.Stage{})
	return err
}

// runs connects to the node once per run id, as that many runs of tops would.
func (c *container) runs(t *testing.T, runIDs ...string) []*Node {
	t.Helper()
	d := c.dialer(t, c.cfg)
	nodes := make([]*Node, len(runIDs))
	for i, id := range runIDs {
		n, err := c.connectAs(t, d, wire.Request{RunID: id, By: id + "@helper-test"})
		if err != nil {
			t.Fatal(err)
		}
		nodes[i] = n
	}
	return nodes
}

// TestHelperLockRace: runs that acquire one node's lease at the same moment, each through its
// own SSH connection and helper process: exactly one holds it, and every other is told which.
func TestHelperLockRace(t *testing.T) {
	c := startContainer(t)
	nodes := c.runs(t, "r-race-0", "r-race-1", "r-race-2", "r-race-3")
	results := make([]wire.LockResult, len(nodes))
	errs := make([]error, len(nodes))
	var wg sync.WaitGroup
	for i, n := range nodes {
		wg.Go(func() { results[i], errs[i] = acquire(t, n, 3600) })
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		t.Fatal(err)
	}
	winner := onlyHolder(t, results)
	if got := docker(t, nil, "exec", c.id, "grep", "-o", `"runId":"[^"]*"`, "/var/lib/tops/lock"); got != `"runId":"`+winner+`"` {
		t.Errorf("the node's lease names %s, want %s", got, winner)
	}
}

// onlyHolder is the one run that holds the lease; every other must have been told it is that
// run's.
func onlyHolder(t *testing.T, results []wire.LockResult) string {
	t.Helper()
	var holders []string
	for _, r := range results {
		if r.Held {
			holders = append(holders, r.Lease.RunID)
		}
	}
	if len(holders) != 1 {
		t.Fatalf("holders %q, want exactly one", holders)
	}
	for _, r := range results {
		if !r.Held && (r.Lease == nil || r.Lease.RunID != holders[0]) {
			t.Errorf("a refused run was told %+v, want %s's lease", r.Lease, holders[0])
		}
	}
	return holders[0]
}

// TestHelperTakeOverWaitsForWrite: a's lease expires while its stage is still downloading. b's
// acquire waits until that stage has ended, which completes: it passed the fence before the
// lease expired. Then b holds the node, and a's lease is the one b took over.
func TestHelperTakeOverWaitsForWrite(t *testing.T) {
	c := startContainer(t)
	nodes := c.runs(t, "r-helper-a", "r-helper-b")
	a, b := nodes[0], nodes[1]
	cli := releaseArchive(t, "1.10.0", "temporal")
	downloading, finish := make(chan struct{}), make(chan struct{})
	base := serveOnNode(t, a, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(downloading)
		<-finish
		_, _ = w.Write(cli)
	}))
	if r, err := acquire(t, a, 2); err != nil || !r.Held {
		t.Fatalf("a's acquire = %+v, %v", r, err)
	}
	staged := make(chan error, 1)
	go func() {
		_, err := a.Stage(t.Context(), wire.Stage{Releases: []wire.Download{{Kind: "cli", Version: "1.10.0", URL: base + "/cli.tar.gz", SHA256: hexSum(cli), Extract: []string{"temporal"}}}})
		staged <- err
	}()
	<-downloading                       // a's stage is past the fence, writing
	time.Sleep(2500 * time.Millisecond) // and its 2 s lease has expired

	taken := make(chan wire.LockResult, 1)
	go func() {
		r, err := acquire(t, b, 3600)
		if err != nil {
			t.Errorf("b's acquire: %v", err)
		}
		taken <- r
	}()
	if r, ok := receive(taken, 2*time.Second); ok {
		t.Fatalf("b took the node during a's stage: %+v", r)
	}
	close(finish)
	if err := <-staged; err != nil {
		t.Errorf("a's stage, fenced before its lease expired: %v", err)
	}
	r, ok := receive(taken, 30*time.Second)
	if !ok || !r.Held || r.Previous == nil || r.Previous.RunID != "r-helper-a" {
		t.Errorf("b's acquire = %+v (ended %v), want held with a's lease as previous", r, ok)
	}
}

// receive is the next value on ch, if one comes within d.
func receive[T any](ch <-chan T, d time.Duration) (T, bool) {
	select {
	case v := <-ch:
		return v, true
	case <-time.After(d):
		var zero T
		return zero, false
	}
}
