//go:build e2e

package e2e

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

var (
	labDir  = flag.String("lab", "../../lab", "the VPSLAB checkout")
	topsBin = flag.String("tops", "../../bin/tops", "the tops binary under test")
)

// The lab contract: what the suite may use from VPSLAB.
var (
	nodes      = map[string]string{"vps-01": "10.80.0.11", "vps-02": "10.80.0.12", "vps-03": "10.80.0.13"}
	containers = []string{"vps-01", "vps-02", "vps-03", "vps-pg"}
	secretsEnv = filepath.Join("fleet", "environments", "lab", "secrets.env")
)

func TestMain(m *testing.M) {
	flag.Parse()
	for _, p := range []*string{labDir, topsBin} {
		abs, err := filepath.Abs(*p)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		*p = abs
	}
	if err := checkContract(); err != nil {
		fmt.Fprintln(os.Stderr, "lab contract broken:", err)
		os.Exit(1)
	}
	if err := checkMemory(); err != nil {
		fmt.Fprintln(os.Stderr, "workstation memory:", err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

// checkContract fails before any test when VPSLAB is not what the suite expects.
func checkContract() error {
	if _, err := os.Stat(*topsBin); err != nil {
		return fmt.Errorf("no tops binary: %w (run make build)", err)
	}
	if _, err := os.Stat(filepath.Join(*labDir, "keys", "id_ed25519")); err != nil {
		return fmt.Errorf("no lab key: %w", err)
	}
	if err := checkKnownHosts(); err != nil {
		return err
	}
	for _, c := range containers {
		out, err := exec.CommandContext(context.Background(), "docker", "inspect", "-f", "{{.State.Running}}", c).Output() //nolint:gosec // the contract's container names
		if err != nil || strings.TrimSpace(string(out)) != "true" {
			return fmt.Errorf("container %s is not running (make lab-up)", c)
		}
	}
	if _, err := os.Stat(secretsEnv); err != nil {
		return fmt.Errorf("no %s: %w (make lab-up writes it)", secretsEnv, err)
	}
	return nil
}

// checkKnownHosts: VPSLAB's make up writes a line per node address.
func checkKnownHosts() error {
	known, err := os.ReadFile(filepath.Join(*labDir, "keys", "known_hosts"))
	if err != nil {
		return fmt.Errorf("no known_hosts: %w", err)
	}
	for name, addr := range nodes {
		if !bytes.Contains(known, []byte("\n"+addr+" ")) && !bytes.HasPrefix(known, []byte(addr+" ")) {
			return fmt.Errorf("known_hosts has no line for %s (%s)", name, addr)
		}
	}
	return nil
}

// checkMemory refuses to run when the workstation cannot hold every container at its cap:
// overcommitted, the host reclaims from the nodes and every size tops reads is wrong.
func checkMemory() error {
	var caps, used int64
	for _, c := range containers {
		limit, err := docker("inspect", "-f", "{{.HostConfig.Memory}}", c)
		if err != nil {
			return err
		}
		current, err := docker("exec", c, "cat", "/sys/fs/cgroup/memory.current")
		if err != nil {
			return err
		}
		caps += limit
		used += current
	}
	available, err := memAvailable()
	if err != nil {
		return err
	}
	if grow := caps - used; grow > available {
		return fmt.Errorf("the lab may grow by %d MiB to its caps, but only %d MiB is available", grow>>20, available>>20)
	}
	return nil
}

// docker runs a docker command that prints one number.
func docker(args ...string) (int64, error) {
	out, err := exec.CommandContext(context.Background(), "docker", args...).Output() //nolint:gosec // docker with the contract's names
	if err != nil {
		return 0, fmt.Errorf("docker %s: %w", strings.Join(args, " "), err)
	}
	return strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
}

func memAvailable() (int64, error) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()
	s := bufio.NewScanner(f)
	for s.Scan() {
		if kb, ok := strings.CutPrefix(s.Text(), "MemAvailable:"); ok {
			n, err := strconv.ParseInt(strings.TrimSpace(strings.TrimSuffix(kb, "kB")), 10, 64)
			return n << 10, err
		}
	}
	return 0, errors.New("no MemAvailable in /proc/meminfo")
}

// fleetRoot is a temporary fleet root: test/e2e/fleet with the lab's key paths made
// absolute, plus the lab's secrets.env. edit changes files in it before a test runs.
func fleetRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	err := filepath.WalkDir("fleet", func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(path) //nolint:gosec // the suite's own fixture
		if err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if filepath.Base(path) == "secrets.env" {
			mode = 0o600
		}
		data = bytes.ReplaceAll(data, []byte("LAB/"), []byte(*labDir+"/"))
		dst := filepath.Join(root, strings.TrimPrefix(path, "fleet"+string(filepath.Separator)))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil { //nolint:gosec // a test directory
			return err
		}
		return os.WriteFile(dst, data, mode) //nolint:gosec // under the test's own temporary directory
	})
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// result is one run of tops.
type result struct {
	stdout, stderr string
	exit           int
}

// tops runs the binary under test on the fleet at root.
func tops(t *testing.T, root string, args ...string) result {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), *topsBin, args...) //nolint:gosec // the binary under test
	cmd.Env = append(os.Environ(), "TOPS_ROOT="+root)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	r := result{stdout: stdout.String(), stderr: stderr.String()}
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit):
		r.exit = exit.ExitCode()
	case err != nil:
		t.Fatalf("tops %s: %v", strings.Join(args, " "), err)
	}
	t.Logf("tops %s: exit %d", strings.Join(args, " "), r.exit)
	return r
}

// onNode runs a read-only command on a node as root, over the lab's own SSH config.
func onNode(t *testing.T, node, command string) string {
	t.Helper()
	out, err := exec.CommandContext(t.Context(), "ssh", "-F", filepath.Join(*labDir, "keys", "ssh_config"), node, "sudo -n sh -c '"+command+"'").Output() //nolint:gosec // fixed node names and commands
	if err != nil {
		t.Fatalf("ssh %s %s: %v", node, command, err)
	}
	return string(out)
}
