package remote

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"

	"github.com/2SSK/tops/internal/term"
	"golang.org/x/crypto/ssh"
)

// The fixed command strings tops sends before the helper exists. Nothing is ever interpolated
// into a command (TRD "Security").
const (
	cmdArch = "uname -m"
	cmdSudo = "sudo -n true"
)

// goarch maps uname -m to the helper binary's GOARCH. Nodes are amd64 or arm64.
var goarch = map[string]string{"x86_64": "amd64", "aarch64": "arm64"}

// preflight checks what every run needs from a node before the helper runs: passwordless
// sudo and port forwarding. It returns the node's GOARCH, which picks the helper binary.
func (c *Client) preflight(ctx context.Context) (string, error) {
	var out, stderr bytes.Buffer
	if err := c.run(ctx, cmdArch, nil, &out, io.Discard); err != nil {
		return "", fmt.Errorf("%s on %s: %w", cmdArch, c.Node, err)
	}
	machine := strings.TrimSpace(out.String())
	arch, ok := goarch[machine]
	if !ok {
		return "", &term.UserError{
			Code: "helper.no_binary",
			What: fmt.Sprintf("no helper for %s (%s)", c.Node, machine),
			Why:  "tops runs on amd64 and arm64 nodes",
			Fix:  "use an amd64 or arm64 node",
		}
	}
	if err := c.run(ctx, cmdSudo, nil, io.Discard, &stderr); err != nil {
		var ee *ssh.ExitError
		if !errors.As(err, &ee) {
			return "", fmt.Errorf("%s on %s: %w", cmdSudo, c.Node, err)
		}
		return "", &term.UserError{
			Code: "ssh.sudo_password",
			What: "sudo on " + c.Node + " asks for a password",
			Why:  "tops needs passwordless sudo; sudo said: " + strings.TrimSpace(stderr.String()),
			Fix:  "add a NOPASSWD rule for the SSH user",
		}
	}
	conn, err := c.DialContext(ctx, "tcp", "127.0.0.1:22")
	if err != nil {
		return "", err
	}
	_ = conn.Close()
	return arch, nil
}

// DialContext opens a TCP connection from the node to addr, through the SSH connection: how
// tops reaches ports that listen only on the nodde. It fits the grpc and pgx dialers.
func (c *Client) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	conn, err := c.ssh.DialContext(ctx, network, addr)
	if err == nil {
		return conn, nil
	}
	var oe *ssh.OpenChannelError
	if errors.As(err, &oe) && oe.Reason == ssh.Prohibited {
		return nil, &term.UserError{
			Code: "ssh.forwarding_disabled",
			What: c.Node + " refuses port forwarding",
			Fix:  "allow forwarding for the SSH user (AllowTcpForwarding local)",
		}
	}
	return nil, &term.UserError{
		Code: "ssh.tunnel_failed",
		What: fmt.Sprintf("cannot reach %s on %s", addr, c.Node),
		Why:  err.Error(),
		Fix:  "check that something listens on " + addr + " on " + c.Node,
	}
}
