package helper

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/coreos/go-systemd/v22/dbus"

	"github.com/2SSK/tops/internal/wire"
)

func (f *fakeSystemd) ListUnitsByNamesContext(_ context.Context, names []string) ([]dbus.UnitStatus, error) {
	if f.failOn == "names" {
		return nil, errors.New("connection reset")
	}
	var out []dbus.UnitStatus
	for _, name := range names {
		i := slices.IndexFunc(f.units, func(u dbus.UnitStatus) bool { return u.Name == name })
		if i < 0 {
			out = append(out, dbus.UnitStatus{Name: name, LoadState: "not-found", ActiveState: "inactive"})
			continue
		}
		out = append(out, f.units[i])
	}
	return out, nil
}

// enqueue records a job and, as systemd does on JobRemoved, sends its result.
func (f *fakeSystemd) enqueue(verb, name, mode string, ch chan<- string) (int, error) {
	f.calls = append(f.calls, verb+" "+name+" "+mode)
	if f.failOn == verb {
		return 0, errors.New("no such unit")
	}
	switch f.result {
	case "hang":
	case "":
		ch <- "done"
	default:
		ch <- f.result
	}
	return len(f.calls), nil
}

func (f *fakeSystemd) StartUnitContext(_ context.Context, name, mode string, ch chan<- string) (int, error) {
	return f.enqueue("start", name, mode, ch)
}

func (f *fakeSystemd) StopUnitContext(_ context.Context, name, mode string, ch chan<- string) (int, error) {
	return f.enqueue("stop", name, mode, ch)
}

func (f *fakeSystemd) RestartUnitContext(_ context.Context, name, mode string, ch chan<- string) (int, error) {
	return f.enqueue("restart", name, mode, ch)
}

func (f *fakeSystemd) EnableUnitFilesContext(_ context.Context, files []string, runtime, force bool) (bool, []dbus.EnableUnitFileChange, error) {
	f.calls = append(f.calls, "enable "+strings.Join(files, ",")+map[bool]string{true: " runtime", false: ""}[runtime]+map[bool]string{true: " force", false: ""}[force])
	if f.failOn == "enable" {
		return false, nil, errors.New("no such unit file")
	}
	return true, nil, nil
}

func (f *fakeSystemd) DisableUnitFilesContext(_ context.Context, files []string, runtime bool) ([]dbus.DisableUnitFileChange, error) {
	f.calls = append(f.calls, "disable "+strings.Join(files, ",")+map[bool]string{true: " runtime", false: ""}[runtime])
	if f.failOn == "disable" {
		return nil, errors.New("access denied")
	}
	return nil, nil
}

func (f *fakeSystemd) ReloadContext(context.Context) error {
	f.calls = append(f.calls, "reload")
	if f.failOn == "reload" {
		return errors.New("access denied")
	}
	return nil
}

const historyUnit = "temporal@history.service"

// TestService: each action runs its jobs in "replace" mode and waits for them; every start
// follows a reload, and so does disabling; enabling and disabling are persistent; the result
// is the unit's state after.
func TestService(t *testing.T) {
	for _, tc := range []struct {
		action string
		calls  []string
	}{
		{wire.ServiceStart, []string{"reload", "start " + historyUnit + " replace"}},
		{wire.ServiceStop, []string{"stop " + historyUnit + " replace"}},
		{wire.ServiceRestart, []string{"reload", "restart " + historyUnit + " replace"}},
		{wire.ServiceEnableNow, []string{"enable " + historyUnit, "reload", "start " + historyUnit + " replace"}},
		{wire.ServiceDisableNow, []string{"stop " + historyUnit + " replace", "disable " + historyUnit, "reload"}},
	} {
		t.Run(tc.action, func(t *testing.T) {
			sd := twoRoles()
			st, err := hostWith(t, sd).Service(t.Context(), applyA, wire.Service{Unit: historyUnit, Action: tc.action})
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(sd.calls, tc.calls) {
				t.Errorf("calls = %q, want %q", sd.calls, tc.calls)
			}
			if st.Active != "active" || !st.Enabled || st.MainPID != 4242 || !sd.closed {
				t.Errorf("state = %+v, closed %v", st, sd.closed)
			}
		})
	}
}

// TestServiceFails: a job that ends in anything but "done" is systemd.job_failed with the
// result; a failed call, or a wait that runs out of time, says which step and runs nothing
// after it: no job after a failed reload, and disable-now leaves a unit it could not stop
// enabled.
func TestServiceFails(t *testing.T) {
	for _, tc := range []struct {
		name, action, failOn, result, want string
		calls                              int
	}{
		{"job failed", wire.ServiceStart, "", "failed", codeJobFailed + ": start " + historyUnit + ": the job ended failed", 2},
		{"job timed out", wire.ServiceRestart, "", "timeout", codeJobFailed + ": restart " + historyUnit + ": the job ended timeout", 2},
		{"dependency", wire.ServiceEnableNow, "", "dependency", codeJobFailed + ": start " + historyUnit + ": the job ended dependency", 3},
		{"stop failed", wire.ServiceDisableNow, "", "failed", codeJobFailed + ": stop " + historyUnit + ": the job ended failed", 1},
		{"no unit", wire.ServiceStart, "start", "", "start " + historyUnit + ": no such unit", 2},
		{"no unit file", wire.ServiceEnableNow, "enable", "", "enable " + historyUnit + ": no such unit file", 1},
		{"reload first", wire.ServiceRestart, "reload", "", "daemon-reload: access denied", 1},
		{"enabled, reload", wire.ServiceEnableNow, "reload", "", "daemon-reload: access denied", 2},
		{"disable", wire.ServiceDisableNow, "disable", "", "disable " + historyUnit + ": access denied", 2},
		{"disabled, reload", wire.ServiceDisableNow, "reload", "", "daemon-reload: access denied", 3},
		{"state", wire.ServiceStop, "names", "", "read " + historyUnit + ": connection reset", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sd := twoRoles()
			sd.failOn, sd.result = tc.failOn, tc.result
			_, err := hostWith(t, sd).Service(t.Context(), applyA, wire.Service{Unit: historyUnit, Action: tc.action})
			if got := errText(err); got != tc.want || len(sd.calls) != tc.calls {
				t.Errorf("error %q after %q, want %q after %d calls", got, sd.calls, tc.want, tc.calls)
			}
		})
	}
	t.Run("wait", func(t *testing.T) {
		sd := twoRoles()
		sd.result = "hang"
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
		defer cancel()
		_, err := hostWith(t, sd).Service(ctx, applyA, wire.Service{Unit: historyUnit, Action: wire.ServiceStop})
		if !errors.Is(err, context.DeadlineExceeded) || !strings.HasPrefix(err.Error(), "stop "+historyUnit+": ") {
			t.Errorf("error = %v, want the stop's deadline", err)
		}
	})
	t.Run("connect", func(t *testing.T) {
		h := hostWith(t, twoRoles())
		h.Systemd = func(context.Context) (Systemd, error) { return nil, errors.New("no bus") }
		if _, err := h.Service(t.Context(), applyA, wire.Service{Unit: historyUnit, Action: wire.ServiceStart}); errText(err) != "connect to systemd: no bus" {
			t.Errorf("error = %v", err)
		}
	})
}

// errText is a wire.Error as "code: message: detail", any other error as its text.
func errText(err error) string {
	var we *wire.Error
	if errors.As(err, &we) {
		return we.Code + ": " + we.Message + ": " + we.Detail
	}
	if err == nil {
		return ""
	}
	return err.Error()
}

func serviceRequest(t *testing.T, s wire.Service) string {
	t.Helper()
	args, err := json.Marshal(s)
	must(t, err)
	b, _ := json.Marshal(wire.Request{RunID: "r-1", ApplyID: applyA, By: "ssk@thinkpad", Args: args})
	return string(b)
}

// TestRunServiceRefused: only a role's unit and the five actions, only with an apply id to
// record under; no lease, no job. None of them reaches systemd.
func TestRunServiceRefused(t *testing.T) {
	sd := twoRoles()
	h := hostWith(t, sd)
	h.Now = time.Now
	for _, s := range []wire.Service{
		{Unit: "sshd.service", Action: wire.ServiceStop},
		{Unit: "temporal@.service", Action: wire.ServiceStart},
		{Unit: "temporal@History.service", Action: wire.ServiceStart},
		{Unit: "temporal@history.service.d", Action: wire.ServiceStart},
		{Unit: historyUnit, Action: "reload"},
		{Unit: historyUnit, Action: "mask"},
	} {
		if code, resp, _ := run(t, Env{Host: h, Root: true}, wire.OpService, serviceRequest(t, s)); code != ExitBadRequest || resp.Error.Code != codeBadRequest {
			t.Errorf("%+v: exit %d, %+v, want a bad request", s, code, resp.Error)
		}
	}
	args, _ := json.Marshal(wire.Service{Unit: historyUnit, Action: wire.ServiceRestart})
	noApply, _ := json.Marshal(wire.Request{RunID: "r-1", By: "ssk@thinkpad", Args: args})
	if code, resp, _ := run(t, Env{Host: h, Root: true}, wire.OpService, string(noApply)); code != ExitBadRequest || resp.Error.Code != codeBadRequest {
		t.Errorf("without an apply id: exit %d, %+v, want a bad request", code, resp.Error)
	}
	restart := serviceRequest(t, wire.Service{Unit: historyUnit, Action: wire.ServiceRestart})
	if code, resp, _ := run(t, Env{Host: h, Root: true}, wire.OpService, restart); code != ExitNotOK || resp.Error.Code != codeLockLost {
		t.Errorf("without the lease: exit %d, %+v", code, resp.Error)
	}
	if len(sd.calls) != 0 {
		t.Errorf("refused requests reached systemd: %q", sd.calls)
	}
}

// TestRunService: for the lease holder, the result is the unit's state; a failed job is
// systemd.job_failed.
func TestRunService(t *testing.T) {
	sd := twoRoles()
	h := hostWith(t, sd)
	leased(t, &h, "r-1")
	restart := serviceRequest(t, wire.Service{Unit: historyUnit, Action: wire.ServiceRestart})
	code, resp, _ := run(t, Env{Host: h, Root: true}, wire.OpService, restart)
	var st wire.UnitState
	if code != ExitOK || json.Unmarshal(resp.Result, &st) != nil || st.MainPID != 4242 || !slices.Equal(sd.calls, []string{"reload", "restart " + historyUnit + " replace"}) {
		t.Errorf("exit %d, %+v, %s, calls %q", code, resp.Error, resp.Result, sd.calls)
	}
	sd.result = "failed"
	if code, resp, _ := run(t, Env{Host: h, Root: true}, wire.OpService, restart); code != ExitNotOK || resp.Error.Code != codeJobFailed {
		t.Errorf("a failed job: exit %d, %+v", code, resp.Error)
	}
}
