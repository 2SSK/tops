//go:build helper

package remote

import (
	"crypto/rand"
	"errors"
	"net/url"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/2SSK/tops/internal/term"
	"github.com/2SSK/tops/internal/wire"
)

// postgresImage is the PostgreSQL the schema is migrated on, as the limits tests use.
const postgresImage = "postgres:17.11"

// schemaLab is a staged node and a PostgreSQL it reaches as "pg" on a network of their own,
// as a node reaches the fleet's database. The superuser is postgres; password is the
// superuser's, with characters a connection URL must escape.
type schemaLab struct {
	node     *Node
	pg       string // the PostgreSQL container
	password string
}

func newSchemaLab(t *testing.T) *schemaLab {
	t.Helper()
	network := docker(t, nil, "network", "create", "tops-schema-"+strings.ToLower(rand.Text()[:8]))
	t.Cleanup(func() { _ = exec.Command("docker", "network", "rm", network).Run() }) //nolint:noctx,gosec // t.Context is done by then
	l := &schemaLab{password: rand.Text() + "@/+:"}
	l.pg = docker(t, nil, "run", "--detach", "--network", network, "--network-alias", "pg",
		"--env", "POSTGRES_PASSWORD="+l.password, postgresImage)
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "--force", l.pg).Run() }) //nolint:noctx,gosec // t.Context is done by then
	c, n := stagedNode(t)
	docker(t, nil, "network", "connect", network, c.id)
	l.node = n
	l.waitForPostgres(t)
	return l
}

// waitForPostgres waits for TCP connections: the image's first boot runs a socket-only
// server, then restarts.
func (l *schemaLab) waitForPostgres(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for exec.Command("docker", "exec", l.pg, "pg_isready", "--host", "127.0.0.1", "--username", "postgres").Run() != nil { //nolint:noctx,gosec // the test's own container
		if time.Now().After(deadline) {
			t.Fatal("PostgreSQL did not accept TCP connections in 60 s")
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// sql runs a query as the superuser in the PostgreSQL container, through its socket.
func (l *schemaLab) sql(t *testing.T, database, query string) string {
	t.Helper()
	return docker(t, nil, "exec", l.pg, "psql", "--username", "postgres", "--dbname", database, "--tuples-only", "--no-align", "--command", query)
}

func (l *schemaLab) schema(user, password string, create bool) wire.Schema {
	return wire.Schema{Release: temporalVersion, Host: "pg", Port: 5432, User: user, Password: password, AdminDatabase: "postgres", Targets: []wire.SchemaTarget{
		{Database: "temporal", SchemaName: "postgresql/v12/temporal", Create: create},
		{Database: "temporal_visibility", SchemaName: "postgresql/v12/visibility", Create: create},
	}}
}

// versions is each database's schema version and how many updates its history records.
func (l *schemaLab) versions(t *testing.T) string {
	t.Helper()
	var v []string
	for _, db := range []string{"temporal", "temporal_visibility"} {
		v = append(v, db+" "+l.sql(t, db, "SELECT curr_version FROM schema_version")+" "+l.sql(t, db, "SELECT count(*) FROM schema_update_history"))
	}
	return strings.Join(v, ", ")
}

// TestHelperSchema is T3.3's done-when: on an empty server, schema creates both databases and
// migrates them to the versions 1.31.3 embeds (docs/backend-schema.md); a second run is a
// no-op, down to the update history.
func TestHelperSchema(t *testing.T) {
	l := newSchemaLab(t)
	if err := l.node.Schema(t.Context(), l.schema("postgres", l.password, true)); err != nil {
		t.Fatal(err)
	}
	first := l.versions(t)
	if !strings.HasPrefix(first, "temporal 1.19 ") || !strings.Contains(first, ", temporal_visibility 1.14 ") {
		t.Fatalf("versions %s, want temporal 1.19 and temporal_visibility 1.14", first)
	}
	if err := l.node.Schema(t.Context(), l.schema("postgres", l.password, false)); err != nil {
		t.Fatal(err)
	}
	if again := l.versions(t); again != first {
		t.Errorf("a second run changed the databases: %s, then %s", first, again)
	}
}

// TestHelperSchemaPrecreated: a user without CREATEDB that owns both databases, as the
// prerequisites allow, gets them versioned and migrated: nothing asks for CREATE DATABASE.
func TestHelperSchemaPrecreated(t *testing.T) {
	l := newSchemaLab(t)
	l.sql(t, "postgres", "CREATE ROLE temporal LOGIN NOCREATEDB PASSWORD 'temporal-owner'")
	l.sql(t, "postgres", "CREATE DATABASE temporal OWNER temporal")
	l.sql(t, "postgres", "CREATE DATABASE temporal_visibility OWNER temporal")
	if err := l.node.Schema(t.Context(), l.schema("temporal", "temporal-owner", false)); err != nil {
		t.Fatal(err)
	}
	if got := l.versions(t); !strings.HasPrefix(got, "temporal 1.19 ") || !strings.Contains(got, ", temporal_visibility 1.14 ") {
		t.Errorf("versions %s, want temporal 1.19 and temporal_visibility 1.14", got)
	}
}

// TestHelperSchemaWrongPassword: the tool's failure is schema.failed with its own error, and
// neither the password sent nor its URL-escaped form is in it.
func TestHelperSchemaWrongPassword(t *testing.T) {
	l := newSchemaLab(t)
	wrong := "wrong-" + l.password
	err := l.node.Schema(t.Context(), l.schema("postgres", wrong, true))
	var ue *term.UserError
	if !errors.As(err, &ue) || ue.Code != "schema.failed" {
		t.Fatalf("err = %v, want schema.failed", err)
	}
	t.Logf("schema.failed: %s: %s", err, ue.Why)
	msg := err.Error() + " " + ue.Why
	for _, leak := range []string{wrong, url.QueryEscape(wrong), l.password, url.QueryEscape(l.password)} {
		if strings.Contains(msg, leak) {
			t.Errorf("the error shows a password: %s", msg)
		}
	}
	if !strings.Contains(msg, "password authentication failed") {
		t.Errorf("err = %s, want PostgreSQL's reason", msg)
	}
}
