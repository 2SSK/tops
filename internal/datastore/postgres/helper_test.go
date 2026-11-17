//go:build helper

// Limits against real PostgreSQL servers in containers. Run with make helper-test.

package postgres

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"net"
	"os/exec"
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
