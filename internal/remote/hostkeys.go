// Package remote reaches the nodes over SSH: host keys, sessions, helper upload and tunnels.
package remote

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"net"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/2SSK/tops/internal/term"
)

// knownHosts checks host keys against the spec's known_hosts file. There is no trust on first
// use: a host missing from the file is refused, like a host whose key changed.
type knownHosts struct {
	path  string
	check ssh.HostKeyCallback
}

func loadKnownHosts(path string) (knownHosts, error) {
	check, err := knownhosts.New(path)
	if err != nil {
		return knownHosts{}, &term.UserError{
			Code: "ssh.host_key_unknown",
			What: "cannot read known_hosts " + path,
			Why:  err.Error(),
			Fix:  "set ssh.knownHosts to the file that lists every node' host key",
		}
	}
	return knownHosts{path: path, check: check}, nil
}

// algorithms lists the host key algorithms stored for addr ("10.80.0.11:22"), so the server
// presents the key we know rather than another type we never stored. Asking with a key that
// cannot be in the file makes knownhosts return every stored key for addr in KeyError.Want.
func (k knownHosts) algorithms(addr string) []string {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil
	}
	probe, err := ssh.NewPublicKey(pub)
	if err != nil {
		return nil
	}
	var ke *knownhosts.KeyError
	if !errors.As(k.check(addr, &net.TCPAddr{}, probe), &ke) {
		return nil
	}
	var algos []string
	for _, w := range ke.Want {
		if w.Key.Type() == ssh.KeyAlgoRSA { // the key type; servers sign with SHA-2
			algos = append(algos, ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256)
			continue
		}
		algos = append(algos, w.Key.Type())
	}
	return algos
}

// verify is the HostKeyCallback for one node; refusals name the node and the key to verify.
func (k knownHosts) verify(node string) ssh.HostKeyCallback {
	return func(addr string, remote net.Addr, key ssh.PublicKey) error {
		err := k.check(addr, remote, key)
		var ke *knownhosts.KeyError
		switch {
		case err == nil:
			return nil
		case errors.As(err, &ke) && len(ke.Want) == 0:
			return &term.UserError{
				Code: "ssh.host_key_unknown",
				What: fmt.Sprintf("%s (%s) is not in %s", node, addr, k.path),
				Why:  "tops never trusts a host key on first use",
				Fix:  fmt.Sprintf("verify %s with the node's owner, then add the line: %s", ssh.FingerprintSHA256(key), knownhosts.Line([]string{addr}, key)),
			}
		case errors.As(err, &ke):
			w := ke.Want[0]
			return &term.UserError{
				Code: "ssh.host_key_changed",
				What: node + " presented a different host key",
				Why:  fmt.Sprintf("known %s, presented %s; a rebuilt node or an attack", ssh.FingerprintSHA256(w.Key), ssh.FingerprintSHA256(key)),
				Fix:  fmt.Sprintf("if the node was rebuilt, verify %s, then replace %s:%d", ssh.FingerprintSHA256(key), w.Filename, w.Line),
			}
		default:
			return fmt.Errorf("check host key of %s: %w", node, err)
		}
	}
}
