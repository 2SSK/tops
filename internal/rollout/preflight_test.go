package rollout

import (
	"errors"
	"slices"
	"testing"

	"github.com/2SSK/tops/internal/datastore/postgres"
	"github.com/2SSK/tops/internal/spec"
	"github.com/2SSK/tops/internal/term"
	"github.com/2SSK/tops/internal/wire"
)

// refused is err's code and why, failing the test when err is not a *term.UserError.
func refused(t *testing.T, err error) (code, why string) {
	t.Helper()
	var ue *term.UserError
	if !errors.As(err, &ue) {
		t.Fatalf("err = %v, want a *term.UserError", err)
	}
	return ue.Code, ue.Why
}

// empty is a store before any bootstrap: the image's "temporal" database, owned by the
// spec's user, who may create the other.
func empty() postgres.Store {
	return postgres.Store{Host: "vps-pg", User: "temporal", CanCreate: true, Owners: map[string]string{postgres.Persistence: "temporal"}}
}

func TestCheckStore(t *testing.T) {
	for _, tc := range []struct {
		name   string
		store  func(s *postgres.Store)
		resume bool
		code   string
		why    string
	}{
		{name: "empty", store: func(*postgres.Store) {}},
		{name: "this cluster", store: func(s *postgres.Store) { s.Clusters = []string{"tops-e2e"} }, resume: true},
		{name: "another cluster", store: func(s *postgres.Store) { s.Clusters = []string{"tops-e2e", "tscale-lab"} },
			code: "refused.datastore_in_use", why: "vps-pg holds cluster tscale-lab"},
		{name: "may not create", store: func(s *postgres.Store) { s.CanCreate = false },
			code: "refused.datastore_access", why: "vps-pg has no database temporal_visibility, and temporal may not create one"},
		{name: "both made for it", store: func(s *postgres.Store) { s.CanCreate, s.Owners[postgres.Visibility] = false, "temporal" }},
		{name: "another owner", store: func(s *postgres.Store) { s.Owners[postgres.Visibility] = "postgres" },
			code: "refused.datastore_access", why: "temporal_visibility on vps-pg is owned by postgres, not temporal: its schema could not be migrated"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := empty()
			tc.store(&s)
			resume, err := CheckStore("tops-e2e", s)
			if tc.code == "" {
				if err != nil || resume != tc.resume {
					t.Fatalf("resume %v, %v; want %v", resume, err, tc.resume)
				}
				return
			}
			if code, why := refused(t, err); code != tc.code || why != tc.why || resume {
				t.Errorf("%s: %s (resume %v)", code, why, resume)
			}
		})
	}
}

// vps01 runs every role; vps03 frontend and matching only.
var (
	vps01 = spec.Node{Name: "vps-01", Address: "10.80.0.11", Roles: []string{"frontend", "history", "matching", "worker"}}
	vps03 = spec.Node{Name: "vps-03", Address: "10.80.0.13", Roles: []string{"frontend", "matching"}}
)

// fresh is a node as the lab makes it: Ubuntu 24.04, sshd, a synchronized clock.
func fresh(listeners ...wire.Listener) wire.Hello {
	return wire.Hello{OS: "ubuntu 24.04", ClockSynced: true, Listeners: append([]wire.Listener{{Port: 22, Address: "0.0.0.0", Process: "sshd"}}, listeners...)}
}

func TestCheckNode(t *testing.T) {
	temporal := map[string]wire.UnitState{"temporal@history.service": {Active: "active"}, "temporal@frontend.service": {Active: "active"}}
	running := fresh(wire.Listener{Port: 7233, Address: "10.80.0.11", Process: "temporal-server"})
	for _, tc := range []struct {
		name   string
		node   spec.Node
		hello  wire.Hello
		units  map[string]wire.UnitState
		resume bool
		code   string
		why    string
	}{
		{name: "fresh", node: vps01, hello: fresh()},
		{name: "debian", node: vps01, hello: wire.Hello{OS: "debian 12", ClockSynced: true},
			code: "refused.unsupported_os", why: "debian 12; tops supports ubuntu 24.04"},
		{name: "temporal there, store empty", node: vps01, hello: running, units: temporal,
			code: "refused.node_in_use", why: "temporal@frontend.service, temporal@history.service exist and the store is empty"},
		{name: "one unit", node: vps01, hello: fresh(), units: map[string]wire.UnitState{"temporal@worker.service": {}, "temporal-ui.service": {}},
			code: "refused.node_in_use", why: "temporal@worker.service exists and the store is empty"},
		{name: "the ui alone is not Temporal", node: vps01, hello: fresh(), units: map[string]wire.UnitState{"temporal-ui.service": {}}},
		{name: "resume onto its own", node: vps01, hello: running, units: temporal, resume: true},
		{name: "ports held", node: vps01, hello: fresh(
			wire.Listener{Port: 7233, Address: "0.0.0.0", Process: "nginx"}, wire.Listener{Port: 7233, Address: "::", Process: "nginx"},
			wire.Listener{Port: 9234, Address: "0.0.0.0"}),
			code: "refused.ports_in_use", why: "7233 is used by nginx; 9234 is used by a process tops cannot see"},
		{name: "a port of a role the node does not run", node: vps03, hello: fresh(wire.Listener{Port: 7234, Address: "0.0.0.0", Process: "nginx"})},
		{name: "temporal-server not resuming", node: vps01, hello: running,
			code: "refused.ports_in_use", why: "7233 is used by temporal-server"},
		{name: "resume, another process", node: vps01, hello: fresh(wire.Listener{Port: 6935, Address: "0.0.0.0", Process: "nginx"}), resume: true,
			code: "refused.ports_in_use", why: "6935 is used by nginx"},
		{name: "OS first", node: vps01, hello: wire.Hello{OS: "debian 12", Listeners: running.Listeners}, units: temporal,
			code: "refused.unsupported_os", why: "debian 12; tops supports ubuntu 24.04"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			warnings, err := CheckNode(tc.node, tc.hello, tc.units, tc.resume)
			if tc.code == "" {
				if err != nil || len(warnings) > 0 {
					t.Fatalf("%q, %v; want it to pass", warnings, err)
				}
				return
			}
			if code, why := refused(t, err); code != tc.code || why != tc.why {
				t.Errorf("%s: %s", code, why)
			}
			if warnings != nil {
				t.Errorf("a refusal with warnings %q", warnings)
			}
		})
	}
}

func TestCheckNodeClock(t *testing.T) {
	h := fresh()
	h.ClockSynced = false
	warnings, err := CheckNode(vps01, h, nil, false)
	want := []string{"the clock on vps-01 is not synchronized: lease ages and log times can be off"}
	if err != nil || !slices.Equal(warnings, want) {
		t.Errorf("%q, %v; want %q", warnings, err, want)
	}
}
