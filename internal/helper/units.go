package helper

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/2SSK/tops/internal/wire"
	"github.com/coreos/go-systemd/v22/dbus"
)

// unitPatterns are the units facts reports: one temporal@<role> per role, and the UI.
var unitPatterns = []string{"temporal@*.service", "temporal-ui.service"}

// Systemd is the part of systemd's D-Bus API the helper reads. *dbus.Conn has it; tests
// fake it.
type Systemd interface {
	ListUnitsByPatternsContext(ctx context.Context, states, patterns []string) ([]dbus.UnitStatus, error)
	GetUnitPropertiesContext(ctx context.Context, unit string) (map[string]any, error)
	GetUnitTypePropertiesContext(ctx context.Context, unit, unitType string) (map[string]any, error)
	Close()
}

// systemBus connects to systemd on the system bus.
func systemBus(ctx context.Context) (Systemd, error) {
	return dbus.NewSystemConnectionContext(ctx)
}

// units returns every unit systemd has loaded that matches unitPatterns, in any state.
// An enabled unit is laoded even when it never started, so none is missed.
func (h Host) units(ctx context.Context) (map[string]wire.UnitState, error) {
	conn, err := h.Systemd(ctx)
	if err != nil {
		return nil, fmt.Errorf("connect to systemd: %w", err)
	}
	defer conn.Close()

	loaded, err := conn.ListUnitsByPatternsContext(ctx, nil, unitPatterns)
	if err != nil {
		return nil, fmt.Errorf("list units: %w", err)
	}
	units := map[string]wire.UnitState{}
	for _, u := range loaded {
		if units[u.Name], err = h.unitState(ctx, conn, u); err != nil {
			return nil, err
		}
	}
	return units, nil
}

func (h Host) unitState(ctx context.Context, conn Systemd, u dbus.UnitStatus) (wire.UnitState, error) {
	unit, err := conn.GetUnitPropertiesContext(ctx, u.Name)
	if err != nil {
		return wire.UnitState{}, fmt.Errorf("read %s: %w", u.Name, err)
	}
	service, err := conn.GetUnitTypePropertiesContext(ctx, u.Name, "Service")
	if err != nil {
		return wire.UnitState{}, fmt.Errorf("read %s: %w", u.Name, err)
	}
	st := wire.UnitState{
		Active:    u.ActiveState,
		Enabled:   prop[string](unit, "UnitFileState") == "enabled",
		MainPID:   int(prop[uint32](service, "MainPID")),
		NRestarts: int(prop[uint32](service, "NRestarts")),
	}
	if usec := prop[uint64](unit, "ActiveEnterTimestamp"); usec > 0 && usec <= math.MaxInt64 {
		st.Since = time.UnixMicro(int64(usec)).UTC()
	}
	if st.MainPID > 0 {
		// The process may exit between the two reads: then there is no exe to report.
		st.Exe, _ = os.Readlink(filepath.Join(h.Proc, strconv.Itoa(st.MainPID), "exe"))
	}
	return st, nil
}

// prop returns a D-Bus property as T, or T's zero value when it is missing or of another
// type.
func prop[T any](props map[string]any, name string) T {
	v, _ := props[name].(T)
	return v
}
