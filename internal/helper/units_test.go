package helper

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/coreos/go-systemd/v22/dbus"

	"github.com/2SSK/tops/internal/wire"
)

// fakeSystemd answers like systemd's D-Bus API from fixed units. A unit's properties are
// those of GetUnitProperties and GetUnitTypeProperties(…, "Service") merged; failOn makes
// one call fail.
type fakeSystemd struct {
	units  []dbus.UnitStatus
	props  map[string]map[string]any
	failOn string
	closed bool
	asked  []string // the patterns ListUnitsByPatterns was given
}

func (f *fakeSystemd) ListUnitsByPatternsContext(_ context.Context, _, patterns []string) ([]dbus.UnitStatus, error) {
	f.asked = patterns
	if f.failOn == "list" {
		return nil, errors.New("connection reset")
	}
	return f.units, nil
}

func (f *fakeSystemd) GetUnitPropertiesContext(_ context.Context, unit string) (map[string]any, error) {
	if f.failOn == "unit" {
		return nil, errors.New("no such unit")
	}
	return f.props[unit], nil
}

func (f *fakeSystemd) GetUnitTypePropertiesContext(_ context.Context, unit, _ string) (map[string]any, error) {
	if f.failOn == "service" {
		return nil, errors.New("no such unit")
	}
	return f.props[unit], nil
}

func (f *fakeSystemd) Close() { f.closed = true }

// since is when the running history unit became active, as systemd's microseconds.
var since = time.Date(2026, 11, 8, 3, 8, 0, 0, time.UTC)

// twoRoles is a node with history running (restarted twice) and an enabled matching unit
// that failed, with the property types systemd really sends: uint32 pids and counters,
// uint64 microsecond timestamps.
func twoRoles() *fakeSystemd {
	return &fakeSystemd{
		units: []dbus.UnitStatus{
			{Name: "temporal@history.service", ActiveState: "active"},
			{Name: "temporal@matching.service", ActiveState: "failed"},
		},
		props: map[string]map[string]any{
			"temporal@history.service": {
				"UnitFileState": "enabled", "ActiveEnterTimestamp": uint64(since.UnixMicro()),
				"MainPID": uint32(4242), "NRestarts": uint32(2),
			},
			"temporal@matching.service": {
				"UnitFileState": "disabled", "ActiveEnterTimestamp": uint64(0),
				"MainPID": uint32(0), "NRestarts": uint32(0),
			},
		},
	}
}

func hostWith(t *testing.T, sd *fakeSystemd) Host {
	t.Helper()
	files := node()
	files["etc/passwd"], files["etc/group"] = "", ""
	h := withAffinity(fakeHost(t, files), 12)
	h.Systemd = func(context.Context) (Systemd, error) { return sd, nil }
	exe := filepath.Join(h.Proc, "4242", "exe")
	if err := os.MkdirAll(filepath.Dir(exe), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/opt/temporal/releases/1.31.3/temporal-server", exe); err != nil {
		t.Fatal(err)
	}
	return h
}

func TestUnits(t *testing.T) {
	sd := twoRoles()
	got, err := hostWith(t, sd).units(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]wire.UnitState{
		"temporal@history.service": {
			Active: "active", Enabled: true, MainPID: 4242, NRestarts: 2, Since: since,
			Exe: "/opt/temporal/releases/1.31.3/temporal-server",
		},
		"temporal@matching.service": {Active: "failed"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
	if !slices.Equal(sd.asked, []string{"temporal@*.service", "temporal-ui.service"}) {
		t.Errorf("asked systemd for %q", sd.asked)
	}
	if !sd.closed {
		t.Error("the D-Bus connection was not closed")
	}
}

func TestUnitsProcessGone(t *testing.T) {
	// MainPID names a process that exited before its exe was read.
	sd := twoRoles()
	sd.props["temporal@history.service"]["MainPID"] = uint32(9999)
	got, err := hostWith(t, sd).units(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if st := got["temporal@history.service"]; st.MainPID != 9999 || st.Exe != "" {
		t.Errorf("got %+v, want the pid and no exe", st)
	}
}

func TestUnitsErrors(t *testing.T) {
	for _, call := range []string{"list", "unit", "service"} {
		t.Run(call, func(t *testing.T) {
			sd := twoRoles()
			sd.failOn = call
			if got, err := hostWith(t, sd).units(t.Context()); err == nil {
				t.Errorf("got %+v, want an error", got)
			}
			if !sd.closed {
				t.Error("the D-Bus connection was not closed")
			}
		})
	}

	t.Run("no systemd", func(t *testing.T) {
		h := hostWith(t, twoRoles())
		h.Systemd = func(context.Context) (Systemd, error) {
			return nil, errors.New("no such file: /run/dbus/system_bus_socket")
		}
		if got, err := h.units(t.Context()); err == nil {
			t.Errorf("got %+v, want an error", got)
		}
	})
}

func TestFactsUnits(t *testing.T) {
	got, err := hostWith(t, twoRoles()).Facts(t.Context(), wire.FactsArgs{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Units) != 2 || got.Units["temporal@history.service"].Active != "active" {
		t.Errorf("units %+v", got.Units)
	}
}
