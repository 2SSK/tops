package helper

import (
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/2SSK/tops/internal/wire"
)

// sqlPassword needs escaping in a URL, as the tool's connection URL escapes it.
const sqlPassword = "p@ss/w0rd+x"

// fakeSQLTool is a temporal-sql-tool that behaves like 1.31.3's where schema depends on it:
// global flags, then the command and its flags; the password only from SQL_PASSWORD; its log
// on stderr with the level coloured even off a terminal. On the database "broken" it fails
// update-schema as the real one fails to connect, its error quoting the password both as it is
// and escaped in the connection URL, and exits 1. It logs its arguments, whether
// SQL_PASSWORD was the password, and whether the helper's own environment leaked in.
//
//nolint:gosec // G101: the test password in a fake connection URL
const fakeSQLTool = `#!/bin/sh
log=LOG
pw='PW'
ok=no
[ "$SQL_PASSWORD" = "$pw" ] && ok=yes
echo "$* password=$ok leak=${TOPS_TEST_LEAK-unset}" >> "$log"
db= step=
while [ $# -gt 0 ]; do
	case $1 in
	--database) db=$2; shift 2 ;;
	--endpoint | --port | --user | --plugin | --defaultdb | --version | --schema-name) shift 2 ;;
	--tls) shift ;;
	*) step=$1; shift ;;
	esac
done
printf '2026-11-22T12:00:00.000Z\t\033[34mINFO\033[0m\tUpdateSchemaTask started\n' >&2
if [ "$db/$step" = broken/update-schema ]; then
	printf '2026-11-22T12:00:01.000Z\t\033[31mERROR\033[0m\tUnable to connect to SQL database.\t{"error": "dial postgres://temporal:%s@db:5432/broken: password %s rejected"}\n' 'ENC' "$SQL_PASSWORD" >&2
	exit 1
fi
exit 0
`

// schemaNode is a fake node with 1.31.3's temporal-sql-tool staged as the fake one.
type schemaNode struct {
	host Host
	log  string
}

func newSchemaNode(t *testing.T) *schemaNode {
	t.Helper()
	root := t.TempDir()
	n := &schemaNode{host: Host{Root: root}, log: filepath.Join(t.TempDir(), "log")}
	tool := filepath.Join(root, "opt/temporal/releases/1.31.3/temporal-sql-tool")
	writeFile(t, tool, strings.NewReplacer("log=LOG", "log="+n.log, "PW", sqlPassword, "ENC", url.QueryEscape(sqlPassword)).Replace(fakeSQLTool))
	if err := os.Chmod(tool, 0o755); err != nil { //nolint:gosec // a test binary
		t.Fatal(err)
	}
	t.Setenv("TOPS_TEST_LEAK", "1") // in the helper's environment, never in the tool's
	return n
}

func (n *schemaNode) ran(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile(n.log)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

func schemaArgs(targets ...wire.SchemaTarget) wire.Schema {
	return wire.Schema{Release: "1.31.3", Host: "db.lab", Port: 5432, User: "temporal", Password: sqlPassword, AdminDatabase: "postgres", Targets: targets}
}

var (
	temporalDB   = wire.SchemaTarget{Database: "temporal", SchemaName: "postgresql/v12/temporal", Create: true}
	visibilityDB = wire.SchemaTarget{Database: "temporal_visibility", SchemaName: "postgresql/v12/visibility"}
)

// TestSchemaEnsures: a database the workstation found missing is created first, through the
// admin database; every database is then versioned from 0.0 and migrated with the schema the
// tool embeds. The password reaches the tool only through SQL_PASSWORD, nothing else of the
// helper's environment does, and it is never in argv.
func TestSchemaEnsures(t *testing.T) {
	n := newSchemaNode(t)
	if err := n.host.Schema(t.Context(), schemaArgs(temporalDB, visibilityDB)); err != nil {
		t.Fatal(err)
	}
	const global = "--endpoint db.lab --port 5432 --user temporal --database "
	want := []string{
		global + "temporal --plugin postgres12 create-database --defaultdb postgres password=yes leak=unset",
		global + "temporal --plugin postgres12 setup-schema --version 0.0 password=yes leak=unset",
		global + "temporal --plugin postgres12 update-schema --schema-name postgresql/v12/temporal password=yes leak=unset",
		global + "temporal_visibility --plugin postgres12 setup-schema --version 0.0 password=yes leak=unset",
		global + "temporal_visibility --plugin postgres12 update-schema --schema-name postgresql/v12/visibility password=yes leak=unset",
	}
	if got := n.ran(t); !reflect.DeepEqual(got, want) {
		t.Errorf("ran\n%q\nwant\n%q", got, want)
	}
}

// TestSchemaTLS: the spec's tls reaches the tool as its flag.
func TestSchemaTLS(t *testing.T) {
	n := newSchemaNode(t)
	s := schemaArgs(visibilityDB)
	s.TLS = true
	if err := n.host.Schema(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	for _, line := range n.ran(t) {
		if !strings.Contains(line, " --plugin postgres12 --tls ") {
			t.Errorf("ran %q, want --tls", line)
		}
	}
}

// TestSchemaFails: a step the tool fails is schema.failed naming the step and the database,
// with the tool's error on one line, uncoloured, the password masked as it is and as the URL
// escapes it; nothing runs after it.
func TestSchemaFails(t *testing.T) {
	n := newSchemaNode(t)
	broken := wire.SchemaTarget{Database: "broken", SchemaName: "postgresql/v12/temporal"}
	err := n.host.Schema(t.Context(), schemaArgs(broken, visibilityDB))
	want := &wire.Error{ //nolint:gosec // G101: the password masked in a fake connection URL
		Code:    codeSchemaFailed,
		Message: "update-schema broken failed",
		Detail:  `2026-11-22T12:00:01.000Z ERROR Unable to connect to SQL database. {"error": "dial postgres://temporal:***@db:5432/broken: password *** rejected"}`,
	}
	var we *wire.Error
	if !errors.As(err, &we) || !reflect.DeepEqual(we, want) {
		t.Fatalf("err = %#v\nwant %#v", err, want)
	}
	if ran := n.ran(t); len(ran) != 2 {
		t.Errorf("ran %d steps after the failure, want none:\n%q", len(ran)-2, ran)
	}
}

// TestSchemaFailsWithoutMessage: a tool that fails and says nothing still names its exit.
func TestSchemaFailsWithoutMessage(t *testing.T) {
	n := newSchemaNode(t)
	writeFile(t, filepath.Join(n.host.Root, "opt/temporal/releases/1.31.3/temporal-sql-tool"), "#!/bin/sh\nexit 1\n")
	var we *wire.Error
	if err := n.host.Schema(t.Context(), schemaArgs(visibilityDB)); !errors.As(err, &we) || we.Detail != "exit status 1" {
		t.Errorf("err = %#v, want schema.failed with its exit status", err)
	}
}

// TestSchemaNotStaged: without the release there is no tool to run: release.not_staged.
func TestSchemaNotStaged(t *testing.T) {
	n := newSchemaNode(t)
	s := schemaArgs(visibilityDB)
	s.Release = "1.31.4"
	var we *wire.Error
	if err := n.host.Schema(t.Context(), s); !errors.As(err, &we) || we.Code != codeNotStaged {
		t.Errorf("err = %v, want %s", err, codeNotStaged)
	}
}

// TestSchemaToolWontRun: a staged tool that cannot be executed is an error of the node, not
// schema.failed, which says the database refused.
func TestSchemaToolWontRun(t *testing.T) {
	n := newSchemaNode(t)
	if err := os.Chmod(filepath.Join(n.host.Root, "opt/temporal/releases/1.31.3/temporal-sql-tool"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := n.host.Schema(t.Context(), schemaArgs(visibilityDB))
	var we *wire.Error
	if err == nil || errors.As(err, &we) {
		t.Errorf("err = %v, want a plain error", err)
	}
}

func schemaRequest(t *testing.T, s wire.Schema) string {
	t.Helper()
	args, err := json.Marshal(s) //nolint:gosec // G117: the request carries the password, as the workstation's does
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(wire.Request{RunID: "r-1", By: "ssk@thinkpad", Args: args})
	return string(b)
}

// TestSchemaRefusesUnsafeArgs: whatever reaches the tool's argv is the value it is: a release
// that is a version, a host and port, names that cannot be flags, and schemas the tool embeds.
func TestSchemaRefusesUnsafeArgs(t *testing.T) {
	for name, edit := range map[string]func(*wire.Schema){
		"release":        func(s *wire.Schema) { s.Release = "../1.31.3" },
		"host":           func(s *wire.Schema) { s.Host = "--tls" },
		"port":           func(s *wire.Schema) { s.Port = 0 },
		"user":           func(s *wire.Schema) { s.User = "-u" },
		"admin database": func(s *wire.Schema) { s.AdminDatabase = "" },
		"database":       func(s *wire.Schema) { s.Targets[0].Database = "--overwrite" },
		"database twice": func(s *wire.Schema) { s.Targets = append(s.Targets, s.Targets[0]) },
		"schema name":    func(s *wire.Schema) { s.Targets[0].SchemaName = "../../etc" },
	} {
		t.Run(name, func(t *testing.T) {
			n := newSchemaNode(t)
			s := schemaArgs(temporalDB)
			edit(&s)
			code, resp, _ := run(t, Env{Host: n.host, Root: true}, wire.OpSchema, schemaRequest(t, s))
			if code != ExitBadRequest || resp.Error == nil || resp.Error.Code != codeBadRequest {
				t.Errorf("exit %d, error %+v, want a bad request", code, resp.Error)
			}
			if ran := n.ran(t); ran != nil {
				t.Errorf("the tool ran: %q", ran)
			}
		})
	}
}

// TestRunSchema: through the op, an empty result, and a failure's response and log line
// without the password.
func TestRunSchema(t *testing.T) {
	n := newSchemaNode(t)
	code, resp, _ := run(t, Env{Host: n.host, Root: true}, wire.OpSchema, schemaRequest(t, schemaArgs(temporalDB, visibilityDB)))
	if code != ExitOK || string(resp.Result) != "{}" {
		t.Errorf("exit %d, result %s", code, resp.Result)
	}
	broken := wire.SchemaTarget{Database: "broken", SchemaName: "postgresql/v12/temporal"}
	code, resp, log := run(t, Env{Host: n.host, Root: true}, wire.OpSchema, schemaRequest(t, schemaArgs(broken)))
	if code != ExitNotOK || resp.Error == nil || resp.Error.Code != codeSchemaFailed {
		t.Errorf("exit %d, error %+v", code, resp.Error)
	}
	if b, _ := json.Marshal(resp); strings.Contains(string(b)+log, sqlPassword) || strings.Contains(string(b)+log, url.QueryEscape(sqlPassword)) {
		t.Errorf("the password is in the response or the log:\n%s\n%s", b, log)
	}
}
