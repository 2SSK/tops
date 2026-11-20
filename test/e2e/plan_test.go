//go:build e2e

package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The T2 acceptance (implementation plan "T2 acceptance"), one test per row.

// files is every path tops renders for a node with these roles.
func files(roles ...string) []string {
	paths := []string{"/etc/systemd/system/temporal@.service", "/etc/temporal/temporal.env"}
	for _, r := range roles {
		paths = append(paths, "/etc/temporal/"+r+".yaml", "/etc/temporal/"+r+".env",
			"/etc/systemd/system/temporal@"+r+".service.d/limits.conf", "/etc/temporal/dynamicconfig-"+r+".yaml")
	}
	slices.Sort(paths)
	return paths
}

var labRoles = map[string][]string{
	"vps-01": {"frontend", "history", "matching", "worker"},
	"vps-02": {"frontend", "history", "matching", "worker"},
	"vps-03": {"frontend", "matching"},
}

type planJSON struct {
	Changes  bool `json:"changes"`
	Refusals []struct {
		Code string `json:"code"`
	} `json:"refusals"`
	Nodes []struct {
		Name  string `json:"name"`
		Files []struct {
			Path string `json:"path"`
		} `json:"files"`
		Roles []struct {
			Role   string `json:"role"`
			Action string `json:"action"`
		} `json:"roles"`
	} `json:"nodes"`
}

func decode[T any](t *testing.T, r result) T {
	t.Helper()
	var v T
	if err := json.Unmarshal([]byte(r.stdout), &v); err != nil {
		t.Fatalf("not JSON: %v\nstdout: %s\nstderr: %s", err, r.stdout, r.stderr)
	}
	return v
}

// TestCapacityDetectsContainers: the sizes are the containers' cgroup limits, and every
// listed role is sized (4 + 4 + 2).
func TestCapacityDetectsContainers(t *testing.T) {
	r := tops(t, fleetRoot(t), "-e", "lab", "capacity", "--json")
	doc := decode[struct {
		Nodes []struct {
			Name   string `json:"name"`
			Memory struct {
				Source string `json:"source"`
			} `json:"memory"`
			Roles []json.RawMessage `json:"roles"`
		} `json:"nodes"`
	}](t, r)
	roles := 0
	for _, n := range doc.Nodes {
		roles += len(n.Roles)
		if !strings.HasPrefix(n.Memory.Source, "cgroup") {
			t.Errorf("%s memory from %q, want the container's cgroup", n.Name, n.Memory.Source)
		}
	}
	if r.exit != 0 || roles != 10 {
		t.Errorf("exit %d, %d roles sized, want 0 and 10\n%s", r.exit, roles, r.stderr)
	}
}

// TestPlanListsEveryFile: every file of every node, every role started, exit 2. On a reset
// lab every file is a create; on a lab with files left by hand some are updates, and both
// count: the plan must name every file.
func TestPlanListsEveryFile(t *testing.T) {
	r := tops(t, fleetRoot(t), "-e", "lab", "plan", "--json")
	doc := decode[planJSON](t, r)
	if r.exit != 2 || !doc.Changes || len(doc.Refusals) != 0 || len(doc.Nodes) != 3 {
		t.Fatalf("exit %d, changes %v, refusals %v, %d nodes\n%s", r.exit, doc.Changes, doc.Refusals, len(doc.Nodes), r.stderr)
	}
	for _, n := range doc.Nodes {
		var paths []string
		for _, f := range n.Files {
			paths = append(paths, f.Path)
		}
		slices.Sort(paths)
		if want := files(labRoles[n.Name]...); !slices.Equal(paths, want) {
			t.Errorf("%s plans\n%v\nwant\n%v", n.Name, paths, want)
		}
		for _, role := range n.Roles {
			if role.Action != "start" {
				t.Errorf("%s %s: %s, want start (tops never enabled it)", n.Name, role.Role, role.Action)
			}
		}
	}
}

// snapshot is every file and symlink under the directories tops manages on a node, by
// sha256 and target, and what is left in /run/tops.
func snapshot(t *testing.T, node string) (state, run string) {
	t.Helper()
	state = onNode(t, node, `for d in /etc/temporal /opt/temporal /var/lib/tops; do [ -e $d ] || continue; find $d -xdev -type f -exec sha256sum {} +; find $d -xdev -type l -printf "%p -> %l\n"; done | sort`)
	run = onNode(t, node, `ls -A /run/tops 2>/dev/null || true`)
	return state, run
}

// TestPlanTouchesNothing is H1: plan writes nothing on a node but the helper (and its run
// directory, removed): every managed file and link is the same after, /run/tops is empty.
func TestPlanTouchesNothing(t *testing.T) {
	before := map[string]string{}
	for node := range nodes {
		before[node], _ = snapshot(t, node)
	}
	if r := tops(t, fleetRoot(t), "-e", "lab", "plan"); r.exit != 2 {
		t.Fatalf("plan exit %d, want 2\n%s", r.exit, r.stderr)
	}
	for node := range nodes {
		after, run := snapshot(t, node)
		if after != before[node] {
			t.Errorf("plan changed %s:\nbefore\n%s\nafter\n%s", node, before[node], after)
		}
		if run != "" {
			t.Errorf("plan left /run/tops on %s: %s", node, run)
		}
	}
}

// TestHelperUploadedOnce: the second plan of this build uploads nothing. The upload is the one
// sh -c command tops sends (H2), so counting it in --debug counts the uploads.
func TestHelperUploadedOnce(t *testing.T) {
	root := fleetRoot(t)
	tops(t, root, "-e", "lab", "plan")
	r := tops(t, root, "-e", "lab", "plan", "--debug")
	if n := strings.Count(r.stderr, "sudo -n sh -c"); n != 0 || !strings.Contains(r.stderr, "node hello") {
		t.Errorf("%d uploads in the second plan, want 0\n%s", n, r.stderr)
	}
}

// TestNodeTooSmallRefused: vps-03 pins matching above what its 768 MiB cap can hold: plan
// refuses with exit 3, whatever the final caps are.
func TestNodeTooSmallRefused(t *testing.T) {
	root := fleetRoot(t)
	lab, small := filepath.Join(root, "environments", "lab"), filepath.Join(root, "environments", "small")
	if err := os.CopyFS(small, os.DirFS(lab)); err != nil {
		t.Fatal(err)
	}
	edit(t, filepath.Join(small, "cluster.yaml"), "    roles: [frontend, matching]\n", "    roles: [frontend, matching]\n    pin: { matching: { memoryMax: 4G } }\n")
	if err := os.Chmod(filepath.Join(small, "secrets.env"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := tops(t, root, "-e", "small", "plan", "--json")
	doc := decode[planJSON](t, r)
	if r.exit != 3 || len(doc.Refusals) == 0 || doc.Refusals[0].Code != "refused.node_too_small" {
		t.Errorf("exit %d, refusals %v, want 3 and refused.node_too_small\n%s", r.exit, doc.Refusals, r.stderr)
	}
}

// TestUnknownHostKeyRefused: with vps-02's line gone from known_hosts tops will not talk to
// it, and plan fails before anything else: exit 1, ssh.host_key_unknown.
func TestUnknownHostKeyRefused(t *testing.T) {
	root := fleetRoot(t)
	known, err := os.ReadFile(filepath.Join(*labDir, "keys", "known_hosts"))
	if err != nil {
		t.Fatal(err)
	}
	var kept []string
	for _, line := range strings.SplitAfter(string(known), "\n") {
		if !strings.HasPrefix(line, "10.80.0.12 ") {
			kept = append(kept, line)
		}
	}
	copied := filepath.Join(root, "known_hosts")
	if err := os.WriteFile(copied, []byte(strings.Join(kept, "")), 0o600); err != nil { //nolint:gosec // under the test's own temporary directory
		t.Fatal(err)
	}
	edit(t, filepath.Join(root, "environments", "lab", "cluster.yaml"), "knownHosts: "+*labDir+"/keys/known_hosts", "knownHosts: "+copied)
	r := tops(t, root, "-e", "lab", "plan", "--json")
	doc := decode[struct {
		Error struct {
			Code string `json:"code"`
			What string `json:"what"`
		} `json:"error"`
	}](t, r)
	if r.exit != 1 || doc.Error.Code != "ssh.host_key_unknown" || !strings.Contains(doc.Error.What, "vps-02") {
		t.Errorf("exit %d, error %+v, want 1 and ssh.host_key_unknown for vps-02", r.exit, doc.Error)
	}
}

// edit replaces old with replacement in file, and fails if old is not there exactly once.
func edit(t *testing.T, file, old, replacement string) {
	t.Helper()
	data, err := os.ReadFile(file) //nolint:gosec // a file of the test's own fleet
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), old) != 1 {
		t.Fatalf("%s has %q %d times, want once", file, old, strings.Count(string(data), old))
	}
	if err := os.WriteFile(file, []byte(strings.Replace(string(data), old, replacement, 1)), 0o600); err != nil { //nolint:gosec // a file of the test's own fleet
		t.Fatal(err)
	}
}
