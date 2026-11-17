package postgres

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/2SSK/tops/internal/secret"
)

func lab() Config {
	return Config{Host: "10.80.0.20", Port: 5432, User: "temporal", Password: secret.New("hunter2"), Database: "postgres"}
}

func TestConnConfig(t *testing.T) {
	// The libpq variables a workstation may have set for psql: none may leak into tops' connection.
	for k, v := range map[string]string{"PGHOST": "prod-db", "PGPORT": "6543", "PGUSER": "admin", "PGPASSWORD": "other",
		"PGDATABASE": "prod", "PGSSLMODE": "prefer"} {
		t.Setenv(k, v)
	}
	cfg, err := connConfig(lab())
	if err != nil {
		t.Fatal(err)
	}
	type conn struct {
		host           string
		port           uint16
		user, password string
		database       string
	}
	if got, want := (conn{cfg.Host, cfg.Port, cfg.User, cfg.Password, cfg.Database}), (conn{"10.80.0.20", 5432, "temporal", "hunter2", "postgres"}); got != want {
		t.Errorf("config = %+v, want %+v from the spec alone", got, want)
	}
	if cfg.TLSConfig != nil || len(cfg.Fallbacks) != 0 {
		t.Errorf("tls: false gave TLS %v and %d fallbacks, want plain and none", cfg.TLSConfig, len(cfg.Fallbacks))
	}
	if cfg.RuntimeParams["application_name"] != "tops" {
		t.Errorf("application_name = %q, want tops: other sessions see who we are", cfg.RuntimeParams["application_name"])
	}
}

// TestConnConfigTunnel: every connection goes through the given dialer, and names are left for
// the node to resolve, so the workstation never needs to reach or resolve the store.
func TestConnConfigTunnel(t *testing.T) {
	dialed := false
	c := lab()
	c.Dial = func(context.Context, string, string) (net.Conn, error) { dialed = true; return nil, errors.New("x") }
	cfg, err := connConfig(c)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = cfg.DialFunc(t.Context(), "tcp", "10.80.0.20:5432")
	if !dialed {
		t.Error("the connection does not use the given dialer")
	}
	if addrs, err := cfg.LookupFunc(t.Context(), "vps-pg"); err != nil || len(addrs) != 1 || addrs[0] != "vps-pg" {
		t.Errorf("lookup = %v, %v; want the name passed through for the node to resolve", addrs, err)
	}
}

func TestConnConfigTLS(t *testing.T) {
	c := lab()
	c.Host, c.TLS = "db.example.com", true
	cfg, err := connConfig(c)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TLSConfig == nil || cfg.TLSConfig.ServerName != "db.example.com" || cfg.TLSConfig.InsecureSkipVerify {
		t.Errorf("tls = %+v, want verified against db.example.com", cfg.TLSConfig)
	}
	if len(cfg.Fallbacks) != 0 {
		t.Errorf("%d fallbacks: a TLS failure must not retry in plain text", len(cfg.Fallbacks))
	}
}

func TestLimitsUnreachable(t *testing.T) {
	c := lab()
	c.Dial = func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("cannot reach 10.80.0.20:5432 on vps-01")
	}
	_, err := Limits(t.Context(), c)
	if err == nil || !strings.Contains(err.Error(), "connect to 10.80.0.20:5432 as temporal") ||
		!strings.Contains(err.Error(), "cannot reach 10.80.0.20:5432 on vps-01") {
		t.Fatalf("err = %v, want the store, the user and the tunnel's error", err)
	}
	if strings.Contains(err.Error(), "hunter2") {
		t.Errorf("the password is in the error: %v", err)
	}
}
