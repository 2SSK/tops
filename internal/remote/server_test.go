package remote

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// execFunc plays a command on the test server and returns its exit status.
type execFunc func(cmd string, stdin io.Reader, stdout, stderr io.Writer) uint32

// testServer is an SSH server inside the test: real handshake, host keys and public key auth,
// with commands played by exec instead of a shell.
type testServer struct {
	port     int
	hostKeys []ssh.Signer
	exec     execFunc

	silent           atomic.Bool // stop answering keepalives: a node that vanished
	refuseForwarding atomic.Bool // sshd with AllowTcpForwarding no

	mu   sync.Mutex
	cmds []string // every command a client ran, in order
}

// hostSigner makes a new host key: "ed25519" or "ecdsa".
func hostSigner(t *testing.T, kind string) ssh.Signer {
	t.Helper()
	var priv any
	var err error
	switch kind {
	case "ed25519":
		_, priv, err = ed25519.GenerateKey(rand.Reader)
	case "ecdsa":
		priv, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	}
	if err != nil {
		t.Fatal(err)
	}
	s, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// startServer listens on 127.0.0.1 and accepts only the client key allowed.
func startServer(t *testing.T, allowed ssh.PublicKey, exec execFunc, hostKeys ...ssh.Signer) *testServer {
	t.Helper()
	return startServerAt(t, "127.0.0.1:0", allowed, exec, hostKeys...)
}

// startServerAt listens on listen ("127.0.0.2:2222"): a fleet's nodes share ssh.port, so
// several test nodes listen on one port at different loopback addresses.
func startServerAt(t *testing.T, listen string, allowed ssh.PublicKey, exec execFunc, hostKeys ...ssh.Signer) *testServer {
	t.Helper()
	s := &testServer{hostKeys: hostKeys, exec: exec}
	cfg := &ssh.ServerConfig{
		PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if string(key.Marshal()) != string(allowed.Marshal()) {
				return nil, errors.New("not authorized")
			}
			return nil, nil
		},
	}
	for _, k := range hostKeys {
		cfg.AddHostKey(k)
	}
	l, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", listen)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	s.port = l.Addr().(*net.TCPAddr).Port
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go s.serve(conn, cfg)
		}
	}()
	return s
}

func (s *testServer) serve(conn net.Conn, cfg *ssh.ServerConfig) {
	sc, chans, reqs, err := ssh.NewServerConn(conn, cfg)
	if err != nil {
		return
	}
	defer func() { _ = sc.Close() }()
	go s.globalRequests(reqs)
	for nc := range chans {
		switch nc.ChannelType() {
		case "session":
			ch, creqs, err := nc.Accept()
			if err != nil {
				return
			}
			go s.session(ch, creqs)
		case "direct-tcpip":
			go s.forward(nc)
		default:
			_ = nc.Reject(ssh.UnknownChannelType, "only sessions and direct-tcpip")
		}
	}
}

// globalRequests answers keepalives the way OpenSSH does (a failure reply), unless silent.
func (s *testServer) globalRequests(reqs <-chan *ssh.Request) {
	for req := range reqs {
		if req.WantReply && !s.silent.Load() {
			_ = req.Reply(false, nil)
		}
	}
}

// forward plays sshd's direct-tcpip: it connects from the server to the address asked for.
func (s *testServer) forward(nc ssh.NewChannel) {
	if s.refuseForwarding.Load() {
		_ = nc.Reject(ssh.Prohibited, "port forwarding is disabled")
		return
	}
	var p struct {
		Host     string
		Port     uint32
		OrigHost string
		OrigPort uint32
	}
	if err := ssh.Unmarshal(nc.ExtraData(), &p); err != nil {
		_ = nc.Reject(ssh.ConnectionFailed, "bad payload")
		return
	}
	conn, err := net.Dial("tcp", net.JoinHostPort(p.Host, strconv.Itoa(int(p.Port)))) //nolint:noctx // test server
	if err != nil {
		_ = nc.Reject(ssh.ConnectionFailed, err.Error())
		return
	}
	ch, reqs, err := nc.Accept()
	if err != nil {
		_ = conn.Close()
		return
	}
	go ssh.DiscardRequests(reqs)
	go func() {
		_, _ = io.Copy(ch, conn)
		_ = ch.CloseWrite()
	}()
	_, _ = io.Copy(conn, ch)
	_ = conn.Close()
	_ = ch.Close()
}

func (s *testServer) session(ch ssh.Channel, reqs <-chan *ssh.Request) {
	defer func() { _ = ch.Close() }()
	for req := range reqs {
		if req.Type != "exec" {
			_ = req.Reply(false, nil)
			continue
		}
		var p struct{ Cmd string }
		if err := ssh.Unmarshal(req.Payload, &p); err != nil {
			_ = req.Reply(false, nil)
			return
		}
		_ = req.Reply(true, nil)
		s.mu.Lock()
		s.cmds = append(s.cmds, p.Cmd)
		s.mu.Unlock()
		status := s.exec(p.Cmd, ch, ch, ch.Stderr())
		_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{status}))
		return
	}
}

// commands returns every command run so far.
func (s *testServer) commands() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.cmds...)
}

// knownHostsFor writes a known_hosts file listing keys for the server's [127.0.0.1]:port.
func (s *testServer) knownHostsFor(t *testing.T, keys ...ssh.PublicKey) string {
	t.Helper()
	host := "[127.0.0.1]:" + strconv.Itoa(s.port)
	var lines []string
	for _, k := range keys {
		lines = append(lines, knownhosts.Line([]string{host}, k))
	}
	return writeKnownHosts(t, lines...)
}
