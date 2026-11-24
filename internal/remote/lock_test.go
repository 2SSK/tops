package remote

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"

	"github.com/2SSK/tops/internal/wire"
)

// TestNodeLock: lock runs the helper of this build with the run's request and the action as
// args, and returns whose the lease is.
func TestNodeLock(t *testing.T) {
	var got wire.Request
	s, cfg := newNode(t, helperReplies(&got, `{"ok":true,"result":{"held":false,"lease":{"runId":"r-other","command":"apply","by":"ana@laptop","ttlSeconds":120}}}`, 0))
	n := &Node{Client: mustDial(t, cfg), SHA256: sha, req: wire.Request{RunID: "r-1", ApplyID: "20261123T120000Z-9c1e", By: "ssk@thinkpad"}}
	args := wire.Lock{Action: wire.LockAcquire, Lease: wire.Lease{Env: "lab", Command: "apply", By: "ssk@thinkpad", PID: 4242, TTLSeconds: 120}}
	r, err := n.Lock(t.Context(), args)
	if err != nil {
		t.Fatal(err)
	}
	if want := (wire.LockResult{Lease: &wire.Lease{RunID: "r-other", Command: "apply", By: "ana@laptop", TTLSeconds: 120}}); !reflect.DeepEqual(r, want) {
		t.Errorf("result = %+v, want %+v", r, want)
	}
	var sent wire.Lock
	if err := json.Unmarshal(got.Args, &sent); err != nil || !reflect.DeepEqual(sent, args) {
		t.Errorf("args = %s (%v), want %+v", got.Args, err, args)
	}
	if got.RunID != "r-1" || got.ApplyID != "20261123T120000Z-9c1e" {
		t.Errorf("request = %+v, want the run's", got)
	}
	if cmds := s.commands(); !slices.Equal(cmds, []string{cmdHelper(sha, wire.OpLock)}) {
		t.Errorf("commands = %q", cmds)
	}
}

// TestNodeLockLost: a fenced op refused by the helper reaches the user as lock.lost, with the
// node in what happened and the holder in why.
func TestNodeLockLost(t *testing.T) {
	var got wire.Request
	_, cfg := newNode(t, helperReplies(&got, `{"ok":false,"error":{"code":"lock.lost","message":"this run does not hold the lock","detail":"run r-other holds it: apply by ana@laptop"}}`, 1))
	n := &Node{Client: mustDial(t, cfg), SHA256: sha}
	_, err := n.Stage(t.Context(), wire.Stage{})
	ue := userError(t, err, "lock.lost")
	if ue.What != "stage on vps-01: this run does not hold the lock" || ue.Why != "run r-other holds it: apply by ana@laptop" {
		t.Errorf("what %q, why %q", ue.What, ue.Why)
	}
}
