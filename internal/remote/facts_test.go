package remote

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"

	"github.com/2SSK/tops/internal/wire"
)

// TestNodeFacts: facts runs the helper of this build with the run's request and the wanted
// files as args, and decodes what is on the node.
func TestNodeFacts(t *testing.T) {
	var got wire.Request
	s, cfg := newNode(t, helperReplies(&got, `{"ok":true,"result":{"files":{"/etc/temporal/temporal.env":{"exists":false}},"unknown":[],"current":"","releases":[],"units":{},"applied":null}}`, 0))
	n := &Node{Client: mustDial(t, cfg), SHA256: sha, req: wire.Request{RunID: "r-1", By: "ssk@thinkpad"}}
	args := wire.FactsArgs{Want: map[string]string{"/etc/temporal/temporal.env": "ab12"}, Dirs: []string{"/etc/temporal"}}
	f, err := n.Facts(t.Context(), args)
	if err != nil {
		t.Fatal(err)
	}
	if st, ok := f.Files["/etc/temporal/temporal.env"]; !ok || st.Exists || f.Applied != nil {
		t.Errorf("facts = %+v", f)
	}
	var sent wire.FactsArgs
	if err := json.Unmarshal(got.Args, &sent); err != nil || !reflect.DeepEqual(sent, args) {
		t.Errorf("args = %s (%v), want %+v", got.Args, err, args)
	}
	if got.RunID != "r-1" || got.By != "ssk@thinkpad" {
		t.Errorf("request = %+v, want the run's", got)
	}
	if cmds := s.commands(); !slices.Equal(cmds, []string{cmdHelper(sha, wire.OpFacts)}) {
		t.Errorf("commands = %q", cmds)
	}
}

func TestNodeFactsHelperError(t *testing.T) {
	var got wire.Request
	_, cfg := newNode(t, helperReplies(&got, `{"ok":false,"error":{"code":"helper.bad_request","message":"path \"etc\" is not absolute and clean"}}`, 2))
	n := &Node{Client: mustDial(t, cfg), SHA256: sha}
	if _, err := n.Facts(t.Context(), wire.FactsArgs{}); err == nil {
		t.Error("a helper error came back as facts")
	}
}

// TestConnectKeepsRequest: a node remembers the run's request for its later ops, without the
// args of hello.
func TestConnectKeepsRequest(t *testing.T) {
	e := newConnectEnv(t, "x86_64")
	n, err := e.connect(t)
	if err != nil {
		t.Fatal(err)
	}
	if n.req.RunID == "" || n.req.Args != nil {
		t.Errorf("req = %+v", n.req)
	}
}
