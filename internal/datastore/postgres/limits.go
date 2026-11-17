// Package postgres reads what tops needs from the cluster's PostgreSQL: today, its connection
// limits for the sizing budget. Connections go through a dialer the caller gives, which is an
// SSH tunnel through a node: the database is never reached from the workstation directly.
package postgres

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"strconv"

	"github.com/2SSK/tops/internal/secret"
	"github.com/2SSK/tops/internal/sizing"
	"github.com/jackc/pgx/v5"
)

// Config is how to reach the store: the spec's persistence.postgres, the password from
// secrets.env, and the dialer that opens the tunnel.
type Config struct {
	Host     string
	Port     int
	User     string
	Password secret.Value
	Database string // the spec's adminDatabase
	TLS      bool
	Dial     func(ctx context.Context, network, addr string) (net.Conn, error)
}

// limitsQuery reads the four numbers in one round trip.reserved_connections exists from
// PostgreSQL 16; current_setting(..., true) returns NULL before that instead of failing.
// Other users' sessions are client backends of any other role: Temporal's own sessions are
// what the pools replace, so they are not counted twice.
const limitsQuery = `SELECT
	current_setting('max_connections')::int,
	current_setting('superuser_reserved_connections')::int,
	coalesce(current_setting('reserved_connections',true), '0')::int,
	(SELECT count(*) FROM pg_stat_activity
	WHERE backend_type = 'client backend' AND usename IS DISTINCT FROM current_user)::int`

// Limits connects once, reads the server's connection limits, and loses.
func Limits(ctx context.Context, c Config) (sizing.DBLimits, error) {
	cfg, err := connConfig(c)
	if err != nil {
		return sizing.DBLimits{}, err
	}
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return sizing.DBLimits{}, fmt.Errorf("connect to %s as %s: %w", net.JoinHostPort(c.Host, strconv.Itoa(c.Port)), c.User, err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()

	l := sizing.DBLimits{Host: c.Host}
	if err := conn.QueryRow(ctx, limitsQuery).Scan(&l.MaxConnections, &l.SuperuserReserved, &l.Reserved, &l.OtherSessions); err != nil {
		return sizing.DBLimits{}, fmt.Errorf("read the connection limits of %s: %w", c.Host, err)
	}
	return l, nil
}

// connConfig builds the connection from the spec alone. pgx.ParseConfig("") also reads the
// libpq PG* environment variables; every field they could set is overwritten here, so a
// stray PGHOST or PGSSLMODE on the workstation changes nothing.
func connConfig(c Config) (*pgx.ConnConfig, error) {
	cfg, err := pgx.ParseConfig("")
	if err != nil {
		return nil, fmt.Errorf("postgres config: %w", err)
	}
	cfg.Host = c.Host
	cfg.Port = uint16(c.Port) //nolint:gosec	// the spec checks 1-65535
	cfg.User = c.User
	cfg.Password = c.Password.Reveal()
	cfg.Database = c.Database
	cfg.TLSConfig = nil
	if c.TLS {
		cfg.TLSConfig = &tls.Config{ServerName: c.Host, MinVersion: tls.VersionTLS12}
	}
	cfg.Fallbacks = nil // no silent retry without TLS
	cfg.DialFunc = c.Dial
	cfg.LookupFunc = func(_ context.Context, host string) ([]string, error) {
		return []string{host}, nil // the node resolves the name, through the tunnel
	}
	cfg.RuntimeParams = map[string]string{"application_name": "tops"}
	return cfg, nil
}
