package cli

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/2SSK/tops/internal/fleet"
	"github.com/2SSK/tops/internal/remote"
	"github.com/2SSK/tops/internal/wire"
	"github.com/spf13/cobra"
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
func connect(cmd *cobra.Command, opts *Options, env *fleet.Env) (*connected, error) {
	ctx := cmd.Context()
	s := env.Cluster.SSH
	d, err := remote.NewDialer(ctx, remote.Config{
		User:       s.User,
		Port:       s.Port,
		Key:        s.Key,
		KnownHosts: s.KnownHosts,
		AuthSock:   opts.Getenv("SSH_AUTH_SOCK"),
		Passphrase: opts.Passphrase,
		Version:    Version,
		Cache:      cacheDir(opts),
		Log:        debugLog(cmd.ErrOrStderr(), opts),
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

// cacheDir is where downloaded helpers live: ${XDG_CACHE_HOME:-~/.cache}/tops on every OS
// (backend schema "Files on the workstation"), so a Mac and Linux use the same path.
func cacheDir(opts *Options) string {
	base := opts.Getenv("XDG_CACHE_HOME")
	if base == "" {
		base = filepath.Join(opts.Getenv("HOME"), ".cache")
	}
	return filepath.Join(base, "tops")
}

// debugLog is the --debug logger: slog text records on stderr, or none.
func debugLog(w io.Writer, opts *Options) *slog.Logger {
	if !opts.Debug {
		return slog.New(slog.DiscardHandler)
	}
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelDebug}))
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
