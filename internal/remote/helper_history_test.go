//go:build helper

package remote

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/2SSK/tops/internal/wire"
)

// TestHelperHistory: on a real node, the run's record is kept root's alone, named by the
// run's apply id, and says its snapshot is there; without the lease append is lock.lost and
// list still answers.
func TestHelperHistory(t *testing.T) {
	c, _, n := writeNode(t)
	if _, err := n.Write(t.Context(), renderedFiles); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(renderedFiles.Files[0].Content)
	r := wire.History{ApplyID: "20261124T130000Z-bbbb", RunID: "r-other", Command: "apply", By: "make@helper-test", Node: "vps-01",
		Changes: []wire.Change{{Path: "/etc/temporal/temporal.env", Action: wire.ChangeCreate, AfterSHA256: hex.EncodeToString(sum[:])}},
		Result:  wire.ResultOK}
	if err := n.AppendHistory(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	records, err := n.ListHistory(t.Context(), 0)
	if err != nil || len(records) != 1 || records[0].ApplyID != "20261124T120000Z-aaaa" || records[0].RunID != "r-helper-write" || !records[0].Snapshot {
		t.Errorf("list = %+v, %v", records, err)
	}
	if got := docker(t, nil, "exec", c.id, "stat", "-c", "%a %U %G", "/var/lib/tops/history", "/var/lib/tops/history/20261124T120000Z-aaaa.json"); got != "700 root root\n600 root root" {
		t.Errorf("history:\n%s", got)
	}
	withoutLease(t, n, r)
}

// withoutLease: once the run released the node, append is lock.lost and list still answers.
func withoutLease(t *testing.T, n *Node, r wire.History) {
	t.Helper()
	if _, err := n.Lock(t.Context(), wire.Lock{Action: wire.LockRelease}); err != nil {
		t.Fatal(err)
	}
	_ = userError(t, n.AppendHistory(t.Context(), r), "lock.lost")
	if _, err := n.ListHistory(t.Context(), 0); err != nil {
		t.Errorf("list without the lease: %v", err)
	}
}
