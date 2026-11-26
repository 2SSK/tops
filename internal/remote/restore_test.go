package remote

import (
	"slices"
	"testing"

	"github.com/2SSK/tops/internal/wire"
)

// TestNodeRestore: restore runs the helper of this build with the run's apply id and the
// snapshot to bring back.
func TestNodeRestore(t *testing.T) {
	var got wire.Request
	s, cfg := newNode(t, helperReplies(&got, `{"ok":true,"result":{"snapshot":"20261124T130000Z-bbbb","written":["/etc/temporal/history.yaml"],"removed":null}}`, 0))
	n := &Node{Client: mustDial(t, cfg), SHA256: sha, req: wire.Request{RunID: "r-1", ApplyID: "20261124T130000Z-bbbb", By: "ssk@thinkpad"}}
	r, err := n.Restore(t.Context(), wire.Restore{Snapshot: "20261124T120000Z-aaaa"})
	if err != nil || r.Snapshot != "20261124T130000Z-bbbb" || !slices.Equal(r.Written, []string{"/etc/temporal/history.yaml"}) {
		t.Errorf("restore = %+v, %v", r, err)
	}
	if got.ApplyID != "20261124T130000Z-bbbb" || string(got.Args) != `{"snapshot":"20261124T120000Z-aaaa"}` {
		t.Errorf("request = %+v, args %s", got, got.Args)
	}
	if cmds := s.commands(); !slices.Equal(cmds, []string{cmdHelper(sha, wire.OpRestore)}) {
		t.Errorf("commands = %q", cmds)
	}
}
