package remote

import (
	"context"
	"io"
	"net"
	"slices"
	"strings"
	"testing"
	"time"
)

// ubuntu plays a node's answers to the preflight commands.
func ubuntu(machine string, passwordless bool) execFunc {
	return func(cmd string, _ io.Reader, stdout, stderr io.Writer) uint32 {
		switch {
		case cmd == cmdArch:
			_, _ = io.WriteString(stdout, machine+"\n")
		case cmd == cmdSudo && !passwordless:
			_, _ = io.WriteString(stderr, "sudo: a password is required\n")
			return 1
		case cmd == cmdSudo:
		default:
			_, _ = io.WriteString(stderr, "sh: 1: "+cmd+": not found\n")
			return 127
		}
		return 0
	}
}

func TestPreflight(t *testing.T) {
	for machine, want := range map[string]string{"x86_64": "amd64", "aarch64": "arm64"} {
		t.Run(machine, func(t *testing.T) {
			s, cfg := newNode(t, ubuntu(machine, true))
			arch, err := mustDial(t, cfg).preflight(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if arch != want {
				t.Errorf("arch = %q, want %q", arch, want)
			}
			if got := s.commands(); !slices.Equal(got, []string{"uname -m", "sudo -n true"}) {
				t.Errorf("commands = %q, want only the fixed preflight strings", got)
			}
		})
	}
}

func TestPreflightRefuses(t *testing.T) {
	tests := []struct {
		name, machine    string
		passwordless     bool
		refuseForwarding bool
		code, want       string
	}{
		{"another arch", "riscv64", true, false, "helper.no_binary", "riscv64"},
		{"sudo asks for a password", "x86_64", false, false, "ssh.sudo_password", "a password is required"},
		{"forwarding disabled", "x86_64", true, true, "ssh.forwarding_disabled", "AllowTcpForwarding"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, cfg := newNode(t, ubuntu(tt.machine, tt.passwordless))
			s.refuseForwarding.Store(tt.refuseForwarding)
			_, err := mustDial(t, cfg).preflight(t.Context())
			ue := userError(t, err, tt.code)
			if all := ue.What + "\n" + ue.Why + "\n" + ue.Fix; !strings.Contains(all, tt.want) || !strings.Contains(ue.What, "vps-01") {
				t.Errorf("error = %+v, want vps-01 and %q in it", ue, tt.want)
			}
		})
	}
}

func TestPreflightCancelled(t *testing.T) {
	_, cfg := newNode(t, ubuntu("x86_64", true))
	c := mustDial(t, cfg)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := c.preflight(ctx); err == nil {
		t.Fatal("preflight ran with a cancelled context")
	}
}

// echoServer listens on the "node" side and echoes one line back.
func echoServer(t *testing.T) string {
	t.Helper()
	l, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				_, _ = io.Copy(conn, conn)
			}()
		}
	}()
	return l.Addr().String()
}

func TestDialContext(t *testing.T) {
	_, cfg := newNode(t, echo)
	target := echoServer(t)
	conn, err := mustDial(t, cfg).DialContext(t.Context(), "tcp", target)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := io.WriteString(conn, "ping"); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, 4)
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatal(err)
	}
	if string(got) != "ping" {
		t.Errorf("read %q through the tunnel, want ping", got)
	}
}

func TestDialContextFails(t *testing.T) {
	_, cfg := newNode(t, echo)
	l, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed := l.Addr().String()
	_ = l.Close()
	_, err = mustDial(t, cfg).DialContext(t.Context(), "tcp", closed)
	ue := userError(t, err, "ssh.tunnel_failed")
	if !strings.Contains(ue.What, closed) || !strings.Contains(ue.What, "vps-01") {
		t.Errorf("What = %q, want the address and the node", ue.What)
	}
}

func TestKeepaliveAnswered(t *testing.T) {
	_, cfg := newNode(t, echo)
	c := mustDial(t, cfg, func(d *Dialer) { d.keepalive = 10 * time.Millisecond })
	time.Sleep(20 * 10 * time.Millisecond) // many keepalives, all answered with a failure reply
	if err := c.run(t.Context(), "hello", strings.NewReader(""), io.Discard, io.Discard); err != nil {
		t.Fatalf("the connection did not survive answered keepalives: %v", err)
	}
}

func TestKeepaliveLost(t *testing.T) {
	s, cfg := newNode(t, func(_ string, stdin io.Reader, _, _ io.Writer) uint32 {
		_, _ = io.Copy(io.Discard, stdin)
		return 0
	})
	c := mustDial(t, cfg, func(d *Dialer) { d.keepalive = 20 * time.Millisecond })
	s.silent.Store(true) // the node vanished mid-op
	stdin, w := io.Pipe()
	defer func() { _ = w.Close() }()
	done := make(chan error, 1)
	go func() { done <- c.run(t.Context(), "journal", stdin, io.Discard, io.Discard) }()
	select {
	case err := <-done:
		ue := userError(t, err, "ssh.unreachable")
		if !strings.Contains(ue.Why, "keepalive") {
			t.Errorf("Why = %q, want the keepalives named", ue.Why)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run still hangs on a node that stopped answering")
	}
}
