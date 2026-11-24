//go:build helper

package remote

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/2SSK/tops/internal/wire"
)

// renderedFiles are files as render makes them: a secret root's alone, a role's config the
// temporal group reads, a unit drop-in.
var renderedFiles = wire.Write{Files: []wire.File{
	{Path: "/etc/temporal/temporal.env", Content: []byte("TEMPORAL_DB_PASSWORD=s3cret\n"), Mode: "0600", Owner: "root", Group: "root"},
	{Path: "/etc/temporal/history.yaml", Content: []byte("persistence: {}\n"), Mode: "0640", Owner: "root", Group: "temporal"},
	{Path: "/etc/systemd/system/temporal@history.service.d/limits.conf", Content: []byte("[Service]\nMemoryMax=1G\n"), Mode: "0644", Owner: "root", Group: "root"},
}}

// writeNode is a node with the temporal group, and the run's connection for an apply id,
// holding the lease.
func writeNode(t *testing.T) (*container, *Dialer, *Node) {
	t.Helper()
	c := startContainer(t)
	docker(t, nil, "exec", c.id, "groupadd", "--system", "temporal")
	d := c.dialer(t, c.cfg)
	n := c.apply(t, d, "20261124T120000Z-aaaa")
	holdLease(t, n)
	return c, d, n
}

// apply connects as the test's run, for one apply id.
func (c *container) apply(t *testing.T, d *Dialer, applyID string) *Node {
	t.Helper()
	n, err := c.connectAs(t, d, wire.Request{RunID: "r-helper-write", ApplyID: applyID, By: "make@helper-test"})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// TestHelperWrite: as root on a real node, write puts each file in place with its mode,
// owner and group; a second write changes nothing and saves no snapshot.
func TestHelperWrite(t *testing.T) {
	c, _, n := writeNode(t)
	if r, err := n.Write(t.Context(), renderedFiles); err != nil || len(r.Written) != 3 {
		t.Fatalf("write = %+v, %v", r, err)
	}
	f := renderedFiles.Files
	if got := docker(t, nil, "exec", c.id, "stat", "-c", "%a %U %G %n", f[0].Path, f[1].Path, f[2].Path); got != "600 root root /etc/temporal/temporal.env\n640 root temporal /etc/temporal/history.yaml\n644 root root /etc/systemd/system/temporal@history.service.d/limits.conf" {
		t.Errorf("files:\n%s", got)
	}
	if r, err := n.Write(t.Context(), renderedFiles); err != nil || r.Snapshot != "" || len(r.Written) != 0 {
		t.Errorf("second write = %+v, %v; want no change", r, err)
	}
}

// TestHelperWriteSnapshot: a hand edit is saved in the next apply's snapshot, root's alone,
// before it is overwritten.
func TestHelperWriteSnapshot(t *testing.T) {
	c, d, n := writeNode(t)
	if _, err := n.Write(t.Context(), renderedFiles); err != nil {
		t.Fatal(err)
	}
	docker(t, nil, "exec", c.id, "sh", "-c", "echo 'hand edit' > /etc/temporal/history.yaml")
	if r, err := c.apply(t, d, "20261124T130000Z-bbbb").Write(t.Context(), renderedFiles); err != nil || len(r.Written) != 1 || r.Snapshot != "20261124T130000Z-bbbb" {
		t.Fatalf("write over a hand edit = %+v, %v", r, err)
	}
	sum := sha256.Sum256([]byte("hand edit\n"))
	saved := "/var/lib/tops/snapshots/20261124T130000Z-bbbb/files/" + hex.EncodeToString(sum[:])
	if got := docker(t, nil, "exec", c.id, "cat", saved); got != "hand edit" {
		t.Errorf("saved %q, want the hand edit", got)
	}
	if got := docker(t, nil, "exec", c.id, "stat", "-c", "%a %U", "/var/lib/tops/snapshots/20261124T130000Z-bbbb", saved); got != "700 root\n600 root" {
		t.Errorf("snapshot:\n%s", got)
	}
}
