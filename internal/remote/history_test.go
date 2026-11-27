package remote

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/2SSK/tops/internal/wire"
)

// TestNodeAppendHistory: append runs the helper of this build with the record, under the
// run's apply id.
func TestNodeAppendHistory(t *testing.T) {
	var got wire.Request
	s, cfg := newNode(t, helperReplies(&got, `{"ok":true,"result":{}}`, 0))
	n := &Node{Client: mustDial(t, cfg), SHA256: sha, req: wire.Request{RunID: "r-1", ApplyID: "20261126T100000Z-aaaa", By: "ssk@thinkpad"}}
	if err := n.AppendHistory(t.Context(), wire.History{Command: "apply", Result: wire.ResultOK}); err != nil {
		t.Fatal(err)
	}
	var a wire.HistoryArgs
	if err := json.Unmarshal(got.Args, &a); err != nil || got.ApplyID != "20261126T100000Z-aaaa" || a.Action != wire.HistoryAppend || a.Record == nil || a.Record.Command != "apply" || a.Limit != 0 {
		t.Errorf("request = %+v, args %s", got, got.Args)
	}
	if cmds := s.commands(); !slices.Equal(cmds, []string{cmdHelper(sha, wire.OpHistory)}) {
		t.Errorf("commands = %q", cmds)
	}
}

// TestNodeListHistory: list asks for up to limit records and returns them as the helper
// listed them.
func TestNodeListHistory(t *testing.T) {
	var got wire.Request
	_, cfg := newNode(t, helperReplies(&got, `{"ok":true,"result":{"records":[{"applyId":"20261126T100000Z-aaaa","runId":"r-1","result":"ok","snapshot":true}]}}`, 0))
	n := &Node{Client: mustDial(t, cfg), SHA256: sha, req: wire.Request{RunID: "r-2", By: "ssk@thinkpad"}}
	records, err := n.ListHistory(t.Context(), 5)
	if err != nil || len(records) != 1 || records[0].ApplyID != "20261126T100000Z-aaaa" || !records[0].Snapshot {
		t.Errorf("list = %+v, %v", records, err)
	}
	if string(got.Args) != `{"action":"list","limit":5}` {
		t.Errorf("args %s", got.Args)
	}
}
