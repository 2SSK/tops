//go:build helper

package remote

import (
	"strings"
	"testing"

	"github.com/2SSK/tops/internal/wire"
)

// testTemplate stands in for temporal@.service: a role that runs until it is stopped. The
// worker's drop-in fails every start, as a role whose config does not load.
const testTemplate = `[Unit]
Description=Temporal %i (helper test)

[Service]
ExecStart=/usr/bin/sleep infinity

[Install]
WantedBy=multi-user.target
`

// serviceNode is a node with the test template and a failing worker, the run holding the
// lease.
func serviceNode(t *testing.T) (*container, *Node) {
	t.Helper()
	c := startContainer(t)
	docker(t, []byte(testTemplate), "exec", "-i", c.id, "sh", "-c", "cat > /etc/systemd/system/temporal@.service")
	docker(t, nil, "exec", c.id, "sh", "-c", "mkdir /etc/systemd/system/temporal@worker.service.d && printf '[Service]\\nExecStartPre=/bin/false\\n' > /etc/systemd/system/temporal@worker.service.d/fail.conf")
	docker(t, nil, "exec", c.id, "systemctl", "daemon-reload")
	n := c.apply(t, c.dialer(t, c.cfg), "20261126T100000Z-aaaa")
	holdLease(t, n)
	return c, n
}

// unitFile is what systemd itself says of a unit: whether it runs, and starts at boot.
func unitFile(t *testing.T, c *container, unit string) string {
	t.Helper()
	return strings.ReplaceAll(docker(t, nil, "exec", c.id, "systemctl", "show", unit, "-p", "ActiveState", "-p", "UnitFileState", "--value"), "\n", " ")
}

// TestHelperService: as root on a real node, each action leaves the unit as systemd itself
// then reports it, and returns that state; a restart is a new process.
func TestHelperService(t *testing.T) {
	c, n := serviceNode(t)
	var last wire.UnitState
	for _, step := range []struct {
		action, systemd string
		ok              func(st wire.UnitState) bool
	}{
		{wire.ServiceEnableNow, "active enabled", func(st wire.UnitState) bool {
			return st.Enabled && st.MainPID > 0 && st.Exe == "/usr/bin/sleep" && !st.Since.IsZero()
		}},
		{wire.ServiceRestart, "active enabled", func(st wire.UnitState) bool { return st.MainPID > 0 && st.MainPID != last.MainPID }},
		{wire.ServiceStop, "inactive enabled", func(st wire.UnitState) bool { return st.MainPID == 0 }},
		{wire.ServiceStart, "active enabled", func(st wire.UnitState) bool { return st.MainPID > 0 }},
		{wire.ServiceDisableNow, "inactive disabled", func(st wire.UnitState) bool { return !st.Enabled }},
	} {
		st, err := n.Service(t.Context(), wire.Service{Unit: "temporal@history.service", Action: step.action})
		if err != nil {
			t.Fatalf("%s: %v", step.action, err)
		}
		if active, _, _ := strings.Cut(step.systemd, " "); st.Active != active || !step.ok(st) {
			t.Errorf("%s = %+v, after %+v", step.action, st, last)
		}
		// matching is never touched: an instance's job is its own.
		if got := unitFile(t, c, "temporal@history.service") + ", " + unitFile(t, c, "temporal@matching.service"); got != step.systemd+", inactive disabled" {
			t.Errorf("after %s systemd says %q, want history %q", step.action, got, step.systemd)
		}
		last = st
	}
}

// TestHelperServiceFailed: a start whose job systemd ends "failed" is systemd.job_failed,
// although the D-Bus call that queued it succeeded.
func TestHelperServiceFailed(t *testing.T) {
	_, n := serviceNode(t)
	_, err := n.Service(t.Context(), wire.Service{Unit: "temporal@worker.service", Action: wire.ServiceStart})
	if ue := userError(t, err, "systemd.job_failed"); ue.What != "service on vps-01: start temporal@worker.service" || ue.Why != "the job ended failed" {
		t.Errorf("error = %+v", ue)
	}
}
