package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Persistence and Visisbility are the datbases Temporal runs on, as server.yaml names them.
const (
	Persistence = "temporal"
	Visibility  = "temporal_visibility"
)

// Store is the store as bootstrap finds it, before it creates anything there.
type Store struct {
	Host      string
	User      string            // the spec's: tops reads as it, the schema tool and Temporal run as it
	CanCreate bool              // User may create databases: CREATEDB, or superuser
	Owners    map[string]string // Persistence and Visibility, those that exist: database -> owning role
	Clusters  []string          // the clusters cluster_metadata_info names, sorted; none until Temporal first starts
}

const (
	createQuery  = `SELECT rolcreatedb OR rolsuper FROM pg_roles WHERE rolname = current_user`
	ownersQuery  = `SELECT datname, pg_get_userbyid(datdba) FROM pg_database WHERE datname = ANY($1)`
	tableQuery   = `SELECT to_regclass('cluster_metadata_info') IS NOT NULL`
	clusterQuery = `SELECT cluster_name FROM cluster_metadata_info ORDER BY cluster_name`
)

// Read reads, through the admin database, whether the user may create databases and who
// owns Temporal's; then, when the user owns Persistence and its schema is set up, the
// clusters it holds.
func Read(ctx context.Context, c Config) (Store, error) {
	s := Store{Host: c.Host, User: c.User, Owners: map[string]string{}}
	conn, err := open(ctx, c)
	if err != nil {
		return Store{}, err
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	if err := conn.QueryRow(ctx, createQuery).Scan(&s.CanCreate); err != nil {
		return Store{}, fmt.Errorf("read the privileges of %s on %s: %w", c.User, c.Host, err)
	}
	rows, err := conn.Query(ctx, ownersQuery, []string{Persistence, Visibility})
	if err != nil {
		return Store{}, fmt.Errorf("read the databases of %s: %w", c.Host, err)
	}
	var name, owner string
	if _, err := pgx.ForEachRow(rows, []any{&name, &owner}, func() error { s.Owners[name] = owner; return nil }); err != nil {
		return Store{}, fmt.Errorf("read the databases of %s: %w", c.Host, err)
	}
	if s.Owners[Persistence] != c.User {
		return s, nil // missing, or another role's: bootstrap refuses it before its clusters matter
	}
	if s.Clusters, err = clusters(ctx, c); err != nil {
		return Store{}, err
	}
	return s, nil
}

// clusters is the cluster Persistence records: none while its schema is not set up.
func clusters(ctx context.Context, c Config) ([]string, error) {
	c.Database = Persistence
	conn, err := open(ctx, c)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	var exists bool
	if err := conn.QueryRow(ctx, tableQuery).Scan(&exists); err != nil {
		return nil, fmt.Errorf("look for cluster_metadata_info on %s: %w", c.Host, err)
	}
	if !exists {
		return nil, nil
	}
	rows, err := conn.Query(ctx, clusterQuery)
	if err != nil {
		return nil, fmt.Errorf("read cluster_metadata_info on %s: %w", c.Host, err)
	}
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("read cluster_metadata_info on %s: %w", c.Host, err)
	}
	return names, nil
}
