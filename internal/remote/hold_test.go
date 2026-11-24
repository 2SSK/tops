package remote

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/2SSK/tops/internal/helper"
	"github.com/2SSK/tops/internal/term"
	"github.com/2SSK/tops/internal/wire"
)

var t0 = time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)

// lockFleet is nodes whose helper is the real one, run in-process behind each test SSH server on
// a root of its own, with one clock for all of them that the test moves.
type lockFleet struct {
	roots []string
	cfgs  []Config
	mu    sync.Mutex
	now   time.Time
}

func newLockFleet(t *testing.T, size int) *lockFleet {
	t.Helper()
	f := &lockFleet{now: t0}
	for range size {
		host := helper.Host{Root: t.TempDir(), Now: f.clock}
		_, cfg := newNode(t, func(cmd string, stdin io.Reader, stdout, stderr io.Writer) uint32 {
			op := wire.Op(cmd[strings.LastIndex(cmd, " ")+1:])
			env := helper.Env{Host: host, Root: true, Stdin: stdin, Stdout: stdout, Log: slog.New(slog.NewJSONHandler(stderr, nil))}
			return uint32(helper.Run(context.Background(), op, env)) //nolint:gosec // G115: an exit code
		})
		f.roots, f.cfgs = append(f.roots, host.Root), append(f.cfgs, cfg)
	}
	return f
}

func (f *lockFleet) clock() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *lockFleet) advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
}

// run connects to every node, vps-01 onwards, as one run of tops.
func (f *lockFleet) run(t *testing.T, runID string) []*Node {
	t.Helper()
	var nodes []*Node
	for i, cfg := range f.cfgs {
		d, err := NewDialer(t.Context(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = d.Close() })
		c, err := d.Dial(t.Context(), fmt.Sprintf("vps-%02d", i+1), "127.0.0.1")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Close() })
		nodes = append(nodes, &Node{Client: c, SHA256: sha, req: wire.Request{RunID: runID, By: runID + "@laptop"}})
	}
	return nodes
}

// lease is node i's lease file, nil when it is free.
func (f *lockFleet) lease(t *testing.T, i int) *wire.Lease {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(f.roots[i], "var/lib/tops/lock"))
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

// holders is the run holding each node's lease, "" for a free node.
func (f *lockFleet) holders(t *testing.T) []string {
	t.Helper()
	ids := make([]string, len(f.roots))
	for i := range ids {
		if l := f.lease(t, i); l != nil {
			ids[i] = l.RunID
		}
	}
	return ids
}

func applyLease() wire.Lease {
	return wire.Lease{Env: "lab", Cluster: "tops-e2e", Command: "apply", By: "ssk@thinkpad", PID: 4242}
}

// TestHold: the run holds every node's lease, with the TRD's TTL; it renews them in the
// background by the node's clock; Release stops that, frees every node, and ends the run's
// context.
func TestHold(t *testing.T) {
	f := newLockFleet(t, 3)
	h, run, err := hold(t.Context(), f.run(t, "r-a"), applyLease(), 20*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if got := f.holders(t); !slices.Equal(got, []string{"r-a", "r-a", "r-a"}) || f.lease(t, 0).TTLSeconds != 120 {
		t.Errorf("holders %q, ttl %d s; want r-a everywhere for 120 s", got, f.lease(t, 0).TTLSeconds)
	}
	f.advance(time.Minute)
	renewed := func(i int) bool { return f.lease(t, i).RenewedAt.Equal(t0.Add(time.Minute)) }
	waitFor(t, func() bool { return renewed(0) && renewed(1) && renewed(2) })
	if err := h.Release(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := f.holders(t); !slices.Equal(got, []string{"", "", ""}) || run.Err() == nil {
		t.Errorf("after Release: holders %q, run's context %v", got, run.Err())
	}
}

// waitFor polls cond for up to 5 s.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); !cond(); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
	}
}

// acquireAs takes node's lease for another run, through its own connection.
func acquireAs(t *testing.T, n *Node, command, by string) {
	t.Helper()
	if r, err := n.Lock(t.Context(), wire.Lock{Action: wire.LockAcquire, Lease: wire.Lease{Env: "lab", Command: command, By: by, TTLSeconds: 120}}); err != nil || !r.Held {
		t.Fatalf("acquire = %+v, %v", r, err)
	}
}

// TestHoldHeld: another run's live lease on vps-02 refuses the run with lock.held (exit 3),
// naming that run and its ages by the node's clock; vps-01, taken first, is released, and
// vps-03 is never asked.
func TestHoldHeld(t *testing.T) {
	f := newLockFleet(t, 3)
	b := f.run(t, "r-b")
	acquireAs(t, b[1], "upgrade", "ana@laptop")
	f.advance(80 * time.Second)
	if r, err := b[1].Lock(t.Context(), wire.Lock{Action: wire.LockRenew}); err != nil || !r.Held {
		t.Fatalf("b's renew = %+v, %v", r, err)
	}
	f.advance(12 * time.Second)

	_, _, err := hold(t.Context(), f.run(t, "r-a"), applyLease(), time.Hour)
	var ue *term.UserError
	if !errors.As(err, &ue) || ue.Code != "lock.held" || term.ExitCode(err) != 3 {
		t.Fatalf("err = %v, want lock.held, exit 3", err)
	}
	if ue.What != "lab is locked" || ue.Why != "upgrade by ana@laptop, started 1m32s ago, renewed 12s ago (run r-b, on vps-02)" {
		t.Errorf("what %q, why %q", ue.What, ue.Why)
	}
	if got := f.holders(t); !slices.Equal(got, []string{"", "r-b", ""}) {
		t.Errorf("holders %q, want only b's on vps-02", got)
	}
}

// TestHoldTakesOverStale: a lease nobody renewed for its TTL is taken over, and the run is
// told whose it was, to name the interrupted apply.
func TestHoldTakesOverStale(t *testing.T) {
	f := newLockFleet(t, 2)
	acquireAs(t, f.run(t, "r-gone")[1], "apply", "ana@laptop")
	f.advance(3 * time.Minute)
	h, _, err := hold(t.Context(), f.run(t, "r-a"), applyLease(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = h.Release(t.Context()) }()
	if len(h.TakenOver) != 1 || h.TakenOver[0].Node != "vps-02" || h.TakenOver[0].Lease.RunID != "r-gone" {
		t.Errorf("taken over %+v, want r-gone's on vps-02", h.TakenOver)
	}
}

// TestHoldLost: a node that refuses a renewal, its lease expired while the laptop slept, ends
// the run's context with lock.lost naming the node; Release still frees every node.
func TestHoldLost(t *testing.T) {
	f := newLockFleet(t, 2)
	h, run, err := hold(t.Context(), f.run(t, "r-a"), applyLease(), 20*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	f.advance(3 * time.Minute)
	select {
	case <-run.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the run went on without the lock")
	}
	var ue *term.UserError
	if cause := context.Cause(run); !errors.As(cause, &ue) || ue.Code != "lock.lost" || !strings.HasPrefix(ue.What, "stopped: this run no longer holds the lock on vps-0") {
		t.Errorf("cause = %v, want lock.lost on a node", cause)
	}
	if err := h.Release(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := f.holders(t); !slices.Equal(got, []string{"", ""}) {
		t.Errorf("holders %q after Release", got)
	}
}

// TestLockLost: lock.lost says why the node refused: the lease expired, another run took it
// over, or the lease is gone. Ages are by the node's clock.
func TestLockLost(t *testing.T) {
	h := &Held{env: "lab"}
	n := &Node{Client: &Client{Node: "vps-02"}, req: wire.Request{RunID: "r-a"}}
	mine := &wire.Lease{RunID: "r-a", RenewedAt: t0, TTLSeconds: 120}
	other := &wire.Lease{RunID: "r-b", Command: "apply", By: "ana@laptop", RenewedAt: t0, TTLSeconds: 120}
	for _, c := range []struct {
		r   wire.LockResult
		why string
	}{
		{wire.LockResult{Lease: mine, Now: t0.Add(3 * time.Minute)}, "its lease expired 1m0s ago"},
		{wire.LockResult{Lease: other, Now: t0}, "its lease expired; run r-b took over (apply by ana@laptop)"},
		{wire.LockResult{Now: t0}, "no run holds it any more"},
	} {
		var ue *term.UserError
		if err := h.lockLost(n, c.r); !errors.As(err, &ue) || ue.Code != "lock.lost" || ue.Why != c.why ||
			ue.What != "stopped: this run no longer holds the lock on vps-02" || ue.Fix != "tops -e lab plan to see what is left" {
			t.Errorf("lockLost(%+v) = %#v, want why %q", c.r, err, c.why)
		}
	}
}

// TestHoldReleaseAfterInterrupt: Ctrl-C cancels the run's context; Release with a context of
// its own still frees every node.
func TestHoldReleaseAfterInterrupt(t *testing.T) {
	f := newLockFleet(t, 2)
	ctx, interrupt := context.WithCancel(t.Context())
	h, run, err := hold(ctx, f.run(t, "r-a"), applyLease(), 20*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	interrupt()
	<-run.Done()
	if err := h.Release(context.WithoutCancel(ctx)); err != nil {
		t.Fatal(err)
	}
	if got := f.holders(t); !slices.Equal(got, []string{"", ""}) {
		t.Errorf("holders %q after an interrupted run's Release", got)
	}
}
