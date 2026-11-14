package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"time"

	"github.com/2SSK/tops/internal/fleet"
	"github.com/2SSK/tops/internal/remote"
	"github.com/2SSK/tops/internal/wire"
)

// connected is every node of an environment, connected with its helper in place.
type connected struct {
	dialer *remote.Dialer
	nodes  []*remote.Node // in spec order
}

// Close closes every node's connection, then the dialer's agent connection.
func (c *connected) Close() {
	for _, n := range c.nodes {
		_ = n.Close()
	}
	_ = c.dialer.Close()
}

// connect reaches every node of env with the spec's ssh settings (TRD "Connect"). Close the
// result when the command is done.
func connect(ctx context.Context, opts *Options, env *fleet.Env) (*connected, error) {
	s := env.Cluster.SSH
	d, err := remote.NewDialer(ctx, remote.Config{
		User:       s.User,
		Port:       s.Port,
		Key:        s.Key,
		KnownHosts: s.KnownHosts,
		AuthSock:   opts.Getenv("SSH_AUTH_SOCK"),
		Passphrase: opts.Passphrase,
	})
	if err != nil {
		return nil, err
	}
	targets := make([]remote.Target, len(env.Cluster.Nodes))
	for i, n := range env.Cluster.Nodes {
		targets[i] = remote.Target{Name: n.Name, Address: n.Address}
	}
	nodes, err := d.ConnectAll(ctx, targets, newRequest(opts, time.Now()))
	if err != nil {
		_ = d.Close()
		return nil, err
	}
	return &connected{dialer: d, nodes: nodes}, nil
}

// newRequest starts the envelope every helper call of this run carries: the run id, and who
// ran it, for the node's logs and history.
func newRequest(opts *Options, now time.Time) wire.Request {
	host, err := os.Hostname()
	if err != nil {
		host = "unknown"
	}
	return wire.Request{RunID: newRunID(now), By: opts.Getenv("USER") + "@" + host}
}

// newRunID names a run "r-20261023T153012Z-9c1e": its UTC start, then 4 random hex digits so
// two runs in the same second differ.
func newRunID(now time.Time) string {
	var b [2]byte
	_, _ = rand.Read(b[:]) // crypto/rand.Read never fails
	return "r-" + now.UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(b[:])
}
