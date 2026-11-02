package settings

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/2SSK/tops/internal/catalog"
	"github.com/2SSK/tops/internal/spec"
)

// Layers are the files that set the dynamic config of an environment's roles, in merge
// order: later wins, a whole key at a time (TD "Dynamic-config merge order").
type Layers struct {
	Common    File            // base/common.yaml
	Roles     map[string]File // base/<role>.yaml, by role
	Overrides File            // environments/<env>/overrides.yaml
}

// Merge is the dynamic config of role: each key role redas, from the last layer that sets it.
// A key's list is replaced, never merged: merging constraint lists would maek values nobody wrote.
func (l Layers) Merge(role string, c *catalog.Catalog) File {
	merged := File{}
	for _, f := range []File{l.Common, l.Roles[role], l.Overrides} {
		for key, e := range f {
			if s, _ := c.Lookup(key); reads(s, role) {
				merged[key] = e
			}
		}
	}
	return merged
}

// Check returns the errors and the warnings of the layers: a key in a role's file that the
// role does not read, a key in both common.yaml and a role's file, and a role that would
// not stop before systemd kills it.
func (l Layers) Check(c *catalog.Catalog) (errs, warnings []spec.Problem) {
	for _, role := range spec.Roles {
		f := l.Roles[role]
		for _, key := range slices.Sorted(maps.Keys(f)) {
			e := f[key]
			s, _ := c.Lookup(key)
			if !reads(s, role) {
				errs = append(errs, problem(e, "%s does not read it (read by %s); move it to the file of a role that does", role, strings.Join(s.Roles, ", ")))
			} else if common, ok := l.Common[key]; ok {
				warnings = append(warnings, problem(e, "also set in %s line %d; this file wins for %s", common.File, common.Line, role))
			}
		}
		if p, ok := l.stop(role, c); !ok {
			errs = append(errs, p)
		}
	}
	return errs, warnings
}

// reads reports whether role reads setting s : its own code does, or code every role shares.
func reads(s catalog.Setting, role string) bool {
	return slices.Contains(s.Roles, role) || slices.Contains(s.Roles, "shared")
}

func problem(e Entry, format string, args ...any) spec.Problem {
	return spec.Problem{File: e.File, Path: e.Key, Line: e.Line, Message: fmt.Sprintf(format, args...)}
}

// StopTimeout is how long systemd waits for a role to stop before it kills it
// (TimeoutStopSec in temporal@.service).
const StopTimeout = 90 * time.Second

// stopKeys are the settings a role waits out when it stops (service/<role>/service.go Stop).
var stopKeys = map[string][]string{
	"frontend": {"frontend.shutdownFailHealthCheckDuration", "frontend.shutdownDrainDuration"},
	"history":  {"history.shutdownDrainDuration"},
	"matching": {"matching.shutdownDrainDuration"},
}

// stop checks that role's merged stop settings add up to less than StopTimeout, so
// systemd never kills a role that is still draining. The problem points at the last
// of them that is set.
func (l Layers) stop(role string, c *catalog.Catalog) (spec.Problem, bool) {
	merged := l.Merge(role, c)
	var total time.Duration
	var last Entry
	for _, key := range stopKeys[role] {
		e, ok := merged[key]
		if !ok {
			continue // Temporal's default is 0s
		}
		total += duration(e.Values[0].Value)
		last = e
	}
	if total < StopTimeout {
		return spec.Problem{}, true
	}
	return problem(last, "%s waits %v to stop (%s); systemd kills it after %v (TimeoutStopSec)", role, total, strings.Join(stopKeys[role], " + "), StopTimeout),
		false
}

// duration is a duration value as Parse kept it: a string, or a number of seconds.
func duration(v any) time.Duration {
	switch v := v.(type) {
	case string:
		d, _ := ParseDuration(v) // Parse checked it
		return d
	case int:
		return time.Duration(v) * time.Second
	case float64:
		return time.Duration(v * float64(time.Second))
	}
	return 0
}
