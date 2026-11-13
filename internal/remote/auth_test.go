package remote

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// passphrase encrypts the test keys. Encrypting is slow (bcrypt, more so under -race), so
// encryptedKey makes one key for the whole package.
const passphrase = "correct horse"

var encryptedKey = sync.OnceValues(func() (ed25519.PrivateKey, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	return priv, err
})

var encryptedPEM = sync.OnceValues(func() ([]byte, error) {
	priv, err := encryptedKey()
	if err != nil {
		return nil, err
	}
	block, err := ssh.MarshalPrivateKeyWithPassphrase(priv, "", []byte(passphrase))
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(block), nil
})

// writeKey writes an ed25519 private key in OpenSSH format with the given mode: a new key, or
// the package's encrypted one. It returns the path and the public key.
func writeKey(t *testing.T, encrypted bool, mode os.FileMode) (string, ssh.PublicKey) {
	t.Helper()
	var priv ed25519.PrivateKey
	var data []byte
	var err error
	if encrypted {
		if priv, err = encryptedKey(); err == nil {
			data, err = encryptedPEM()
		}
	} else {
		_, priv, err = ed25519.GenerateKey(rand.Reader)
		var block *pem.Block
		if err == nil {
			block, err = ssh.MarshalPrivateKey(priv, "")
		}
		if err == nil {
			data = pem.EncodeToMemory(block)
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil { // WriteFile's mode is masked by the umask
		t.Fatal(err)
	}
	key, err := ssh.NewPublicKey(priv.Public())
	if err != nil {
		t.Fatal(err)
	}
	return path, key
}

// serveAgent runs an ssh agent holding one new key and returns its socket and public key.
func serveAgent(t *testing.T) (string, ssh.PublicKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	keyring := agent.NewKeyring()
	if err := keyring.Add(agent.AddedKey{PrivateKey: priv}); err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(t.TempDir(), "agent.sock")
	l, err := (&net.ListenConfig{}).Listen(t.Context(), "unix", sock)
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
			go func() { _ = agent.ServeAgent(keyring, conn) }()
		}
	}()
	key, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return sock, key
}

// offers asserts id offers exactly the public key want.
func offers(t *testing.T, id identity, want ssh.PublicKey) {
	t.Helper()
	t.Cleanup(func() { _ = id.close() })
	got, err := id.signers()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || string(got[0].PublicKey().Marshal()) != string(want.Marshal()) {
		t.Fatalf("signers offer %d keys, want exactly %s", len(got), ssh.FingerprintSHA256(want))
	}
	if id.auth() == nil {
		t.Fatal("auth() = nil")
	}
}

func noPassphrase(string) ([]byte, error) { panic("asked for a passphrase") }

func TestIdentityKeyFile(t *testing.T) {
	path, key := writeKey(t, false, 0o600)
	sock, _ := serveAgent(t)
	id, err := loadIdentity(t.Context(), path, sock, noPassphrase)
	if err != nil {
		t.Fatal(err)
	}
	offers(t, id, key) // ssh.key wins over the agent
}

func TestIdentityEncryptedKey(t *testing.T) {
	t.Parallel() // decrypting is slow; overlap with the wrong-passphrase case
	path, key := writeKey(t, true, 0o600)
	var asked string
	id, err := loadIdentity(t.Context(), path, "", func(p string) ([]byte, error) {
		asked = p
		return []byte(passphrase), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if asked != path {
		t.Errorf("passphrase asked for %q, want %q", asked, path)
	}
	offers(t, id, key)
}

func TestIdentityAgent(t *testing.T) {
	sock, key := serveAgent(t)
	id, err := loadIdentity(t.Context(), "", sock, nil)
	if err != nil {
		t.Fatal(err)
	}
	offers(t, id, key)
}

func TestIdentityErrors(t *testing.T) {
	t.Parallel()
	plain, _ := writeKey(t, false, 0o600)
	open, _ := writeKey(t, false, 0o644)
	locked, _ := writeKey(t, false, 0o000)
	encrypted, _ := writeKey(t, true, 0o600)
	garbage := filepath.Join(t.TempDir(), "garbage")
	if err := os.WriteFile(garbage, []byte("not a key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	wrong := func(string) ([]byte, error) { return []byte("battery staple"), nil }
	tests := []struct {
		name, key, sock string
		passphrase      func(string) ([]byte, error)
		want            string // in What, Why or Fix
	}{
		{"no key and no agent", "", "", nil, "SSH_AUTH_SOCK"},
		{"agent not running", "", filepath.Join(t.TempDir(), "none.sock"), nil, "ssh-agent"},
		{"missing key", plain + ".absent", "", nil, "cannot read"},
		{"key readable by others", open, "", nil, "chmod 600 " + open},
		{"key unreadable", locked, "", nil, "cannot read"},
		{"not a key", garbage, "", nil, "cannot read ssh.key " + garbage},
		{"encrypted, no terminal", encrypted, "", nil, "no terminal"},
		{"encrypted, wrong passphrase", encrypted, "", wrong, "check the passphrase"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := loadIdentity(t.Context(), tt.key, tt.sock, tt.passphrase)
			ue := userError(t, err, "ssh.auth")
			if all := ue.What + "\n" + ue.Why + "\n" + ue.Fix; !strings.Contains(all, tt.want) {
				t.Errorf("error = %+v, want %q in it", ue, tt.want)
			}
		})
	}
}

func TestIdentityPassphraseFails(t *testing.T) {
	path, _ := writeKey(t, true, 0o600)
	interrupted := errors.New("interrupted")
	_, err := loadIdentity(t.Context(), path, "", func(string) ([]byte, error) { return nil, interrupted })
	if !errors.Is(err, interrupted) {
		t.Fatalf("err = %v, want the prompt's error", err)
	}
}
