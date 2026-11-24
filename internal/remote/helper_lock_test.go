//go:build helper

package remote

import (
	"testing"

	"github.com/2SSK/tops/internal/wire"
)

// TestHelperFence: through the real SSH path, a fenced op runs only for the run that holds the
// node's lease. Before any lease, and after another run took it, stage is lock.lost and says
// who holds the node; the holder's stage runs. The lease is root's alone.
func TestHelperFence(t *testing.T) {
	c := startContainer(t)
	d := c.dialer(t, c.cfg)
	n, err := c.connect(t, d)
	if err != nil {
		t.Fatal(err)
	}
	if ue := userError(t, stageErr(t, n), "lock.lost"); ue.Why != "no run holds it" {
		t.Errorf("before any lease: %s: %s", ue.What, ue.Why)
	}

	other, err := c.connectAs(t, d, wire.Request{RunID: "r-helper-other", By: "other@helper-test"})
	if err != nil {
		t.Fatal(err)
	}
	holdLease(t, other)
	if ue := userError(t, stageErr(t, n), "lock.lost"); ue.What != "stage on vps-01: this run does not hold the lock" || ue.Why != "run r-helper-other holds it: test by other@helper-test" {
		t.Errorf("another run's lease: %s: %s", ue.What, ue.Why)
	}
	if err := stageErr(t, other); err != nil {
		t.Errorf("the holder's stage: %v", err)
	}
	if got := docker(t, nil, "exec", c.id, "stat", "-c", "%n %a %U", "/var/lib/tops", "/var/lib/tops/lock"); got != "/var/lib/tops 700 root\n/var/lib/tops/lock 600 root" {
		t.Errorf("node state:\n%s", got)
	}
}

// stageErr is the error of a stage that has nothing to install.
func stageErr(t *testing.T, n *Node) error {
	t.Helper()
	_, err := n.Stage(t.Context(), wire.Stage{})
	return err
}
