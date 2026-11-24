package remote

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/2SSK/tops/internal/term"
	"github.com/2SSK/tops/internal/wire"
)

// the run's lease (TRD "Cluster lock"): it expires after 120 s without a renewal, and is
// renewed every 30 s, so three renewals in a row can fail before it is lost.
const (
	leaseTTL   = 120 // seconds
	renewEvery = 30 * time.Second
)

// Held is the run's elase on every node of an environment, renewed in the background until
// Release.
type Held struct {
	TakenOver []TakenOver // the stale leases acquire took over: the runs they interrupted

	env      string
	nodes    []*Node // the ones that granted the lease, in order
	stop     context.CancelCauseFunc
	renewals sync.WaitGroup
}

// TakenOver is a stale lease acquire took over on one node.
type TakenOver struct {
	Node  string
	Lease wire.Lease
}

// Hold acquires the lease on every node, one after the other in the order given: spec order,
// so two runs never wait on each other in a cycle. A node that refuses is lock.held, naming
// the run that holds it, and the nodes already taken are released. Then each node's lease is
// renewed every 30 s until Release. The context returned is the run's: it ends with the
// parent, or with lock.lost as its cause when a node refuses a renewal, so the run stops.
func Hold(ctx context.Context, nodes []*Node, l wire.Lease) (*Held, context.Context, error) {
	return hold(ctx, nodes, l, renewEvery)
}

func hold(ctx context.Context, nodes []*Node, l wire.Lease, every time.Duration) (*Held, context.Context, error) {
	l.TTLSeconds = leaseTTL
	h := &Held{env: l.Env}
	for _, n := range nodes {
		r, err := n.Lock(ctx, wire.Lock{Action: wire.LockAcquire, Lease: l})
		if err == nil && !r.Held {
			err = lockHeld(n.Node, r)
		}
		if err != nil {
			return nil, nil, errors.Join(err, h.release(context.WithoutCancel(ctx)))
		}
		h.nodes = append(h.nodes, n)
		if r.Previous != nil {
			h.TakenOver = append(h.TakenOver, TakenOver{Node: n.Node, Lease: *r.Previous})
		}
	}
	run, stop := context.WithCancelCause(ctx)
	h.stop = stop
	for _, n := range h.nodes {
		h.renewals.Go(func() { h.renew(run, n, every) })
	}
	return h, run, nil
}

// renew renews the lease on n every interval until ctx ends. A refused renewal means the run
// has lost the lock on n: it ends the run with lock.lost. A renewal that fails as a network,
// blip does, is tried again at the next tick: the TTL outlasts three of them.
func (h *Held) renew(ctx context.Context, n *Node, every time.Duration) {
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		r, err := n.Lock(ctx, wire.Lock{Action: wire.LockRenew})
		switch {
		case err != nil:
			n.log.Warn("renew the lease", "err", err)
		case !r.Held:
			h.stop(h.lockLost(n, r))
			return
		}
	}
}

// Release stops the renewals and releases the lease on every node, the last taken first. It
// runs on every exit path, after a failure or an interrupt too: pass a context that is not
// the run's, which may be canceled already.
func (h *Held) Release(ctx context.Context) error {
	h.stop(nil)
	h.renewals.Wait()
	return h.release(ctx)
}

func (h *Held) release(ctx context.Context) error {
	var errs []error
	for _, n := range slices.Backward(h.nodes) {
		if _, err := n.Lock(ctx, wire.Lock{Action: wire.LockRelease}); err != nil {
			errs = append(errs, fmt.Errorf("release the lock on %s: %w", n.Node, err))
		}

	}
	return errors.Join(errs...)
}

// lockHeld is the refusal when another run's live lease is one node. The ages are by the
// node's clock, which the lease's times are.
func lockHeld(node string, r wire.LockResult) error {
	l := r.Lease
	return &term.UserError{
		Code: "lock.held",
		What: l.Env + " is locked",
		Why: fmt.Sprintf("%s by %s, started %s ago, renewed %s ago (run %s, on %s)",
			l.Command, l.By, age(r.Now, l.AcquiredAt), age(r.Now, l.RenewedAt), l.RunID, node),
		Fix: "wait for that run to end; a lease nobody renews expires 2 min after its last renewal",
	}
}

// lockLost is the end of the run when node refused to renew its lease.
func (h *Held) lockLost(n *Node, r wire.LockResult) error {
	why := "no run holds it any more"
	switch l := r.Lease; {
	case l != nil && l.RunID != n.req.RunID:
		why = fmt.Sprintf("its lease expired; run %s took over (%s by %s)", l.RunID, l.Command, l.By)
	case l != nil:
		why = fmt.Sprintf("its lease expired %s ago", age(r.Now, l.RenewedAt.Add(time.Duration(l.TTLSeconds)*time.Second)))
	}
	return &term.UserError{
		Code: "lock.lost",
		What: "stopped: this run no longer holds the lock on " + n.Node,
		Why:  why,
		Fix:  fmt.Sprintf("tops -e %s plan to see what is left", h.env),
	}
}

func age(now, then time.Time) time.Duration { return now.Sub(then).Round(time.Second) }
