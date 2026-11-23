package helper

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"

	"github.com/2SSK/tops/internal/wire"
)

// The node's lease and its two flocks (backend schema "Node state"). Every fenced op holds
// fenceFlock for its whole run, and so does a takeover, which therefore waits for a write in
// progress. Every change of the lease holds leaseFlock, only for as long as it reads and
// replaces the file: a renewal never waits for a fenced op, which may take ten minutes.
// Lock order: fenceFlock, then leaseFlock.
const (
	stateDir   = "/var/lib/tops"
	leaseFile  = "/var/lib/tops/lock"
	fenceFlock = "/var/lib/tops/lock.flock"
	leaseFlock = "/var/lib/tops/lease.flock"
)

// Lock acquires, renews or releases the node's lease for one run.
//   - acquire: takes a free lease, renews the run's own, refuses another run's live one, and
//
// takes over a stale one after its run's fenced op, if any, has ended;
//   - renew: extends the run's own live lease; one that expired or was taken over is lost;
//   - release: removes the run's own lease and never another's.
func (h Host) Lock(ctx context.Context, l wire.Lock) (wire.LockResult, error) {
	if err := os.MkdirAll(filepath.Join(h.Root, stateDir), 0o700); err != nil {
		return wire.LockResult{}, fmt.Errorf("create %s: %w", stateDir, err)
	}
	r, takeOver, err := h.lockStep(ctx, l, false)
	if err != nil || !takeOver {
		return r, err
	}
	unlock, err := h.flock(ctx, fenceFlock)
	if err != nil {
		return wire.LockResult{}, err
	}
	defer unlock()
	r, _, err = h.lockStep(ctx, l, true) // decided again: the stale run may have renewed meanwhile
	return r, err
}

// lockStep runs one action under leaseFlock. Unless fenced, acquire stops before taking over
// a stale lease and says so: the caller must first hold fenceFlock.
func (h Host) lockStep(ctx context.Context, l wire.Lock, fenced bool) (wire.LockResult, bool, error) {
	unlock, err := h.flock(ctx, leaseFlock)
	if err != nil {
		return wire.LockResult{}, false, err
	}
	defer unlock()
	cur, err := h.readLease()
	if err != nil {
		return wire.LockResult{}, false, err
	}
	s := leaseState{cur: cur, now: h.Now()}
	s.mine = cur != nil && cur.RunID == l.Lease.RunID
	s.live = cur != nil && s.now.Before(cur.RenewedAt.Add(time.Duration(cur.TTLSeconds)*time.Second))
	switch l.Action {
	case wire.LockRelease:
		if !s.mine {
			return wire.LockResult{Lease: cur}, false, nil // never another run's
		}
		return wire.LockResult{}, false, h.removeLease()
	case wire.LockRenew:
		r, err := h.renew(s)
		return r, false, err
	}
	return h.acquire(l.Lease, s, fenced)
}

// leaseState is the node's lease as one action found it, and the node's time.
type leaseState struct {
	cur        *wire.Lease
	mine, live bool // the request's run holds it; it has not expired
	now        time.Time
}

// renew extends the run's own live lease. One that expired, even if nobody took it over yet,
// is lost: the run must not count on it.
func (h Host) renew(s leaseState) (wire.LockResult, error) {
	if !s.mine || !s.live {
		return wire.LockResult{Lease: s.cur}, nil
	}
	s.cur.RenewedAt = s.now
	return wire.LockResult{Held: true, Lease: s.cur}, h.writeLease(s.cur)
}

// acquire takes a free lease, renews the run's own live one, refuses another run's live one,
// and takes over a stale one only when fenced.
func (h Host) acquire(l wire.Lease, s leaseState, fenced bool) (wire.LockResult, bool, error) {
	switch {
	case s.mine && s.live:
		r, err := h.renew(s)
		return r, false, err
	case s.live:
		return wire.LockResult{Lease: s.cur}, false, nil
	case s.cur != nil && !fenced:
		return wire.LockResult{}, true, nil
	}
	l.AcquiredAt, l.RenewedAt = s.now, s.now
	return wire.LockResult{Held: true, Lease: &l, Previous: s.cur}, false, h.writeLease(&l)
}

// readLease is the node's lease, nil when it is free.
func (h Host) readLease() (*wire.Lease, error) {
	b, err := os.ReadFile(filepath.Join(h.Root, leaseFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", leaseFile, err)
	}
	var l wire.Lease
	if err := json.Unmarshal(b, &l); err != nil {
		return nil, fmt.Errorf("read %s: %w", leaseFile, err)
	}
	return &l, nil
}

// writeLease replaces the lease file atomically: a render sees the old lease or the new one.
func (h Host) writeLease(l *wire.Lease) error {
	b, err := json.Marshal(l)
	if err != nil {
		return err
	}
	path := filepath.Join(h.Root, leaseFile)
	tmp := path + ".tops"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", leaseFile, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("write %s: %w", leaseFile, err)
	}
	return nil
}

func (h Host) removeLease() error {
	if err := os.Remove(filepath.Join(h.Root, leaseFile)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("release %s: %w", leaseFile, err)
	}
	return nil
}

// flock takes an exclusive flock(2) on path, waiting while another process holds it, until
// ctx ends. The returned func releases it; so does the helper's exit, whatever ends it.
func (h Host) flock(ctx context.Context, path string) (func(), error) {
	f, err := os.OpenFile(filepath.Join(h.Root, path), os.O_RDWR|os.O_CREATE, 0o600) //nolint:gosec // one of our own fixed paths
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	for {
		err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB) //nolint:gosec	// G115: a file descriptor fits an int
		if err == nil {
			return func() { _ = f.Close() }, nil
		}
		if !errors.Is(err, unix.EWOULDBLOCK) {
			_ = f.Close()
			return nil, fmt.Errorf("flock %s: %w", path, err)
		}
		select {
		case <-ctx.Done():
			_ = f.Close()
			return nil, fmt.Errorf("wait for %s: %w", path, ctx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// checkLock refuses an action the helper does not know, a run id that is not one, and a lease
// that would expire as it is taken.
func checkLock(l wire.Lock) error {
	switch {
	case l.Action != wire.LockAcquire && l.Action != wire.LockRenew && l.Action != wire.LockRelease:
		return fmt.Errorf("unknown lock action %q", l.Action)
	case !runIDName.MatchString(l.Lease.RunID):
		return fmt.Errorf("run id %q is not one", l.Lease.RunID)
	case l.Action == wire.LockAcquire && l.Lease.TTLSeconds < 1:
		return fmt.Errorf("lease ttl %d s", l.Lease.TTLSeconds)
	}
	return nil
}
