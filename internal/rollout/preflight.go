// Package rollout changes a cluster one step at a time, each step behind its gate (TRD
// "Orders"). Its preflight decides, before anything changes, whether bootstrap may start.
package rollout

import (
	"fmt"
	"slices"
	"strings"

	"github.com/2SSK/tops/internal/datastore/postgres"
	"github.com/2SSK/tops/internal/render"
	"github.com/2SSK/tops/internal/spec"
	"github.com/2SSK/tops/internal/term"
	"github.com/2SSK/tops/internal/wire"
)

// supportedOS is the only OS tops installs Temporal on.
const supportedOS = "ubuntu 24.04"

// CheckStore refuses a store bootstrap must not touch: one that holds another cluster, or
// whose databases the spec's user cannot create or does not own. resume says the store holds
// / this cluster already: an earlier bootstrap got as far as starting Temporal.
func CheckStore(cluster string, s postgres.Store) (resume bool, err error) {
	for _, c := range s.Clusters {
		if c != cluster {
			return false, &term.UserError{
				Code: "refused.datastore_in_use",
				What: "not bootstrapping",
				Why:  s.Host + " holds cluster " + c,
				Fix:  "another environment owns it: point persistence.postgres at an empty store",
			}
		}
	}
	for _, db := range []string{postgres.Persistence, postgres.Visibility} {
		owner, ok := s.Owners[db]
		switch {
		case !ok && !s.CanCreate:
			return false, &term.UserError{
				Code: "refused.datastore_access",
				What: "not bootstrapping",
				Why:  fmt.Sprintf("%s has no database %s, and %s may not create one", s.Host, db, s.User),
				Fix:  fmt.Sprintf("grant CREATEDB to %s, or create %s and %s owned by it", s.User, postgres.Persistence, postgres.Visibility),
			}
		case ok && owner != s.User:
			return false, &term.UserError{
				Code: "refused.datastore_access",
				What: "not bootstrapping",
				Why:  fmt.Sprintf("%s on %s is owned by %s, not %s: its schema could not be migrated", db, s.Host, owner, s.User),
				Fix:  fmt.Sprintf("ALTER DATABASE %s OWNER TO %s", db, s.User),
			}
		}
	}
	return slices.Contains(s.Clusters, cluster), nil
}

// CheckNode refuses a node bootstrap must not start on: an OS other than Ubuntu 24.04, a
// Temporal unit while the store holds no cluster, a port of its roles that something holds.
// When resuming, the units and a temporal-server on the ports are this cluster's own. An
// unsynchronized clock is a warning: lease ages and log times can be off.
func CheckNode(n spec.Node, h wire.Hello, units map[string]wire.UnitState, resume bool) ([]string, error) {
	not := "not bootstrapping on " + n.Name
	if h.OS != supportedOS {
		return nil, &term.UserError{
			Code: "refused.unsupported_os",
			What: not,
			Why:  h.OS + "; tops supports " + supportedOS,
			Fix:  "use an Ubuntu 24.04 node",
		}
	}
	var found []string
	for unit := range units {
		if strings.HasPrefix(unit, "temporal@") {
			found = append(found, unit)
		}
	}
	if len(found) > 0 && !resume {
		slices.Sort(found)
		verb := "exists"
		if len(found) > 1 {
			verb = "exist"
		}
		return nil, &term.UserError{
			Code: "refused.node_in_use",
			What: not,
			Why:  strings.Join(found, ", ") + " " + verb + " and the store is empty",
			Fix:  "remove Temporal from " + n.Name + ", or point persistence at the store its cluster is in",
		}
	}
	if held := heldPorts(n.Roles, h.Listeners, resume); len(held) > 0 {
		return nil, &term.UserError{
			Code: "refused.ports_in_use",
			What: not,
			Why:  strings.Join(held, "; "),
			Fix:  "free the ports, or move what holds them",
		}
	}
	if !h.ClockSynced {
		return []string{"the clock on " + n.Name + " is not synchronized: lease ages and log times can be off"}, nil
	}
	return nil, nil
}

// heldPorts says, for each port of roles that a listener holds, which process holds it, in
// the order of the roles and their ports.
func heldPorts(roles []string, listeners []wire.Listener, resume bool) []string {
	var held []string
	for _, role := range roles {
		for _, port := range render.PortsOf(role).Listening() {
			i := slices.IndexFunc(listeners, func(l wire.Listener) bool { return l.Port == port })
			if i < 0 || resume && listeners[i].Process == "temporal-server" {
				continue
			}
			by := listeners[i].Process
			if by == "" {
				by = "a process tops cannot see"
			}
			held = append(held, fmt.Sprintf("%d is used by %s", port, by))
		}
	}
	return held
}
