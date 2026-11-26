package helper

import (
	"context"
	"fmt"
	"regexp"
	"slices"

	"github.com/2SSK/tops/internal/wire"
)

const codeJobFailed = "systemd.job_failed"

// roleUnit is a role's instance of temporal@.service: the only units service acts on.
var roleUnit = regexp.MustCompile(`^temporal@[a-z]+\.service$`)

// jobStart enqueues one systemd job; go-systemd sends its result on ch when it ends.
type jobStart func(ctx context.Context, name, mode string, ch chan<- string) (int, error)

// Service acts on a role's unit and waits for every job it starts to end. A job that ends in
// anything but "done" is systemd.job_failed, although the D-Bus call itself succeeded.
func (h Host) Service(ctx context.Context, s wire.Service) (wire.UnitState, error) {
	conn, err := h.Systemd(ctx)
	if err != nil {
		return wire.UnitState{}, fmt.Errorf("connect to systemd: %w", err)
	}
	defer conn.Close()
	if err := act(ctx, conn, s); err != nil {
		return wire.UnitState{}, err
	}
	loaded, err := conn.ListUnitsByNamesContext(ctx, []string{s.Unit})
	if err != nil {
		return wire.UnitState{}, fmt.Errorf("read %s: %w", s.Unit, err)
	}
	return h.unitState(ctx, conn, loaded[0]) // one status for each name, loaded or not
}

// act runs's jobs. Enabling and disabling change the unit's links, which systemd sees only
// after a reload, as systemctl does. disable-now stops first: a stop that fails leaves the
// unit as it was, enabled.
func act(ctx context.Context, conn Systemd, s wire.Service) error {
	switch s.Action {
	case wire.ServiceStart:
		return job(ctx, conn.StartUnitContext, "start", s.Unit)
	case wire.ServiceStop:
		return job(ctx, conn.StopUnitContext, "stop", s.Unit)
	case wire.ServiceRestart:
		return job(ctx, conn.RestartUnitContext, "restart", s.Unit)
	case wire.ServiceEnableNow:
		if _, _, err := conn.EnableUnitFilesContext(ctx, []string{s.Unit}, false, false); err != nil {
			return fmt.Errorf("enable %s: %w", s.Unit, err)
		}
		if err := conn.ReloadContext(ctx); err != nil {
			return fmt.Errorf("daemon-reload: %w", err)
		}
		return job(ctx, conn.StartUnitContext, "start", s.Unit)
	default: // disable-now: checkService let nothing else through
		if err := job(ctx, conn.StopUnitContext, "stop", s.Unit); err != nil {
			return err
		}
		if _, err := conn.DisableUnitFilesContext(ctx, []string{s.Unit}, false); err != nil {
			return fmt.Errorf("disable %s: %w", s.Unit, err)
		}
		if err := conn.ReloadContext(ctx); err != nil {
			return fmt.Errorf("daemon-reload: %w", err)
		}
		return nil
	}
}

// job starts one job and waits for its result. ch has room for it: go-systemd sends the
// result while it holds its own lock, and after a timeout nobody is left to read it.
func job(ctx context.Context, start jobStart, verb, unit string) error {
	ch := make(chan string, 1)
	if _, err := start(ctx, unit, "replace", ch); err != nil {
		return fmt.Errorf("%s %s: %w", verb, unit, err)
	}
	select {
	case result := <-ch:
		if result != "done" {
			return &wire.Error{Code: codeJobFailed, Message: verb + " " + unit, Detail: "the job ended " + result}
		}
		return nil
	case <-ctx.Done():
		return fmt.Errorf("%s %s: %w", verb, unit, context.Cause(ctx))
	}
}

// checkService refuses a unit that is not a role's, and an action service does not have.
func checkService(s wire.Service) error {
	if !roleUnit.MatchString(s.Unit) {
		return fmt.Errorf("unit %q is not a role's temporal@<role>.service", s.Unit)
	}
	if !slices.Contains([]string{wire.ServiceStart, wire.ServiceStop, wire.ServiceRestart, wire.ServiceEnableNow, wire.ServiceDisableNow}, s.Action) {
		return fmt.Errorf("action %q is not one of start, stop, restart, enable-now, disable-now", s.Action)
	}
	return nil
}
