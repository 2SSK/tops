package helper

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/2SSK/tops/internal/wire"
)

// run runs op as root on a fake node with stdin, and returns the exit code, the decoded
// response, and the log written to stderr.
func run(t *testing.T, env Env, op wire.Op, stdin string) (int, wire.Response, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	env.Stdin = strings.NewReader(stdin)
	env.Stdout = &stdout
	env.Log = slog.New(slog.NewJSONHandler(&stderr, nil))

	code := Run(op, env)

	var resp wire.Response
	dec := json.NewDecoder(&stdout)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&resp); err != nil {
		t.Fatalf("stdout is not one response: %v\n%s", err, stdout.String())
	}
	if dec.More() {
		t.Errorf("stdout has more than one document")
	}
	return code, resp, stderr.String()
}

// rootEnv is a root helper of version v0.2.0 on the fake node of hello_test.go.
func rootEnv(t *testing.T, files map[string]string) Env {
	return Env{Host: withAffinity(fakeHost(t, files), 12), Root: true, Version: "v0.2.0"}
}

func TestRunHello(t *testing.T) {
	env := rootEnv(t, node())
	code, resp, log := run(t, env, wire.OpHello, `{"runId":"r-1","by":"ssk@thinkpad","args":{}}`)
	if code != ExitOK || !resp.OK || resp.Error != nil {
		t.Fatalf("exit %d, response %+v", code, resp)
	}

	// The result is exactly what Host.Hello returns for this node.
	var got wire.Hello
	if err := json.Unmarshal(resp.Result, &got); err != nil {
		t.Fatal(err)
	}
	if want, _ := env.Host.Hello(env.Version); got != want {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}

	// One JSON record on stderr, naming the op and the run.
	rec := record(t, log)
	if got := fmt.Sprint(rec["op"], " ", rec["run_id"], " ", rec["result"]); got != "hello r-1 ok" {
		t.Errorf("log record %v", rec)
	}
}

// record decodes stderr as exactly one JSON log record.
func record(t *testing.T, log string) map[string]any {
	t.Helper()
	var rec map[string]any
	if err := json.Unmarshal([]byte(log), &rec); err != nil {
		t.Fatalf("stderr is not one JSON record: %v\n%s", err, log)
	}
	return rec
}

func TestRunHelloWithoutArgs(t *testing.T) {
	for _, stdin := range []string{`{"runId":"r-1"}`, `{"runId":"r-1","args":null}`} {
		if code, resp, _ := run(t, rootEnv(t, node()), wire.OpHello, stdin); code != ExitOK || !resp.OK {
			t.Errorf("%s: exit %d, response %+v", stdin, code, resp)
		}
	}
}

func TestRunRefuses(t *testing.T) {
	tests := []struct {
		name  string
		root  bool
		op    wire.Op
		stdin string
		code  string
	}{
		{"not root", false, wire.OpHello, `{"runId":"r-1","args":{}}`, "helper.not_root"},
		{"stdin is not JSON", true, wire.OpHello, `hello`, "helper.bad_request"},
		{"stdin is empty", true, wire.OpHello, ``, "helper.bad_request"},
		{"unknown request field", true, wire.OpHello, `{"runId":"r-1","verbose":true}`, "helper.bad_request"},
		{"unknown args field", true, wire.OpHello, `{"runId":"r-1","args":{"verbose":true}}`, "helper.bad_request"},
		{"args not an object", true, wire.OpHello, `{"runId":"r-1","args":[1]}`, "helper.bad_request"},
		{"unknown op", true, "reboot", `{"runId":"r-1","args":{}}`, "helper.bad_request"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := rootEnv(t, node())
			env.Root = tt.root
			code, resp, log := run(t, env, tt.op, tt.stdin)
			if code != ExitBadRequest {
				t.Errorf("exit %d, want %d", code, ExitBadRequest)
			}
			if resp.OK || resp.Error == nil || resp.Error.Code != tt.code || resp.Result != nil {
				t.Errorf("response %+v, want error %s", resp, tt.code)
			}
			if !strings.Contains(log, tt.code) {
				t.Errorf("log does not name %s:\n%s", tt.code, log)
			}
		})
	}
}

func TestRunFails(t *testing.T) {
	// A node without /proc/meminfo: the request is fine, the op is not.
	code, resp, log := run(t, rootEnv(t, change("proc/meminfo", "")), wire.OpHello, `{"runId":"r-1","args":{}}`)
	if code != ExitNotOK {
		t.Errorf("exit %d, want %d", code, ExitNotOK)
	}
	if resp.OK || resp.Error == nil || resp.Error.Code != "helper.failed" || !strings.Contains(resp.Error.Message, "meminfo") {
		t.Errorf("response %+v", resp.Error)
	}
	if !strings.Contains(log, `"run_id":"r-1"`) {
		t.Errorf("log does not name the run:\n%s", log)
	}
}
