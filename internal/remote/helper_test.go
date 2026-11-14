//go:build helper

// The helper tests: the real SSH path, the real upload and the real helper, against a
// systemd container built from test/helper. Run them with make helper-test, which builds
// bin/tops and the image first.

package remote

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/2SSK/tops/internal/term"
	"github.com/2SSK/tops/internal/wire"
)

const image = "tops-helper-test"

// The limits docker puts on the container; hello must detect them from the cgroup.
const (
	memLimit   = 1 << 30
	coresLimit = 1.5
)

// docker runs the docker CLI and returns its trimmed stdout.
func docker(t *testing.T, stdin []byte, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "docker", args...) //nolint:gosec // the test's own docker commands
	cmd.Stdin = bytes.NewReader(stdin)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("docker %s: %v: %s", strings.Join(args, " "), err, stderr.String())
	}
	return strings.TrimSpace(string(out))
}

// container is one bare node, and what the workstation needs to reach it.
type container struct {
	id       string
	cfg      Config
	hostKey  ssh.PublicKey   // ed25519, the one known_hosts stores
	hostKeys []ssh.PublicKey // all of them: an unknown host presents whichever the client prefers
	tops     string          // bin/tops: the helper to upload
	sum      string          // its sha256
}

// startContainer runs a fresh node from the image with the same flags as VPSLAB, a client key
// in deploy's authorized_keys, and known_hosts holding the host key read through docker,
// never through the network.
func startContainer(t *testing.T) *container {
	t.Helper()
	tops, err := filepath.Abs(filepath.Join("..", "..", "bin", "tops"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(tops) //nolint:gosec // our own build
	if err != nil {
		t.Fatalf("%v: run make helper-test, which builds bin/tops", err)
	}
	sum := sha256.Sum256(b)

	// Never --privileged: it hands the container the host's devices, and systemd inside takes
	// over the host's terminals. SYS_ADMIN without AppArmor's mount rule lets the image's init
	// remount its own cgroup namespace writable on docker versions that mount it read-only.
	id := docker(t, nil, "run", "--detach", "--tty", // tty: systemd's boot log in docker logs
		"--cgroupns", "private", "--cap-add", "SYS_ADMIN", "--security-opt", "apparmor=unconfined",
		"--tmpfs", "/run", "--tmpfs", "/run/lock",
		"--memory", "1g", "--cpus", "1.5",
		"--publish", "127.0.0.1::22", image)
	t.Cleanup(func() {
		if t.Failed() { // the container's state and systemd's boot log say why a node never came up
			state, _ := exec.Command("docker", "inspect", "--format", "{{.State.Status}} exit={{.State.ExitCode}} {{.State.Error}}", id).Output() //nolint:noctx,gosec // t.Context is done by then
			logs, _ := exec.Command("docker", "logs", "--tail", "30", id).CombinedOutput()                                                        //nolint:noctx,gosec // t.Context is done by then
			t.Logf("container %s: %s%s", id[:12], state, logs)
		}
		_ = exec.Command("docker", "rm", "--force", id).Run() //nolint:noctx,gosec // t.Context is done by then
	})

	keyPath, clientKey := writeKey(t, false, 0o600)
	docker(t, ssh.MarshalAuthorizedKey(clientKey), "exec", "-i", id, "sh", "-c",
		"install -d -m 0700 -o deploy -g deploy /home/deploy/.ssh && install -m 0600 -o deploy -g deploy /dev/stdin /home/deploy/.ssh/authorized_keys")
	hostKey, _, _, _, err := ssh.ParseAuthorizedKey([]byte(docker(t, nil, "exec", id, "cat", "/etc/ssh/ssh_host_ed25519_key.pub")))
	if err != nil {
		t.Fatal(err)
	}
	var all []ssh.PublicKey
	for _, line := range strings.Split(docker(t, nil, "exec", id, "sh", "-c", "cat /etc/ssh/ssh_host_*_key.pub"), "\n") {
		k, _, _, _, err := ssh.ParseAuthorizedKey([]byte(line))
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, k)
	}
	addr := docker(t, nil, "port", id, "22/tcp") // 127.0.0.1:49153
	host, port, ok := strings.Cut(addr, ":")
	if !ok || host != "127.0.0.1" {
		t.Fatalf("docker port = %q", addr)
	}
	c := &container{id: id, tops: tops, sum: hex.EncodeToString(sum[:]), hostKey: hostKey, hostKeys: all}
	c.cfg = Config{User: "deploy", Key: keyPath, KnownHosts: writeKnownHosts(t, knownhosts.Line([]string{addr}, hostKey))}
	if c.cfg.Port, err = strconv.Atoi(port); err != nil {
		t.Fatal(err)
	}
	return c
}

// dialer is a Dialer whose helper is bin/tops instead of the test binary.
func (c *container) dialer(t *testing.T, cfg Config) *Dialer {
	t.Helper()
	d, err := NewDialer(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	d.exe = c.tops
	return d
}

var req = wire.Request{RunID: "r-helper-test", By: "make@helper-test"}

// connect connects to the node, retrying while systemd is still starting sshd.
func (c *container) connect(t *testing.T, d *Dialer) (*Node, error) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		n, err := d.Connect(t.Context(), "vps-01", "127.0.0.1", req)
		var ue *term.UserError
		if errors.As(err, &ue) && ue.Code == "ssh.unreachable" && time.Now().Before(deadline) {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		if err == nil {
			t.Cleanup(func() { _ = n.Close() })
		}
		return n, err
	}
}

func TestHelperConnect(t *testing.T) {
	c := startContainer(t)
	d := c.dialer(t, c.cfg)
	n, err := c.connect(t, d)
	if err != nil {
		t.Fatal(err)
	}
	if !n.Uploaded {
		t.Error("a bare node already had the helper")
	}
	checkHello(t, n.Hello, c.sum)

	// T2.3 done-when: a second connect uploads nothing.
	n2, err := c.connect(t, c.dialer(t, c.cfg))
	if err != nil {
		t.Fatal(err)
	}
	if n2.Uploaded {
		t.Error("the second connect uploaded again")
	}
}

// checkHello asserts hello came from bin/tops on Ubuntu 24.04 and read docker's limits from
// the container's cgroup.
func checkHello(t *testing.T, h wire.Hello, sum string) {
	t.Helper()
	if h.SHA256 != sum {
		t.Errorf("hello sha256 = %s, want bin/tops' %s", h.SHA256, sum)
	}
	if h.OS != "ubuntu 24.04" {
		t.Errorf("hello os = %q", h.OS)
	}
	if h.Memory.Value != memLimit || !strings.HasPrefix(h.Memory.Source, "cgroup:") {
		t.Errorf("hello memory = %+v, want %d from the cgroup", h.Memory, memLimit)
	}
	if h.Cores.Value != coresLimit || !strings.HasPrefix(h.Cores.Source, "cgroup:") {
		t.Errorf("hello cores = %+v, want %v from the cgroup", h.Cores, coresLimit)
	}
}

func TestHelperFacts(t *testing.T) {
	c := startContainer(t)
	n, err := c.connect(t, c.dialer(t, c.cfg))
	if err != nil {
		t.Fatal(err)
	}
	r := req
	r.Args = []byte(`{"want":{"/etc/temporal/temporal.env":""},"dirs":["/etc/temporal"]}`)
	var f wire.Facts
	if err := n.call(t.Context(), n.SHA256, wire.OpFacts, r, &f); err != nil {
		t.Fatal(err)
	}
	if f.Applied != nil || f.Current != "" || len(f.Releases) != 0 || len(f.Units) != 0 {
		t.Errorf("facts on a bare node = %+v, want nothing of tops", f)
	}
	if st := f.Files["/etc/temporal/temporal.env"]; st.Exists {
		t.Errorf("temporal.env = %+v on a bare node", st)
	}
}

func TestHelperUnknownHost(t *testing.T) {
	c := startContainer(t)
	cfg := c.cfg
	cfg.KnownHosts = writeKnownHosts(t, "# a fresh workstation")
	_, err := c.connect(t, c.dialer(t, cfg))
	ue := userError(t, err, "ssh.host_key_unknown")
	if !slices.ContainsFunc(c.hostKeys, func(k ssh.PublicKey) bool { return strings.Contains(ue.Fix, ssh.FingerprintSHA256(k)) }) {
		t.Errorf("Fix = %q, want the fingerprint of one of the node's host keys", ue.Fix)
	}
}

func TestHelperChangedHostKey(t *testing.T) {
	c := startContainer(t)
	cfg := c.cfg
	cfg.KnownHosts = writeKnownHosts(t, knownhosts.Line([]string{"[127.0.0.1]:" + strconv.Itoa(cfg.Port)}, hostKey(t, "ed25519")))
	_, err := c.connect(t, c.dialer(t, cfg))
	if ue := userError(t, err, "ssh.host_key_changed"); !strings.Contains(ue.Why, ssh.FingerprintSHA256(c.hostKey)) {
		t.Errorf("Why = %q, want the presented fingerprint", ue.Why)
	}
}

// TestHelperParallelUploads: runs that connect to a bare node at once all upload. Each shell
// writes its own temporary file, so none renames or truncates another's half-written binary
// (a shared tops.tmp fails here with "Text file busy" and missing files).
func TestHelperParallelUploads(t *testing.T) {
	c := startContainer(t)
	if _, err := c.connect(t, c.dialer(t, c.cfg)); err != nil { // wait for sshd
		t.Fatal(err)
	}
	for round := range 3 {
		docker(t, nil, "exec", c.id, "rm", "-rf", "/usr/local/lib/tops")
		var wg sync.WaitGroup
		errs := make([]error, 6)
		for i := range errs {
			wg.Go(func() { _, errs[i] = c.connect(t, c.dialer(t, c.cfg)) })
		}
		wg.Wait()
		for i, err := range errs {
			if err != nil {
				t.Errorf("round %d, connect %d: %v", round, i, err)
			}
		}
	}
}
