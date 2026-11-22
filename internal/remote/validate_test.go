package remote

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"

	"github.com/2SSK/tops/internal/wire"
)

// TestNodeValidate: validate runs the helper of this build with the run's request and the
// roles' files as args, and returns what the target binary rejected.
func TestNodeValidate(t *testing.T) {
	var got wire.Request
	s, cfg := newNode(t, helperReplies(&got, `{"ok":true,"result":{"rejected":[{"role":"history","file":"config","message":"Unable to load configuration: bad"}]}}`, 0))
	n := &Node{Client: mustDial(t, cfg), SHA256: sha, req: wire.Request{RunID: "r-1", By: "ssk@thinkpad"}}
	args := wire.Validate{Version: "1.31.3", Roles: []wire.RoleFiles{{
		Role: "history", Config: "persistence: {}\n", DynamicConfig: "", Env: []string{"GOMEMLIMIT=812MiB"}, Secrets: []string{},
	}}}
	r, err := n.Validate(t.Context(), args)
	if err != nil {
		t.Fatal(err)
	}
	if want := []wire.Rejection{{Role: "history", File: "config", Message: "Unable to load configuration: bad"}}; !reflect.DeepEqual(r.Rejected, want) {
		t.Errorf("rejected = %+v, want %+v", r.Rejected, want)
	}
	var sent wire.Validate
	if err := json.Unmarshal(got.Args, &sent); err != nil || !reflect.DeepEqual(sent, args) {
		t.Errorf("args = %s (%v), want %+v", got.Args, err, args)
	}
	if got.RunID != "r-1" {
		t.Errorf("request = %+v, want the run's", got)
	}
	if cmds := s.commands(); !slices.Equal(cmds, []string{cmdHelper(sha, wire.OpValidate)}) {
		t.Errorf("commands = %q", cmds)
	}
}
