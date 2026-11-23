package remote

import (
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/2SSK/tops/internal/term"
	"github.com/2SSK/tops/internal/wire"
)

// TestNodeSchema: schema runs the helper of this build with the run's request and the
// databases as args, the password among them on stdin, never in the command.
func TestNodeSchema(t *testing.T) {
	var got wire.Request
	s, cfg := newNode(t, helperReplies(&got, `{"ok":true,"result":{}}`, 0))
	n := &Node{Client: mustDial(t, cfg), SHA256: sha, req: wire.Request{RunID: "r-1", By: "ssk@thinkpad"}}
	args := wire.Schema{Release: "1.31.3", Host: "10.0.0.5", Port: 5432, User: "temporal", Password: "s3cret", AdminDatabase: "postgres",
		Targets: []wire.SchemaTarget{{Database: "temporal", SchemaName: "postgresql/v12/temporal", Create: true}}}
	if err := n.Schema(t.Context(), args); err != nil {
		t.Fatal(err)
	}
	var sent wire.Schema
	if err := json.Unmarshal(got.Args, &sent); err != nil || !reflect.DeepEqual(sent, args) {
		t.Errorf("args = %v (%v), want %+v", sent, err, args)
	}
	if got.RunID != "r-1" {
		t.Errorf("request = %+v, want the run's", got)
	}
	if cmds := s.commands(); !slices.Equal(cmds, []string{cmdHelper(sha, wire.OpSchema)}) {
		t.Errorf("commands = %q", cmds)
	}
}

// TestNodeSchemaFails: the tool's failure reaches the caller as the helper's schema.failed.
func TestNodeSchemaFails(t *testing.T) {
	var got wire.Request
	_, cfg := newNode(t, helperReplies(&got, `{"ok":false,"error":{"code":"schema.failed","message":"update-schema temporal failed","detail":"password authentication failed"}}`, 1))
	n := &Node{Client: mustDial(t, cfg), SHA256: sha, req: wire.Request{RunID: "r-1", By: "ssk@thinkpad"}}
	err := n.Schema(t.Context(), wire.Schema{Release: "1.31.3"})
	var ue *term.UserError
	if !errors.As(err, &ue) || ue.Code != "schema.failed" {
		t.Errorf("err = %#v, want schema.failed", err)
	}
}
