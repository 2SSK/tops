package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/2SSK/tops/internal/helper"
	"github.com/2SSK/tops/internal/term"
	"github.com/2SSK/tops/internal/wire"
)

// fakeNode writes the files hello reads for a small VPS and returns a Host on them.
func fakeNode(t *testing.T) helper.Host {
	t.Helper()
	root := t.TempDir()
	for name, content := range map[string]string{
		"proc/meminfo":             "MemTotal: 2097152 kB\n",
		"proc/stat":                "cpu  1 2 3\ncpu0 1 2 3\ncpu1 1 2 3\n",
		"proc/1/cgroup":            "0::/init.scope\n",
		"proc/sys/kernel/hostname": "vps-01\n",
		"etc/machine-id":           "99dd89667394438f8f128b862b8bb427\n",
		"etc/os-release":           "ID=ubuntu\nVERSION_ID=\"24.04\"\n",
	} {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return helper.Host{
		Proc:     filepath.Join(root, "proc"),
		Cgroup:   filepath.Join(root, "cgroup"),
		Etc:      filepath.Join(root, "etc"),
		Affinity: func() (int, error) { return 2, nil },
	}
}

// runNode runs tops node with args as the user euid, and returns stdout, stderr and the
// exit code main would use.
func runNode(t *testing.T, euid int, stdin string, args ...string) (string, string, int) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	cmd := NewRootCommand(&Options{
		Getenv:  func(string) string { return "" },
		Geteuid: func() int { return euid },
		Node:    fakeNode(t),
	})
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs(append([]string{"node"}, args...))
	err := cmd.Execute()
	return stdout.String(), stderr.String(), term.ExitCode(err)
}

func TestNodeHello(t *testing.T) {
	stdout, stderr, code := runNode(t, 0, `{"runId":"r-1","args":{}}`, "hello")
	if code != 0 {
		t.Fatalf("exit %d\nstdout %s\nstderr %s", code, stdout, stderr)
	}
	var resp wire.Response
	if err := json.Unmarshal([]byte(stdout), &resp); err != nil {
		t.Fatal(err)
	}
	var hello wire.Hello
	if err := json.Unmarshal(resp.Result, &hello); err != nil {
		t.Fatal(err)
	}
	if hello.Hostname != "vps-01" || hello.HelperVersion != Version || hello.Cores.Value != 2 {
		t.Errorf("hello %+v", hello)
	}
	if !strings.Contains(stderr, `"op":"hello"`) {
		t.Errorf("stderr has no log record for the op:\n%s", stderr)
	}
}

func TestNodeExitCodes(t *testing.T) {
	tests := []struct {
		name  string
		euid  int
		stdin string
		want  int
		code  string // the response's error code
	}{
		{"not root", 1000, `{"runId":"r-1"}`, 2, "helper.not_root"},
		{"bad request", 0, `not json`, 2, "helper.bad_request"},
		{"unknown op", 0, `{"runId":"r-1"}`, 2, "helper.bad_request"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op := "hello"
			if tt.name == "unknown op" {
				op = "reboot"
			}
			stdout, _, code := runNode(t, tt.euid, tt.stdin, op)
			if code != tt.want {
				t.Errorf("exit %d, want %d", code, tt.want)
			}
			var resp wire.Response
			if err := json.Unmarshal([]byte(stdout), &resp); err != nil || resp.Error == nil || resp.Error.Code != tt.code {
				t.Errorf("stdout %q, want error %s", stdout, tt.code)
			}
		})
	}
}

func TestNodeNeedsOneOp(t *testing.T) {
	for _, args := range [][]string{{}, {"hello", "facts"}} {
		if _, _, code := runNode(t, 0, "", args...); code != 4 {
			t.Errorf("tops node %v: exit %d, want 4 (usage)", args, code)
		}
	}
}

func TestNodeIsHidden(t *testing.T) {
	cmd := NewRootCommand(&Options{})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--help"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	// A command is listed as "  <name>  <short>"; "node" alone also appears in other text.
	if regexp.MustCompile(`(?m)^\s+node\s`).MatchString(out.String()) {
		t.Errorf("help lists node:\n%s", out.String())
	}
}
