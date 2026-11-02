package settings

import (
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/2SSK/tops/internal/spec"
)

// parse is Parse for a test file that must have no problems.
func parse(t *testing.T, file, data string) File {
	t.Helper()
	f, problems, err := Parse(file, []byte(data), load(t))
	if err != nil || len(problems) > 0 {
		t.Fatalf("%s: err %v, problems %v", file, err, problems)
	}
	return f
}

func layers(t *testing.T, common, history, overrides string) Layers {
	t.Helper()
	return Layers{
		Common:    parse(t, "base/common.yaml", common),
		Roles:     map[string]File{"history": parse(t, "base/history.yaml", history)},
		Overrides: parse(t, "environments/lab/overrides.yaml", overrides),
	}
}

func TestMerge(t *testing.T) {
	c := load(t)
	l := layers(t,
		"history.cacheTTL:\n  - value: 1h\nfrontend.namespaceRPS:\n  - value: 100\nlimit.maxIDLength:\n  - value: 500\n",
		"history.cacheTTL:\n  - value: 15m\n",
		"History.CacheTTL:\n  - value: 5m\nfrontend.namespaceRPS:\n  - value: 400\n    constraints: { namespace: drills }\n",
	)

	tests := []struct {
		name, role, key string
		want            []Value // nil: the role does not get the key
	}{
		{"overrides win", "history", "history.cacheTTL", []Value{{Value: "5m"}}},
		{"role routing", "frontend", "history.cacheTTL", nil},
		{"whole-key replace", "frontend", "frontend.namespaceRPS", []Value{{Value: 400, Constraints: map[string]any{"namespace": "drills"}}}},
		{"shared key reaches every role", "matching", "limit.maxIDLength", []Value{{Value: 500}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, ok := l.Merge(tt.role, c)[tt.key]
			if tt.want == nil {
				if ok {
					t.Errorf("%s got %s from %s", tt.role, tt.key, e.File)
				}
				return
			}
			if !ok || !reflect.DeepEqual(e.Values, tt.want) {
				t.Errorf("%s %s = %+v, want %+v", tt.role, tt.key, e.Values, tt.want)
			}
		})
	}
}

func TestCheck(t *testing.T) {
	tests := []struct {
		name                       string
		common, history, overrides string
		errs, warnings             []spec.Problem
	}{
		{name: "clean", history: "history.cacheTTL:\n  - value: 15m\n"},
		{
			name:    "role file key the role does not read",
			history: "frontend.namespaceRPS:\n  - value: 1\n",
			errs:    []spec.Problem{{File: "base/history.yaml", Path: "frontend.namespaceRPS", Line: 1, Message: "history does not read it (read by frontend); move it to the file of a role that does"}},
		},
		{
			name:     "in common and the role file",
			common:   "# shared\nhistory.cacheTTL:\n  - value: 1h\n",
			history:  "history.cacheTTL:\n  - value: 15m\n",
			warnings: []spec.Problem{{File: "base/history.yaml", Path: "history.cacheTTL", Line: 1, Message: "also set in base/common.yaml line 2; this file wins for history"}},
		},
		{
			name:      "drain longer than systemd waits",
			common:    "frontend.shutdownFailHealthCheckDuration:\n  - value: 30s\n",
			overrides: "frontend.shutdownDrainDuration:\n  - value: 60\n",
			errs:      []spec.Problem{{File: "environments/lab/overrides.yaml", Path: "frontend.shutdownDrainDuration", Line: 1, Message: "frontend waits 1m30s to stop (frontend.shutdownFailHealthCheckDuration + frontend.shutdownDrainDuration); systemd kills it after 1m30s (TimeoutStopSec)"}},
		},
		{name: "drain just under", overrides: "history.shutdownDrainDuration:\n  - value: 89s\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs, warnings := layers(t, tt.common, tt.history, tt.overrides).Check(load(t))
			if !slices.Equal(errs, tt.errs) || !slices.Equal(warnings, tt.warnings) {
				t.Errorf("errs %+v\nwarnings %+v\nwant %+v\nand %+v", errs, warnings, tt.errs, tt.warnings)
			}
		})
	}
}

func TestDuration(t *testing.T) {
	tests := []struct {
		in   any
		want time.Duration
	}{
		{"15m", 15 * time.Minute},
		{30, 30 * time.Second},
		{1.5, 1500 * time.Millisecond},
		{true, 0},
	}
	for _, tt := range tests {
		if got := duration(tt.in); got != tt.want {
			t.Errorf("duration(%v) = %v, want %v", tt.in, got, tt.want)
		}
	}
}
