package remote

import (
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/2SSK/tops/internal/wire"
)

// sha is a made-up helper sha256; its first 16 hex digits name the directory.
const sha = "0123456789abcdef" + "0123456789abcdef0123456789abcdef0123456789abcdef"

func TestCmdHelper(t *testing.T) {
	want := "sudo -n /usr/local/lib/tops/0123456789abcdef/tops node facts"
	if got := cmdHelper(sha, wire.OpFacts); got != want {
		t.Errorf("cmdHelper = %q, want %q", got, want)
	}
}

// helperReplies plays an installed helper: it records the request and answers with stdout
// and exit status as given.
func helperReplies(got *wire.Request, stdout string, status uint32) execFunc {
	return func(_ string, stdin io.Reader, out, _ io.Writer) uint32 {
		_ = json.NewDecoder(stdin).Decode(got)
		_, _ = io.WriteString(out, stdout)
		return status
	}
}

func TestCall(t *testing.T) {
	var got wire.Request
	s, cfg := newNode(t, helperReplies(&got, `{"ok":true,"result":{"hostname":"vps-01","sha256":"`+sha+`"}}`, 0))
	req := wire.Request{RunID: "r-1", By: "ssk@thinkpad", Args: json.RawMessage(`{}`)}
	var hello wire.Hello
	if err := mustDial(t, cfg).call(t.Context(), sha, wire.OpHello, req, &hello); err != nil {
		t.Fatal(err)
	}
	if hello.Hostname != "vps-01" || hello.SHA256 != sha {
		t.Errorf("hello = %+v", hello)
	}
	if got.RunID != "r-1" || got.By != "ssk@thinkpad" || string(got.Args) != "{}" {
		t.Errorf("the helper read %+v on stdin, want the request", got)
	}
	if cmds := s.commands(); !slices.Equal(cmds, []string{cmdHelper(sha, wire.OpHello)}) {
		t.Errorf("commands = %q", cmds)
	}
}

func TestCallHelperError(t *testing.T) {
	var got wire.Request
	_, cfg := newNode(t, helperReplies(&got, `{"ok":false,"error":{"code":"helper.bad_request","message":"unknown field","detail":"json: unknown field \"x\""}}`, 2))
	err := mustDial(t, cfg).call(t.Context(), sha, wire.OpFacts, wire.Request{RunID: "r-1"}, &wire.Facts{})
	ue := userError(t, err, "helper.bad_request")
	if !strings.Contains(ue.What, "facts on vps-01") || !strings.Contains(ue.What, "unknown field") {
		t.Errorf("What = %q, want the op, the node and the message", ue.What)
	}
	if !strings.Contains(ue.Why, `unknown field "x"`) {
		t.Errorf("Why = %q, want the detail", ue.Why)
	}
}

func TestCallNotOKWithoutError(t *testing.T) {
	var got wire.Request
	_, cfg := newNode(t, helperReplies(&got, `{"ok":false}`, 1))
	err := mustDial(t, cfg).call(t.Context(), sha, wire.OpHello, wire.Request{}, &wire.Hello{})
	if ue := userError(t, err, "helper.failed"); !strings.Contains(ue.What, "hello on vps-01") {
		t.Errorf("What = %q, want the op and the node", ue.What)
	}
}

func TestCallNoHelper(t *testing.T) {
	_, cfg := newNode(t, func(cmd string, _ io.Reader, _, stderr io.Writer) uint32 {
		_, _ = io.WriteString(stderr, "sudo: "+strings.Fields(cmd)[2]+": command not found\n") // what sudo prints on Ubuntu 24.04
		return 1
	})
	err := mustDial(t, cfg).call(t.Context(), sha, wire.OpHello, wire.Request{}, &wire.Hello{})
	if !errors.Is(err, errNoHelper) {
		t.Fatalf("err = %v, want errNoHelper", err)
	}
}

func TestCallBrokenHelper(t *testing.T) {
	tests := []struct {
		name, stdout, stderr string
		status               uint32
		want                 string
	}{
		{"crashed", "", "panic: runtime error\n", 2, "(exit 2): panic: runtime error"},
		{"garbage", "not json", "", 0, "no response"},
		{"wrong result", `{"ok":true,"result":[1,2]}`, "", 0, "decode the hello result from vps-01"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, cfg := newNode(t, func(_ string, stdin io.Reader, stdout, stderr io.Writer) uint32 {
				_, _ = io.Copy(io.Discard, stdin) // the helper reads its request first
				_, _ = io.WriteString(stdout, tt.stdout)
				_, _ = io.WriteString(stderr, tt.stderr)
				return tt.status
			})
			err := mustDial(t, cfg).call(t.Context(), sha, wire.OpHello, wire.Request{}, &wire.Hello{})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want %q in it", err, tt.want)
			}
			if errors.Is(err, errNoHelper) {
				t.Fatal("a broken helper must not be mistaken for a missing one: tops would upload over it")
			}
		})
	}
}

func TestCallConnectionLost(t *testing.T) {
	_, cfg := newNode(t, echo)
	c := mustDial(t, cfg)
	_ = c.Close()
	err := c.call(t.Context(), sha, wire.OpHello, wire.Request{}, &wire.Hello{})
	if err == nil || strings.Contains(err.Error(), "no response") {
		t.Fatalf("err = %v, want the session error itself", err)
	}
}
