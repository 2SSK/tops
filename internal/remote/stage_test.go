package remote

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/2SSK/tops/internal/wire"
)

// TestNodeStage: stage runs the helper of this build with the run's request and the releases
// as args, and returns what the helper changed.
func TestNodeStage(t *testing.T) {
	var got wire.Request
	s, cfg := newNode(t, helperReplies(&got, `{"ok":true,"result":{"changed":["user temporal","/opt/temporal/releases/1.31.3"]}}`, 0))
	n := &Node{Client: mustDial(t, cfg), SHA256: sha, req: wire.Request{RunID: "r-1", By: "ssk@thinkpad"}}
	args := wire.Stage{User: true, Releases: []wire.Download{{
		Kind: "server", Version: "1.31.3", URL: "https://example.test/temporal_1.31.3_linux_amd64.tar.gz", SHA256: sha,
		Extract: []string{"temporal-server", "temporal-sql-tool", "tdbg"},
	}}}
	r, err := n.Stage(t.Context(), args)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"user temporal", "/opt/temporal/releases/1.31.3"}; !slices.Equal(r.Changed, want) {
		t.Errorf("changed = %q, want %q", r.Changed, want)
	}
	var sent wire.Stage
	if err := json.Unmarshal(got.Args, &sent); err != nil || !reflect.DeepEqual(sent, args) {
		t.Errorf("args = %s (%v), want %+v", got.Args, err, args)
	}
	if got.RunID != "r-1" || got.By != "ssk@thinkpad" {
		t.Errorf("request = %+v, want the run's", got)
	}
	if cmds := s.commands(); !slices.Equal(cmds, []string{cmdHelper(sha, wire.OpStage)}) {
		t.Errorf("commands = %q", cmds)
	}
}

// TestNodeStageChecksum: the helper's error code reaches the user as it is.
func TestNodeStageChecksum(t *testing.T) {
	var got wire.Request
	_, cfg := newNode(t, helperReplies(&got, `{"ok":false,"error":{"code":"release.checksum","message":"https://example.test/a.tar.gz does not match checksums.txt","detail":"sha256 00, checksums.txt says 11"}}`, 1))
	n := &Node{Client: mustDial(t, cfg), SHA256: sha}
	_, err := n.Stage(t.Context(), wire.Stage{})
	if ue := userError(t, err, "release.checksum"); ue.What != "stage on vps-01: https://example.test/a.tar.gz does not match checksums.txt" {
		t.Errorf("What = %q", ue.What)
	}
}

// TestTimeout: the workstation waits a little longer than the helper lets an op run, so a
// helper out of time still answers.
func TestTimeout(t *testing.T) {
	if got := timeout(wire.OpStage); got != 10*time.Minute+15*time.Second {
		t.Errorf("timeout(stage) = %v, want the helper's 10 min and 15 s", got)
	}
}
