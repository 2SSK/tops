package remote

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"

	"github.com/2SSK/tops/internal/wire"
)

// TestNodeWrite: write runs the helper of this build with the run's request, apply id
// included, and the files as args on stdin, never in the command.
func TestNodeWrite(t *testing.T) {
	var got wire.Request
	s, cfg := newNode(t, helperReplies(&got, `{"ok":true,"result":{"snapshot":"20261124T120000Z-aaaa","written":["/etc/temporal/temporal.env"],"removed":null}}`, 0))
	n := &Node{Client: mustDial(t, cfg), SHA256: sha, req: wire.Request{RunID: "r-1", ApplyID: "20261124T120000Z-aaaa", By: "ssk@thinkpad"}}
	args := wire.Write{Files: []wire.File{{Path: "/etc/temporal/temporal.env", Content: []byte("TEMPORAL_DB_PASSWORD=s3cret\n"), Mode: "0600", Owner: "root", Group: "root"}}}
	r, err := n.Write(t.Context(), args)
	if err != nil {
		t.Fatal(err)
	}
	if want := (wire.WriteResult{Snapshot: "20261124T120000Z-aaaa", Written: []string{"/etc/temporal/temporal.env"}}); !reflect.DeepEqual(r, want) {
		t.Errorf("result %+v, want %+v", r, want)
	}
	var sent wire.Write
	if err := json.Unmarshal(got.Args, &sent); err != nil || !reflect.DeepEqual(sent, args) {
		t.Errorf("args = %s (%v), want %+v", got.Args, err, args)
	}
	if got.ApplyID != "20261124T120000Z-aaaa" {
		t.Errorf("request = %+v, want the run's apply id", got)
	}
	if cmds := s.commands(); !slices.Equal(cmds, []string{cmdHelper(sha, wire.OpWrite)}) {
		t.Errorf("commands = %q", cmds)
	}
}
