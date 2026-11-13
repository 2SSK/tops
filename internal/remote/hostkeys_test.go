package remote

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/2SSK/tops/internal/term"
)

// hostKey makes a new host key of the given type: "ed25519", "rsa" or "ecdsa".
func hostKey(t *testing.T, kind string) ssh.PublicKey {
	t.Helper()
	var pub any
	switch kind {
	case "ed25519":
		p, _, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		pub = p
	case "rsa":
		k, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatal(err)
		}
		pub = &k.PublicKey
	case "ecdsa":
		k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		pub = &k.PublicKey
	}
	key, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

// writeKnownHosts writes lines to a known_hosts file and returns its path.
func writeKnownHosts(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func load(t *testing.T, path string) knownHosts {
	t.Helper()
	k, err := loadKnownHosts(path)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// userError asserts err is a *term.UserError with the given code and returns it.
func userError(t *testing.T, err error, code string) *term.UserError {
	t.Helper()
	var ue *term.UserError
	if !errors.As(err, &ue) {
		t.Fatalf("err = %v, want a *term.UserError", err)
	}
	if ue.Code != code {
		t.Fatalf("code = %q, want %q (%+v)", ue.Code, code, ue)
	}
	return ue
}

var remoteAddr = &net.TCPAddr{IP: net.IPv4(10, 80, 0, 11), Port: 22}

// lab is a known_hosts file as VPSLAB writes it, and the keys in it.
type lab struct {
	path         string
	known, other ssh.PublicKey
	k            knownHosts
}

func newLab(t *testing.T) lab {
	t.Helper()
	l := lab{known: hostKey(t, "ed25519"), other: hostKey(t, "ed25519")}
	l.path = writeKnownHosts(t,
		"# VPSLAB writes both forms; tops dials the second",
		knownhosts.Line([]string{"[127.0.0.1]:2201"}, l.other),
		knownhosts.Line([]string{"10.80.0.11"}, l.known),
		knownhosts.Line([]string{"[10.80.0.12]:2222"}, l.known),
	)
	l.k = load(t, l.path)
	return l
}

func TestVerifyKnown(t *testing.T) {
	l := newLab(t)
	if err := l.k.verify("vps-01")("10.80.0.11:22", remoteAddr, l.known); err != nil {
		t.Errorf("port 22: %v", err)
	}
	if err := l.k.verify("vps-02")("10.80.0.12:2222", remoteAddr, l.known); err != nil {
		t.Errorf("a non-default port is stored as [host]:port: %v", err)
	}
}

func TestVerifyChanged(t *testing.T) {
	l := newLab(t)
	ue := userError(t, l.k.verify("vps-01")("10.80.0.11:22", remoteAddr, l.other), "ssh.host_key_changed")
	if !strings.Contains(ue.What, "vps-01") {
		t.Errorf("What = %q, want the node named", ue.What)
	}
	for _, want := range []string{ssh.FingerprintSHA256(l.known), ssh.FingerprintSHA256(l.other)} {
		if !strings.Contains(ue.Why, want) {
			t.Errorf("Why = %q, want %s", ue.Why, want)
		}
	}
	if !strings.Contains(ue.Fix, l.path+":3") {
		t.Errorf("Fix = %q, want the line to replace, %s:3", ue.Fix, l.path)
	}
}

func TestVerifyUnknown(t *testing.T) {
	l := newLab(t)
	ue := userError(t, l.k.verify("vps-03")("10.80.0.13:22", remoteAddr, l.other), "ssh.host_key_unknown")
	if !strings.Contains(ue.What, "vps-03") || !strings.Contains(ue.What, l.path) {
		t.Errorf("What = %q, want the node and the file", ue.What)
	}
	if line := knownhosts.Line([]string{"10.80.0.13:22"}, l.other); !strings.Contains(ue.Fix, line) {
		t.Errorf("Fix = %q, want the line to add %q", ue.Fix, line)
	}
	if !strings.Contains(ue.Fix, ssh.FingerprintSHA256(l.other)) {
		t.Errorf("Fix = %q, want the fingerprint to verify", ue.Fix)
	}
}

func TestVerifyRevoked(t *testing.T) {
	key := hostKey(t, "ed25519")
	k := load(t, writeKnownHosts(t, "@revoked "+knownhosts.Line([]string{"*"}, key)))
	err := k.verify("vps-01")("10.80.0.11:22", remoteAddr, key)
	var re *knownhosts.RevokedError
	if !errors.As(err, &re) {
		t.Fatalf("err = %v, want the RevokedError wrapped", err)
	}
	if !strings.Contains(err.Error(), "vps-01") {
		t.Errorf("err = %v, want the node named", err)
	}
}

func TestAlgorithms(t *testing.T) {
	ed, rsaKey, ec := hostKey(t, "ed25519"), hostKey(t, "rsa"), hostKey(t, "ecdsa")
	k := load(t, writeKnownHosts(t,
		knownhosts.Line([]string{"10.80.0.11"}, ed),
		knownhosts.Line([]string{"10.80.0.12"}, rsaKey),
		knownhosts.Line([]string{"10.80.0.13"}, ec),
		knownhosts.Line([]string{"10.80.0.13"}, ed),
		knownhosts.Line([]string{"[10.80.0.14]:2222"}, ed),
	))
	tests := []struct {
		addr string
		want []string
	}{
		{"10.80.0.11:22", []string{"ssh-ed25519"}},
		{"10.80.0.12:22", []string{"rsa-sha2-512", "rsa-sha2-256"}}, // never SHA-1 ssh-rsa
		{"10.80.0.13:22", []string{"ecdsa-sha2-nistp256", "ssh-ed25519"}},
		{"10.80.0.14:2222", []string{"ssh-ed25519"}},
		{"10.80.0.14:22", nil}, // another port is another host
		{"10.80.0.99:22", nil}, // unknown: verify refuses it
	}
	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			if got := k.algorithms(tt.addr); !slices.Equal(got, tt.want) {
				t.Errorf("algorithms(%s) = %v, want %v", tt.addr, got, tt.want)
			}
		})
	}
}

func TestLoadKnownHostsMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent")
	_, err := loadKnownHosts(path)
	ue := userError(t, err, "ssh.host_key_unknown")
	if !strings.Contains(ue.What, path) {
		t.Errorf("What = %q, want the path", ue.What)
	}
}

// TestNoInsecureHostKeys fails if any Go file in the module disables host key checking (H3).
func TestNoInsecureHostKeys(t *testing.T) {
	banned := "Insecure" + "IgnoreHostKey" // split, so this file does not match itself
	root := os.DirFS(filepath.Join("..", ".."))
	err := fs.WalkDir(root, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == ".cache" || d.Name() == ".git") {
			return fs.SkipDir
		}
		if d.IsDir() || filepath.Ext(path) != ".go" {
			return nil
		}
		b, err := fs.ReadFile(root, path)
		if err != nil {
			return err
		}
		if strings.Contains(string(b), banned) {
			t.Errorf("%s uses ssh.%s: tops checks every host key against known_hosts", path, banned)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
