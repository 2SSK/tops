package remote

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"

	"github.com/2SSK/tops/internal/term"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// identity is how tops authenticates to every node of a run: the spec's key file, or the
// agent. Never a password.
type identity struct {
	signers func() ([]ssh.Signer, error)
	close   func() error
}

// auth is the one ssh.AuthMethod a client offers.
func (id identity) auth() ssh.AuthMethod {
	return ssh.PublicKeysCallback(id.signers)
}

// loadIdentity uses key if the spec sets ssh.key, else the agent at sock ($SSH_AUTH_SOCK).
// passphrase asks for an encrypted key's passphrase; nil when there is no terminal to ask on.
func loadIdentity(ctx context.Context, key, sock string, passphrase func(path string) ([]byte, error)) (identity, error) {
	if key != "" {
		return keyFile(key, passphrase)
	}
	if sock == "" {
		return identity{}, authError("no key to authenticate with", "ssh.key is not set and SSH_AUTH_SOCK is empty", "set ssh.key, or start ssh-agent and ssh-add the key")
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", sock)
	if err != nil {
		return identity{}, authError("cannot reach the ssh agent", err.Error(), "start ssh-agent and ssh-add they key, or set ssh.key")
	}
	return identity{signers: agent.NewClient(conn).Signers, close: conn.Close}, nil
}

// keyFile reads a private key the way OpenSSH does: refused if others can read it.
func keyFile(path string, passphrase func(path string) ([]byte, error)) (identity, error) {
	info, err := os.Stat(path)
	if err != nil {
		return identity{}, authError("cannot read ssh.key "+path, err.Error(), "set ssh.key to your private key, or remove it to use the agent")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return identity{}, authError("ssh.key "+path+" is readable by others", fmt.Sprintf("mode %04o; a private key must be private", info.Mode().Perm()), "chmod 600 "+path)
	}
	pem, err := os.ReadFile(path) //nolint:gosec	// the path is the user's own ssh.key
	if err != nil {
		return identity{}, authError("cannot read ssh.key "+path, err.Error(), "check the file's owner")
	}
	signer, err := ssh.ParsePrivateKey(pem)
	var missing *ssh.PassphraseMissingError
	switch {
	case errors.As(err, &missing):
		if signer, err = decrypt(path, pem, passphrase); err != nil {
			return identity{}, err
		}
	case err != nil:
		return identity{}, authError("cannot read ssh.key "+path, err.Error(), "set ssh.key to an OpenSSH private key")
	}
	return identity{
		signers: func() ([]ssh.Signer, error) { return []ssh.Signer{signer}, nil },
		close:   func() error { return nil },
	}, nil
}

func decrypt(path string, pem []byte, passphrase func(path string) ([]byte, error)) (ssh.Signer, error) {
	if passphrase == nil {
		return nil, authError("ssh.key "+path+" is encrypted", "there is no terminal to ask for its passphrase", "run from a terminal, or remove ssh.key and ssh-add the key to the agent")
	}
	pass, err := passphrase(path)
	if err != nil {
		return nil, fmt.Errorf("read the passphrase of %s: %w", path, err)
	}
	signer, err := ssh.ParsePrivateKeyWithPassphrase(pem, pass)
	if err != nil {
		return nil, authError("cannot decrypt ssh.key "+path, err.Error(), "check the passphrase")
	}
	return signer, nil
}

func authError(what, why, fix string) error {
	return &term.UserError{Code: "ssh.auth", What: what, Why: why, Fix: fix}
}
