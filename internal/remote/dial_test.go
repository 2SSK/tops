package remote

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// echo writes "<cmd>:<stdin>" to stdout and "warn" to stderr.
func echo(cmd string, stdin io.Reader, stdout, stderr io.Writer) uint32 {
	in, _ := io.ReadAll(stdin)
	_, _ = io.WriteString(stdout, cmd+":"+string(in))
	_, _ = io.WriteString(stderr, "warn")
	return 0
}

// newNode starts a server with one ed25519 host key and returns it with a Config that
// knows that key and holds the one client key it accepts.
func newNode(t *testing.T, exec execFunc) (*testServer, Config) {
	t.Helper()
	keyPath, clientKey := writeKey(t, false, 0o600)
	host := hostSigner(t, "ed25519")
	s := startServer(t, clientKey, exec, host)
	return s, Config{User: "deploy", Port: s.port, Key: keyPath, KnownHosts: s.knownHostsFor(t, host.PublicKey())}
}

// dial connects to vps-01 at 127.0.0.1; tune changes the Dialer first (timeouts).
func dial(t *testing.T, cfg Config, tune ...func(*Dialer)) (*Client, error) {
	t.Helper()
	d, err := NewDialer(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	for _, f := range tune {
		f(d)
	}
	c, err := d.Dial(t.Context(), "vps-01", "127.0.0.1")
	if err == nil {
		t.Cleanup(func() { _ = c.Close() })
	}
	return c, err
}

func mustDial(t *testing.T, cfg Config, tune ...func(*Dialer)) *Client {
	t.Helper()
	c, err := dial(t, cfg, tune...)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestRun(t *testing.T) {
	s, cfg := newNode(t, echo)
	c := mustDial(t, cfg)
	if c.Node != "vps-01" || !strings.HasPrefix(c.Addr, "127.0.0.1:") {
		t.Errorf("client = %s at %s", c.Node, c.Addr)
	}
	var stdout, stderr bytes.Buffer
	if err := c.run(t.Context(), "hello", strings.NewReader(`{"runId":"r-1"}`), &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if got := stdout.String(); got != `hello:{"runId":"r-1"}` {
		t.Errorf("stdout = %q", got)
	}
	if got := stderr.String(); got != "warn" {
		t.Errorf("stderr = %q", got)
	}
	// one connection, one session per command
	if err := c.run(t.Context(), "again", strings.NewReader(""), io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	if got := s.commands(); !slices.Equal(got, []string{"hello", "again"}) {
		t.Errorf("commands = %q", got)
	}
}

func TestRunExitStatus(t *testing.T) {
	_, cfg := newNode(t, func(string, io.Reader, io.Writer, io.Writer) uint32 { return 3 })
	err := mustDial(t, cfg).run(t.Context(), "facts", strings.NewReader(""), io.Discard, io.Discard)
	var ee *ssh.ExitError
	if !errors.As(err, &ee) || ee.ExitStatus() != 3 {
		t.Fatalf("err = %v, want exit status 3", err)
	}
}

func TestRunCancel(t *testing.T) {
	sawEOF := make(chan struct{})
	_, cfg := newNode(t, func(_ string, stdin io.Reader, _, _ io.Writer) uint32 {
		_, _ = io.Copy(io.Discard, stdin) // a streaming op runs until stdin closes
		close(sawEOF)
		return 0
	})
	c := mustDial(t, cfg)
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	stdin, w := io.Pipe() // never written: the op would run forever
	defer func() { _ = w.Close() }()
	err := c.run(ctx, "journal", stdin, io.Discard, io.Discard)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want the context's error", err)
	}
	select {
	case <-sawEOF:
	case <-time.After(5 * time.Second):
		t.Fatal("the remote side never saw stdin close")
	}
}

func TestDialOffersTheStoredKeyType(t *testing.T) {
	keyPath, clientKey := writeKey(t, false, 0o600)
	ecdsaKey, edKey := hostSigner(t, "ecdsa"), hostSigner(t, "ed25519")
	s := startServer(t, clientKey, echo, ecdsaKey, edKey) // Go clients prefer ecdsa by default
	cfg := Config{User: "deploy", Port: s.port, Key: keyPath, KnownHosts: s.knownHostsFor(t, edKey.PublicKey())}
	if _, err := dial(t, cfg); err != nil {
		t.Fatalf("only ed25519 is stored, so only ed25519 may be asked for: %v", err)
	}
}

func TestDialRefusesUnknownHost(t *testing.T) {
	_, cfg := newNode(t, echo)
	cfg.KnownHosts = writeKnownHosts(t, "# no nodes yet")
	_, err := dial(t, cfg)
	if ue := userError(t, err, "ssh.host_key_unknown"); !strings.Contains(ue.What, "vps-01") {
		t.Errorf("What = %q, want the node named", ue.What)
	}
}

func TestDialRefusesChangedKey(t *testing.T) {
	s, cfg := newNode(t, echo)
	cfg.KnownHosts = s.knownHostsFor(t, hostSigner(t, "ed25519").PublicKey())
	_, err := dial(t, cfg)
	if ue := userError(t, err, "ssh.host_key_changed"); !strings.Contains(ue.What, "vps-01") {
		t.Errorf("What = %q, want the node named", ue.What)
	}
}

func TestDialAuthRefused(t *testing.T) {
	_, cfg := newNode(t, echo)
	cfg.Key, _ = writeKey(t, false, 0o600) // a key the node does not accept
	_, err := dial(t, cfg)
	ue := userError(t, err, "ssh.auth")
	if !strings.Contains(ue.What, "vps-01") {
		t.Errorf("What = %q, want the node named", ue.What)
	}
}

func TestDialUnreachable(t *testing.T) {
	_, cfg := newNode(t, echo)
	l, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Port = l.Addr().(*net.TCPAddr).Port
	_ = l.Close() // nothing listens there now
	_, err = dial(t, cfg)
	ue := userError(t, err, "ssh.unreachable")
	if !strings.Contains(ue.What, "vps-01") {
		t.Errorf("What = %q, want the node named", ue.What)
	}
}

func TestDialHandshakeTimeout(t *testing.T) {
	_, cfg := newNode(t, echo)
	l, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	go func() { // accepts and never speaks SSH
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { _ = conn.Close() })
		}
	}()
	cfg.Port = l.Addr().(*net.TCPAddr).Port
	d, err := NewDialer(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	d.timeout = 200 * time.Millisecond
	start := time.Now()
	_, err = d.Dial(t.Context(), "vps-01", "127.0.0.1")
	ue := userError(t, err, "ssh.unreachable")
	if !strings.Contains(ue.Why, "handshake") {
		t.Errorf("Why = %q, want the handshake named", ue.Why)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("Dial took %v, want it bounded by the timeout", took)
	}
}

func TestNewDialerErrors(t *testing.T) {
	_, cfg := newNode(t, echo)
	missing := cfg
	missing.KnownHosts += ".absent"
	if _, err := NewDialer(t.Context(), missing); err == nil {
		t.Error("a missing known_hosts was accepted")
	}
	noKey := cfg
	noKey.Key = ""
	if _, err := NewDialer(t.Context(), noKey); err == nil {
		t.Error("no ssh.key and no agent was accepted")
	}
}
