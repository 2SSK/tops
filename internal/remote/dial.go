package remote

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/2SSK/tops/internal/term"
	"golang.org/x/crypto/ssh"
)

// dialTimeout bounds the TCP connect and the SSH handshake together (TRD "Timeouts").
const dialTimeout = 5 * time.Second

// Config is the spec's ssh section, resolved, plus what the workstation provides.
type Config struct {
	User       string
	Port       int
	Key        string // "" uses the agent
	KnownHosts string
	AuthSock   string                            // $SSH_AUTH_SOCK
	Passphrase func(path string) ([]byte, error) // nil without a terminal
}

// Dialer holds what every node of a run shares: the host keys and the identity.
type Dialer struct {
	cfg               Config
	hosts             knownHosts
	id                identity
	timeout           time.Duration
	keepalive         time.Duration
	exe, goos, goarch string // this executable and its platform: the helper on a matching node
	mu                sync.Mutex
	bins              map[string]binary // by GOARCH, hashed once per run
}

// NewDialer reads known_hosts and the identity once per run. Close it when the run ends.
func NewDialer(ctx context.Context, cfg Config) (*Dialer, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("find this executable: %w", err)
	}
	hosts, err := loadKnownHosts(cfg.KnownHosts)
	if err != nil {
		return nil, err
	}
	id, err := loadIdentity(ctx, cfg.Key, cfg.AuthSock, cfg.Passphrase)
	if err != nil {
		return nil, err
	}
	return &Dialer{
		cfg: cfg, hosts: hosts, id: id, timeout: dialTimeout, keepalive: keepaliveInterval, exe: exe, goos: runtime.GOOS, goarch: runtime.GOARCH, bins: map[string]binary{},
	}, nil
}

// Close releases the identity (the agent connection).
func (d *Dialer) Close() error {
	return d.id.close()
}

// Client is the one SSH connection to one node for the whole run.
type Client struct {
	Node string
	Addr string
	ssh  *ssh.Client

	closed    chan struct{} // closed by Close: stops the keepalive
	closeOnce sync.Once
	lost      atomic.Bool // set when the node stopped answering keepalives
}

// Dial connects to node at address and authenticates both ways: its host key against
// known_hosts, us with the identity.
func (d *Dialer) Dial(ctx context.Context, node, address string) (*Client, error) {
	addr := net.JoinHostPort(address, strconv.Itoa(d.cfg.Port))
	ctx, cancel := context.WithTimeout(ctx, d.timeout)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, unreachable(node, addr, err)
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() }) // a handshake that hangs
	defer stop()
	cc, chans, reqs, err := ssh.NewClientConn(conn, addr, &ssh.ClientConfig{
		User:              d.cfg.User,
		Auth:              []ssh.AuthMethod{d.id.auth()},
		HostKeyCallback:   d.hosts.verify(node),
		HostKeyAlgorithms: d.hosts.algorithms(addr),
	})
	if err != nil {
		_ = conn.Close()
		return nil, handshakeError(ctx, node, addr, err)
	}
	c := &Client{Node: node, Addr: addr, ssh: ssh.NewClient(cc, chans, reqs), closed: make(chan struct{})}
	go c.keepalive(d.keepalive)
	return c, nil
}

// handshakeError keeps a host key refusal as it is and names the rest.
func handshakeError(ctx context.Context, node, addr string, err error) error {
	var ue *term.UserError
	switch {
	case errors.As(err, &ue):
		return ue
	case strings.Contains(err.Error(), "unable to authenticate"): // x/crypto has no typed error for it
		return authError(node+" refused the key", err.Error(), "check ssh.user, ssh.key and the node's authorized_keys")
	case ctx.Err() != nil:
		return unreachable(node, addr, fmt.Errorf("no SSH handshake: %w", ctx.Err()))
	default:
		return unreachable(node, addr, err)
	}
}

func unreachable(node, addr string, err error) error {
	return &term.UserError{
		Code: "ssh.unreachable",
		What: fmt.Sprintf("%s (%s) unreachable", node, addr),
		Why:  err.Error(),
		Fix:  "check that the node is up and that sshd answers on " + addr + " from this workstation",
	}
}

// Close closes the connection and every session on it.
func (c *Client) Close() error {
	c.closeOnce.Do(func() { close(c.closed) })
	return c.ssh.Close()
}

// run executes cmd in one session and closes it. Cancelling ctx closes the session, which
// the helper sees as stdin EOF. A non-zero exit is an *ssh.ExitERror.
func (c *Client) run(ctx context.Context, cmd string, stdin io.Reader, stdout, stderr io.Writer) error {
	sess, err := c.ssh.NewSession()
	if err != nil {
		return fmt.Errorf("open a session on %s: %w", c.Node, err)
	}
	defer func() { _ = sess.Close() }() // a leaked session holds the connection open
	sess.Stdin, sess.Stdout, sess.Stderr = stdin, stdout, stderr
	stop := context.AfterFunc(ctx, func() { _ = sess.Close() })
	defer stop()
	err = sess.Run(cmd)
	switch {
	case ctx.Err() != nil:
		return ctx.Err()
	case err != nil && c.lost.Load():
		return unreachable(c.Node, c.Addr, errLost)
	}
	return err
}
