package remote

import (
	"slices"
	"testing"

	"github.com/2SSK/tops/internal/wire"
)

// TestNodeService: service runs the helper of this build with the unit and the action, and
// returns the unit's state.
func TestNodeService(t *testing.T) {
	var got wire.Request
	s, cfg := newNode(t, helperReplies(&got, `{"ok":true,"result":{"active":"active","enabled":true,"mainPid":4242,"exe":"/opt/temporal/releases/1.31.3/temporal-server","since":"2026-11-26T10:00:00Z","nRestarts":0}}`, 0))
	n := &Node{Client: mustDial(t, cfg), SHA256: sha, req: wire.Request{RunID: "r-1", ApplyID: "20261126T100000Z-aaaa", By: "ssk@thinkpad"}}
	st, err := n.Service(t.Context(), wire.Service{Unit: "temporal@history.service", Action: wire.ServiceRestart})
	if err != nil || st.Active != "active" || !st.Enabled || st.MainPID != 4242 {
		t.Errorf("service = %+v, %v", st, err)
	}
	if string(got.Args) != `{"unit":"temporal@history.service","action":"restart"}` {
		t.Errorf("args %s", got.Args)
	}
	if cmds := s.commands(); !slices.Equal(cmds, []string{cmdHelper(sha, wire.OpService)}) {
		t.Errorf("commands = %q", cmds)
	}
}
