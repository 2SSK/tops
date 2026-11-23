package helper

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/2SSK/tops/internal/wire"
)

// lockNode is a node whose clock the test sets.
type lockNode struct {
	host Host
	now  time.Time
}

var t0 = time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)

func newLockNode(t *testing.T) *lockNode {
	t.Helper()
	n := &lockNode{now: t0}
	n.host = Host{Root: t.TempDir(), Now: func() time.Time { return n.now }}
	return n
}

// lease is what the workstation sends for a run: who and what, not the times.
func lease(runID string) wire.Lease {
	return wire.Lease{Env: "lab", Cluster: "tops-e2e", RunID: runID, ApplyID: "20261123T120000Z-" + runID[len(runID)-4:],
		Command: "apply", By: "ssk@thinkpad", PID: 4242, TTLSeconds: 120}
}

func (n *lockNode) lock(t *testing.T, action, runID string) wire.LockResult {
	t.Helper()
	r, err := n.host.Lock(t.Context(), wire.Lock{Action: action, Lease: lease(runID)})
	if err != nil {
		t.Fatalf("%s %s: %v", action, runID, err)
	}
	return r
}

// onDisk is the lease file, nil when there is none.
func (n *lockNode) onDisk(t *testing.T) *wire.Lease {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(n.host.Root, leaseFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var l wire.Lease
	if err := json.Unmarshal(b, &l); err != nil {
		t.Fatal(err)
	}
	return &l
}

// held is the lease a run holds, with the helper's times.
func held(runID string, acquired, renewed time.Time) *wire.Lease {
	l := lease(runID)
	l.AcquiredAt, l.RenewedAt = acquired, renewed
	return &l
}

// TestLockAcquire: a free node's lease goes to the run, with the node's time, in a 0600 file
// under a 0700 /var/lib/tops.
func TestLockAcquire(t *testing.T) {
	n := newLockNode(t)
	r := n.lock(t, wire.LockAcquire, "r-a-0001")
	want := held("r-a-0001", t0, t0)
	if !r.Held || !reflect.DeepEqual(r.Lease, want) || r.Previous != nil {
		t.Errorf("acquire = %+v, want held %+v", r, want)
	}
	if got := n.onDisk(t); !reflect.DeepEqual(got, want) {
		t.Errorf("lease file %+v, want %+v", got, want)
	}
	for path, mode := range map[string]os.FileMode{stateDir: os.ModeDir | 0o700, leaseFile: 0o600} {
		if fi, err := os.Stat(filepath.Join(n.host.Root, path)); err != nil || fi.Mode() != mode {
			t.Errorf("%s: %v %v, want %v", path, fi.Mode(), err, mode)
		}
	}
}

// TestLockAcquireHeld: another run's live lease is refused and left as it is, even a second
// before it expires; the answer names its holder.
func TestLockAcquireHeld(t *testing.T) {
	n := newLockNode(t)
	n.lock(t, wire.LockAcquire, "r-a-0001")
	n.now = t0.Add(119 * time.Second)
	r := n.lock(t, wire.LockAcquire, "r-b-0002")
	if want := held("r-a-0001", t0, t0); r.Held || !reflect.DeepEqual(r.Lease, want) {
		t.Errorf("acquire = %+v, want refused with %+v", r, want)
	}
	if got := n.onDisk(t); got.RunID != "r-a-0001" || !got.RenewedAt.Equal(t0) {
		t.Errorf("lease file %+v, want a's untouched", got)
	}
}

// TestLockAcquireAgain: a run acquiring its own live lease again, as a retry does, renews it.
func TestLockAcquireAgain(t *testing.T) {
	n := newLockNode(t)
	n.lock(t, wire.LockAcquire, "r-a-0001")
	n.now = t0.Add(10 * time.Second)
	if r := n.lock(t, wire.LockAcquire, "r-a-0001"); !r.Held || !reflect.DeepEqual(r.Lease, held("r-a-0001", t0, n.now)) {
		t.Errorf("acquire again = %+v", r)
	}
}

// TestLockRenew: renewing moves the expiry; a run whose lease expired, or that never held it,
// has lost it, and the file is left alone.
func TestLockRenew(t *testing.T) {
	n := newLockNode(t)
	n.lock(t, wire.LockAcquire, "r-a-0001")
	n.now = t0.Add(30 * time.Second)
	if r := n.lock(t, wire.LockRenew, "r-a-0001"); !r.Held || !r.Lease.RenewedAt.Equal(n.now) {
		t.Errorf("renew = %+v", r)
	}
	if r := n.lock(t, wire.LockRenew, "r-b-0002"); r.Held || r.Lease.RunID != "r-a-0001" {
		t.Errorf("renew by another run = %+v, want lost", r)
	}
	n.now = t0.Add(150 * time.Second) // 120 s after the last renewal
	if r := n.lock(t, wire.LockRenew, "r-a-0001"); r.Held {
		t.Errorf("renew after expiry = %+v, want lost", r)
	}
	if got := n.onDisk(t); !got.RenewedAt.Equal(t0.Add(30 * time.Second)) {
		t.Errorf("lease file renewed at %v, want the last renewal that held", got.RenewedAt)
	}
}

// TestLockTakeOverStale: a lease past its TTL goes to the next run, which is told whose it was.
func TestLockTakeOverStale(t *testing.T) {
	n := newLockNode(t)
	n.lock(t, wire.LockAcquire, "r-a-0001")
	n.now = t0.Add(120 * time.Second)
	r := n.lock(t, wire.LockAcquire, "r-b-0002")
	if want := held("r-b-0002", n.now, n.now); !r.Held || !reflect.DeepEqual(r.Lease, want) || !reflect.DeepEqual(r.Previous, held("r-a-0001", t0, t0)) {
		t.Errorf("acquire = %+v, want b's lease and a's as previous", r)
	}
	if r := n.lock(t, wire.LockRenew, "r-a-0001"); r.Held {
		t.Error("the run taken over still renews")
	}
}

// TestLockTakeOverWaitsForFencedOp: a takeover waits while the stale run's fenced op holds
// the fence, so it cannot interleave with a write in progress; then it takes the lease.
func TestLockTakeOverWaitsForFencedOp(t *testing.T) {
	n := newLockNode(t)
	n.lock(t, wire.LockAcquire, "r-a-0001")
	n.now = t0.Add(200 * time.Second)
	release := n.holdFence(t)
	done := make(chan wire.LockResult)
	go func() {
		r, _ := n.host.Lock(context.Background(), wire.Lock{Action: wire.LockAcquire, Lease: lease("r-b-0002")})
		done <- r
	}()
	select {
	case r := <-done:
		t.Fatalf("took over during a fenced op: %+v", r)
	case <-time.After(300 * time.Millisecond):
	}
	release()
	select {
	case r := <-done:
		if !r.Held || r.Lease.RunID != "r-b-0002" {
			t.Errorf("after the op, acquire = %+v", r)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the takeover never finished")
	}
}

// holdFence holds the fence as a fenced op of another process would.
func (n *lockNode) holdFence(t *testing.T) func() {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(n.host.Root, stateDir), 0o700); err != nil {
		t.Fatal(err)
	}
	unlock, err := n.host.flock(t.Context(), fenceFlock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(unlock)
	return unlock
}

// TestLockDuringFencedOp: renewing, and refusing another run's live lease, never wait for a
// fenced op, which may take ten minutes: the lease would expire under it.
func TestLockDuringFencedOp(t *testing.T) {
	n := newLockNode(t)
	n.lock(t, wire.LockAcquire, "r-a-0001")
	n.holdFence(t)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	for _, c := range []struct{ action, runID string }{{wire.LockRenew, "r-a-0001"}, {wire.LockAcquire, "r-b-0002"}, {wire.LockRelease, "r-b-0002"}} {
		if _, err := n.host.Lock(ctx, wire.Lock{Action: c.action, Lease: lease(c.runID)}); err != nil {
			t.Errorf("%s by %s during a fenced op: %v", c.action, c.runID, err)
		}
	}
}

// TestLockTakeOverCanceled: a takeover still waiting when the run's context ends changes nothing.
func TestLockTakeOverCanceled(t *testing.T) {
	n := newLockNode(t)
	n.lock(t, wire.LockAcquire, "r-a-0001")
	n.now = t0.Add(200 * time.Second)
	n.holdFence(t)
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	if _, err := n.host.Lock(ctx, wire.Lock{Action: wire.LockAcquire, Lease: lease("r-b-0002")}); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want the deadline", err)
	}
	if got := n.onDisk(t); got.RunID != "r-a-0001" {
		t.Errorf("lease file %+v, want a's", got)
	}
}

// TestLockRelease: a run releases its own lease; another run's stays, even a stale one.
func TestLockRelease(t *testing.T) {
	n := newLockNode(t)
	n.lock(t, wire.LockAcquire, "r-a-0001")
	n.now = t0.Add(time.Hour)
	if r := n.lock(t, wire.LockRelease, "r-b-0002"); r.Held || r.Lease.RunID != "r-a-0001" || n.onDisk(t) == nil {
		t.Errorf("release by another run = %+v; the lease must stay", r)
	}
	if r := n.lock(t, wire.LockRelease, "r-a-0001"); r.Held || r.Lease != nil || n.onDisk(t) != nil {
		t.Errorf("release = %+v, want the lease gone", r)
	}
	if r := n.lock(t, wire.LockRelease, "r-a-0001"); r.Held || r.Lease != nil {
		t.Errorf("release again = %+v, want nothing to do", r)
	}
}

// TestLockUnreadable: a lease file that is not a lease is an error, not a free node.
func TestLockUnreadable(t *testing.T) {
	n := newLockNode(t)
	writeFile(t, filepath.Join(n.host.Root, leaseFile), "{not json")
	if _, err := n.host.Lock(t.Context(), wire.Lock{Action: wire.LockAcquire, Lease: lease("r-a-0001")}); err == nil {
		t.Error("acquired over an unreadable lease")
	}
}

func lockRequest(t *testing.T, runID string, l wire.Lock) string {
	t.Helper()
	args, err := json.Marshal(l)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(wire.Request{RunID: runID, ApplyID: "20261123T120000Z-9c1e", By: "ssk@thinkpad", Args: args})
	return string(b)
}

// TestRunLock: through the op, the lease carries the request's run and apply id, whatever the
// args said: the run id is the token every fenced op is checked against.
func TestRunLock(t *testing.T) {
	n := newLockNode(t)
	l := wire.Lock{Action: wire.LockAcquire, Lease: lease("r-forged-0000")}
	code, resp, _ := run(t, Env{Host: n.host, Root: true}, wire.OpLock, lockRequest(t, "r-20261123T120000Z-9c1e", l))
	var r wire.LockResult
	if code != ExitOK || json.Unmarshal(resp.Result, &r) != nil || !r.Held {
		t.Fatalf("exit %d, result %s", code, resp.Result)
	}
	if got := n.onDisk(t); got.RunID != "r-20261123T120000Z-9c1e" || got.ApplyID != "20261123T120000Z-9c1e" {
		t.Errorf("lease file names run %q apply %q, want the request's", got.RunID, got.ApplyID)
	}
}

// TestLockRefusesBadArgs: an unknown action, a run id that is not one, and a lease that
// expires as it is taken are refused before anything is written.
func TestLockRefusesBadArgs(t *testing.T) {
	noTTL := lease("r-a-0001")
	noTTL.TTLSeconds = 0
	for name, c := range map[string]struct {
		runID string
		l     wire.Lock
	}{
		"action": {"r-a-0001", wire.Lock{Action: "steal", Lease: lease("r-a-0001")}},
		"run id": {"../../etc", wire.Lock{Action: wire.LockAcquire, Lease: lease("r-a-0001")}},
		"ttl":    {"r-a-0001", wire.Lock{Action: wire.LockAcquire, Lease: noTTL}},
	} {
		t.Run(name, func(t *testing.T) {
			n := newLockNode(t)
			code, resp, _ := run(t, Env{Host: n.host, Root: true}, wire.OpLock, lockRequest(t, c.runID, c.l))
			if code != ExitBadRequest || resp.Error == nil || resp.Error.Code != codeBadRequest {
				t.Errorf("exit %d, error %+v, want a bad request", code, resp.Error)
			}
			if _, err := os.Stat(filepath.Join(n.host.Root, stateDir)); err == nil {
				t.Error("/var/lib/tops was created")
			}
		})
	}
}
