//go:build helper

// Limits against real PostgreSQL servers in containers. Run with make helper-test.

package postgres

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"maps"
	"net"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/2SSK/tops/internal/secret"
	"github.com/2SSK/tops/internal/sizing"
)

func docker(t *testing.T, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "docker", args...) //nolint:gosec // the test's own docker commands
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("docker %s: %v: %s", strings.Join(args, " "), err, stderr.String())
	}
	return strings.TrimSpace(string(out))
}

// startPostgres runs image with the server flags given and returns a Config whose dialer
// reaches it whatever address pgx asks for: the host name is never resolved on this side.
func startPostgres(t *testing.T, image string, flags ...string) Config {
	t.Helper()
	password := rand.Text()
	args := append([]string{"run", "--detach", "--env", "POSTGRES_USER=temporal", "--env", "POSTGRES_PASSWORD=" + password,
		"--publish", "127.0.0.1::5432", image, "postgres"}, flags...)
	id := docker(t, args...)
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "--force", id).Run() }) //nolint:noctx,gosec // t.Context is done by then
	port := docker(t, "port", id, "5432/tcp")
	return Config{
		Host: "vps-pg", Port: 5432, User: "temporal", Password: secret.New(password), Database: "postgres",
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, port)
		},
	}
}

// limits retries while the server starts: the image's first boot runs a socket-only server,
// then restarts with TCP.
func limits(t *testing.T, c Config) sizing.DBLimits {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for {
		l, err := Limits(t.Context(), c)
		if err == nil {
			return l
		}
		if time.Now().After(deadline) {
			t.Fatalf("Limits: %v", err)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func TestHelperLimits(t *testing.T) {
	c := startPostgres(t, "postgres:17.11", "-c", "max_connections=57", "-c", "reserved_connections=2")
	got := limits(t, c)
	want := sizing.DBLimits{Host: "vps-pg", MaxConnections: 57, SuperuserReserved: 3, Reserved: 2, OtherSessions: 0}
	if got != want {
		t.Fatalf("Limits = %+v, want %+v", got, want)
	}

	// Another user's session counts; Temporal's own (same user as tops) does not.
	cfg, err := connConfig(c)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := pgx.ConnectConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = admin.Close(context.Background()) }()
	if _, err := admin.Exec(t.Context(), "CREATE ROLE other LOGIN PASSWORD 'other'"); err != nil {
		t.Fatal(err)
	}
	other := *cfg
	other.User, other.Password = "other", "other"
	conn, err := pgx.ConnectConfig(t.Context(), &other)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	if got := limits(t, c); got.OtherSessions != 1 {
		t.Errorf("with another user connected, OtherSessions = %d, want 1 (the admin session is ours)", got.OtherSessions)
	}
}

// TestHelperLimitsPostgres15: before PostgreSQL 16 there is no reserved_connections; it reads 0.
func TestHelperLimitsPostgres15(t *testing.T) {
	got := limits(t, startPostgres(t, "postgres:15-alpine"))
	if got.MaxConnections != 100 || got.SuperuserReserved != 3 || got.Reserved != 0 {
		t.Errorf("Limits = %+v, want PostgreSQL 15's defaults 100, 3 and no reserved_connections", got)
	}
}

func TestHelperLimitsWrongPassword(t *testing.T) {
	c := startPostgres(t, "postgres:17.11")
	limits(t, c) // wait until it is up
	c.Password = secret.New("wrong")
	_, err := Limits(t.Context(), c)
	var pgErr interface{ SQLState() string }
	if !errors.As(err, &pgErr) || pgErr.SQLState() != "28P01" {
		t.Fatalf("err = %v, want invalid_password (28P01)", err)
	}
	if strings.Contains(err.Error(), "wrong") {
		t.Errorf("the password is in the error: %v", err)
	}
}

// run runs statements as the server's superuser, on database db.
func run(t *testing.T, c Config, db string, statements ...string) {
	t.Helper()
	c.Database = db
	conn, err := open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	for _, s := range statements {
		if _, err := conn.Exec(t.Context(), s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
}

// metadataTable is cluster_metadata_info as Temporal's postgresql/v12/temporal schema creates it.
const metadataTable = `CREATE TABLE cluster_metadata_info (
	metadata_partition INTEGER NOT NULL,
	cluster_name VARCHAR(255) NOT NULL,
	data BYTEA NOT NULL,
	data_encoding VARCHAR(16) NOT NULL,
	version BIGINT NOT NULL,
	PRIMARY KEY (metadata_partition, cluster_name))`

// read checks the store as c reads it against want.
func read(t *testing.T, c Config, step string, want Store) {
	t.Helper()
	s, err := Read(t.Context(), c)
	if err != nil {
		t.Fatalf("%s: %v", step, err)
	}
	if s.Host != want.Host || s.User != want.User || s.CanCreate != want.CanCreate || !maps.Equal(s.Owners, want.Owners) || !slices.Equal(s.Clusters, want.Clusters) {
		t.Fatalf("%s: %+v, want %+v", step, s, want)
	}
}

// TestHelperStore: what bootstrap reads before it creates anything, as the store fills up.
func TestHelperStore(t *testing.T) {
	c := startPostgres(t, "postgres:17.11")
	limits(t, c) // wait until it is up

	// The image creates "temporal", owned by POSTGRES_USER, a superuser.
	read(t, c, "fresh", Store{Host: "vps-pg", User: "temporal", CanCreate: true, Owners: map[string]string{Persistence: "temporal"}})

	run(t, c, "postgres", "CREATE ROLE other LOGIN PASSWORD 'other' NOCREATEDB", "CREATE DATABASE temporal_visibility OWNER other")
	run(t, c, Persistence, metadataTable)
	both := map[string]string{Persistence: "temporal", Visibility: "other"}
	read(t, c, "schema, no cluster yet", Store{Host: "vps-pg", User: "temporal", CanCreate: true, Owners: both})

	run(t, c, Persistence, "INSERT INTO cluster_metadata_info VALUES (1, 'tops-e2e', '', 'Proto3', 1), (1, 'b-other', '', 'Proto3', 1)")
	read(t, c, "two clusters", Store{Host: "vps-pg", User: "temporal", CanCreate: true, Owners: both, Clusters: []string{"b-other", "tops-e2e"}})

	// A role without CREATEDB that does not own temporal: its clusters are not read.
	other := c
	other.User, other.Password = "other", secret.New("other")
	read(t, other, "another role", Store{Host: "vps-pg", User: "other", Owners: both})
}
