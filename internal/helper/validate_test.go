package helper

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/2SSK/tops/internal/wire"
)

// fakeServer is a temporal-server that behaves like 1.31.3 where validate depends on it:
//   - render-config finds production.yaml under --config, fails with "Unable to load
//     configuration: …" on stderr after log lines, exit 1, and on success prints the
//     config with the password on stdout;
//   - validate-dynamic-config lists errors and warnings on stdout and exits 0 either way.
//
// It logs its arguments and what it saw of the environment to LOG. Builtins only: it runs
// with exactly the environment validate gives it, which has no PATH.
const fakeServer = `#!/bin/sh
log=LOG
case "$1 $3 $4 $5" in
"--config --env production render-config")
	echo "render-config $2 GOMEMLIMIT=${GOMEMLIMIT-unset} leak=${TOPS_TEST_LEAK-unset}" >> "$log"
	echo "2026/11/22 12:00:00 Loading config; env=production,zone=,configDir=$2" >&2
	[ -f "$2/production.yaml" ] || { echo "Unable to load configuration: no config files found" >&2; exit 1; }
	while IFS= read -r line; do
		case $line in
		*reject-me*) echo "Unable to load configuration: password $TOPS_PG_PASSWORD is wrong" >&2; exit 1 ;;
		esac
	done < "$2/production.yaml"
	echo "password: $TOPS_PG_PASSWORD"
	exit 0 ;;
esac
case "$1" in
validate-dynamic-config)
	echo "validate-dynamic-config $2 leak=${TOPS_TEST_LEAK-unset}" >> "$log"
	echo "$2"
	while IFS= read -r line; do
		case $line in
		*badKey*) echo "  error: unknown key badKey" ;;
		*oldKey*) echo "  warning: oldKey: deprecated" ;;
		esac
	done < "$2"
	exit 0 ;;
esac
echo "unexpected arguments: $*" >&2
exit 64
`

// validateNode is a fake node with 1.31.3 staged as the fake server; log is what it ran.
type validateNode struct {
	host Host
	log  string
}

func newValidateNode(t *testing.T) *validateNode {
	t.Helper()
	root := t.TempDir()
	n := &validateNode{host: Host{Root: root}, log: filepath.Join(t.TempDir(), "log")}
	bin := filepath.Join(root, "opt/temporal/releases/1.31.3/temporal-server")
	writeFile(t, bin, strings.Replace(fakeServer, "log=LOG", "log="+n.log, 1))
	if err := os.Chmod(bin, 0o755); err != nil { //nolint:gosec // a test binary
		t.Fatal(err)
	}
	t.Setenv("TOPS_TEST_LEAK", "1") // in the helper's environment, never in the server's
	return n
}

func (n *validateNode) ran(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile(n.log)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(strings.ReplaceAll(string(b), n.host.Root, "")), "\n")
}

// assertRunDirGone: /run/tops/<run-id> is removed when validate ends, whatever happened.
func (n *validateNode) assertRunDirGone(t *testing.T) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(n.host.Root, runDir))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("/run/tops holds %v after validate", entries)
	}
}

const pgPassword = "s3cret-pg.pass"

func role(name, config, dynamic string) wire.RoleFiles {
	return wire.RoleFiles{
		Role: name, Config: "# enable-template\npersistence: {}\n" + config, DynamicConfig: dynamic,
		Env:     []string{"TOPS_PG_PASSWORD=" + pgPassword, "GOMEMLIMIT=812MiB", "GOMAXPROCS=2"},
		Secrets: []string{"TOPS_PG_PASSWORD"},
	}
}

// TestValidateAccepts: each role's files go to /run/tops/<run-id>/<role>/, render-config sees
// exactly the role's environment and nothing of the helper's, and the run's directory is gone
// afterwards.
func TestValidateAccepts(t *testing.T) {
	n := newValidateNode(t)
	v := wire.Validate{Version: "1.31.3", Roles: []wire.RoleFiles{role("frontend", "", "frontend.x: 1\n"), role("history", "", "")}}
	r, err := n.host.Validate(t.Context(), "r-1", v)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Rejected) != 0 {
		t.Errorf("rejected %+v", r.Rejected)
	}
	want := []string{
		"render-config /run/tops/r-1/frontend GOMEMLIMIT=812MiB leak=unset",
		"validate-dynamic-config /run/tops/r-1/frontend/dynamicconfig.yaml leak=unset",
		"render-config /run/tops/r-1/history GOMEMLIMIT=812MiB leak=unset",
		"validate-dynamic-config /run/tops/r-1/history/dynamicconfig.yaml leak=unset",
	}
	if got := n.ran(t); !reflect.DeepEqual(got, want) {
		t.Errorf("ran\n%q\nwant\n%q", got, want)
	}
	n.assertRunDirGone(t)
}

// TestValidateRejects: what the binary refuses is a Rejection per role and file, with
// Temporal's own message and the password masked, and nothing is left in /run/tops.
func TestValidateRejects(t *testing.T) {
	n := newValidateNode(t)
	v := wire.Validate{Version: "1.31.3", Roles: []wire.RoleFiles{
		role("frontend", "reject-me: 1\n", ""),
		role("history", "", "badKey: 1\noldKey: 2\n"),
		role("worker", "", ""),
	}}
	r, err := n.host.Validate(t.Context(), "r-1", v)
	if err != nil {
		t.Fatal(err)
	}
	want := []wire.Rejection{
		{Role: "frontend", File: "config", Message: "Unable to load configuration: password *** is wrong"},
		{Role: "history", File: "dynamicConfig", Message: "error: unknown key badKey; warning: oldKey: deprecated"},
	}
	if !reflect.DeepEqual(r.Rejected, want) {
		t.Errorf("rejected\n%+v\nwant\n%+v", r.Rejected, want)
	}
	n.assertRunDirGone(t)
}

// TestValidateFailsWithoutMessage: a binary that fails and says nothing is still a rejection,
// never an accepted file.
func TestValidateFailsWithoutMessage(t *testing.T) {
	n := newValidateNode(t)
	writeFile(t, filepath.Join(n.host.Root, "opt/temporal/releases/1.31.3/temporal-server"), "#!/bin/sh\nexit 3\n")
	r, err := n.host.Validate(t.Context(), "r-1", wire.Validate{Version: "1.31.3", Roles: []wire.RoleFiles{role("frontend", "", "")}})
	if err != nil {
		t.Fatal(err)
	}
	want := []wire.Rejection{
		{Role: "frontend", File: "config", Message: "exit status 3"},
		{Role: "frontend", File: "dynamicConfig", Message: "exit status 3"},
	}
	if !reflect.DeepEqual(r.Rejected, want) {
		t.Errorf("rejected %+v, want %+v", r.Rejected, want)
	}
}

// TestValidateNotStaged: without the release there is nothing to validate with:
// release.not_staged, and no run directory.
func TestValidateNotStaged(t *testing.T) {
	n := newValidateNode(t)
	_, err := n.host.Validate(t.Context(), "r-1", wire.Validate{Version: "1.31.4", Roles: []wire.RoleFiles{role("frontend", "", "")}})
	var we *wire.Error
	if !errors.As(err, &we) || we.Code != codeNotStaged {
		t.Fatalf("err = %v, want %s", err, codeNotStaged)
	}
	n.assertRunDirGone(t)
}

// TestValidateBinaryWontRun: a staged binary that cannot be executed is an error, not a
// rejection of the user's files; the run directory still goes.
func TestValidateBinaryWontRun(t *testing.T) {
	n := newValidateNode(t)
	if err := os.Chmod(filepath.Join(n.host.Root, "opt/temporal/releases/1.31.3/temporal-server"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := n.host.Validate(t.Context(), "r-1", wire.Validate{Version: "1.31.3", Roles: []wire.RoleFiles{role("frontend", "", "")}}); err == nil {
		t.Error("a binary that cannot run validated the files")
	}
	n.assertRunDirGone(t)
}

func validateRequest(t *testing.T, runID string, v wire.Validate) string {
	t.Helper()
	args, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(wire.Request{RunID: runID, By: "ssk@thinkpad", Args: args})
	return string(b)
}

// TestValidateRefusesUnsafeArgs: a run id, version or role that is not a plain name could
// make root write outside /run/tops/<run-id>; a bad env key is not what systemd passes.
func TestValidateRefusesUnsafeArgs(t *testing.T) {
	ok := wire.Validate{Version: "1.31.3", Roles: []wire.RoleFiles{role("frontend", "", "")}}
	twice := wire.Validate{Version: "1.31.3", Roles: []wire.RoleFiles{role("frontend", "", ""), role("frontend", "", "")}}
	badEnv := role("frontend", "", "")
	badEnv.Env = []string{"NOT A KEY=1"}
	for name, c := range map[string]struct {
		runID string
		v     wire.Validate
	}{
		"run id":     {"../../etc", ok},
		"version":    {"r-1", wire.Validate{Version: "../1.31.3", Roles: ok.Roles}},
		"role":       {"r-1", wire.Validate{Version: "1.31.3", Roles: []wire.RoleFiles{role("../etc", "", "")}}},
		"role twice": {"r-1", twice},
		"env":        {"r-1", wire.Validate{Version: "1.31.3", Roles: []wire.RoleFiles{badEnv}}},
	} {
		t.Run(name, func(t *testing.T) {
			n := newValidateNode(t)
			code, resp, _ := run(t, Env{Host: n.host, Root: true}, wire.OpValidate, validateRequest(t, c.runID, c.v))
			if code != ExitBadRequest || resp.Error == nil || resp.Error.Code != codeBadRequest {
				t.Errorf("exit %d, error %+v, want a bad request", code, resp.Error)
			}
			if _, err := os.Stat(n.log); err == nil {
				t.Error("temporal-server ran")
			}
		})
	}
}

func TestRunValidate(t *testing.T) {
	n := newValidateNode(t)
	v := wire.Validate{Version: "1.31.3", Roles: []wire.RoleFiles{role("frontend", "reject-me: 1\n", "")}}
	code, resp, _ := run(t, Env{Host: n.host, Root: true}, wire.OpValidate, validateRequest(t, "r-20261122T120000Z-9c1e", v))
	var r wire.ValidateResult
	if code != ExitOK || json.Unmarshal(resp.Result, &r) != nil || len(r.Rejected) != 1 {
		t.Errorf("exit %d, result %s", code, resp.Result)
	}
	if strings.Contains(string(resp.Result), pgPassword) {
		t.Errorf("the response holds the password: %s", resp.Result)
	}
}
