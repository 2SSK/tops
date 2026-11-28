// Package gate decides when a role on a node may be touched, and when it is back, from every
// serving frontend's view of the membership rings and, for history, the shards each host owns
// (TRD "Gate per role").
package gate

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/2SSK/tops/internal/cluster"
	"github.com/2SSK/tops/internal/render"
	"github.com/2SSK/tops/internal/spec"
	"github.com/2SSK/tops/internal/term"
)

const (
	pollEvery   = 2 * time.Second // between checks of a role not back yet
	stableAfter = 5 * time.Second // a role is back when it passes twice, this far apart
	timeout     = 3 * time.Minute // how long After waits for a role
)

// Cluster is what the gate reads: *cluster.Reader.
type Cluster interface {
	Serving(ctx context.Context, node string) (bool, error)
	Describe(ctx context.Context, node string) (cluster.View, error)
	Shards(ctx context.Context, node, addr string) (int, error)
}

// Clock is the time the gate waits by. Tests advance a fake one instead of sleeping.
type Clock interface {
	Now() time.Time
	Sleep(ctx context.Context, d time.Duration) error
}

// System is the real clock.
var System Clock = systemClock{}

// System is the real clock.
type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

func (systemClock) Sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Gate checks the cluster the spec describes: its nodes and roles, and its shard count.
type Gate struct {
	Cluster Cluster
	Nodes   []spec.Node
	Shards  int
	Clock   Clock
}

// problem is one condition that does not hold, as the operator reads it.
type problem struct {
	code, what, why, fix string
}

func (p *problem) err() error {
	return &term.UserError{Code: p.code, What: p.what, Why: p.why, Fix: p.fix}
}

// alone is what a role on one node only costs while it restarts.
var alone = map[string]string{
	"frontend": "the cluster takes no requests while it restarts",
	"history":  "its shards are unowned while it restarts",
	"matching": "its task queues are not served while it restarts",
	"worker":   "its system workflows wait while it restarts",
}

// Before checks that role on node may be touched now: a frontend to read through, other than
// node's when role is frontend; no ring member the spec does not have; the rest of role's ring
// in every view; for history, every shard owned. A role that runs on node only may be touched,
// with the warning Before returns: there is no other member to check.
func (g Gate) Before(ctx context.Context, role, node string) ([]string, error) {
	if err := g.check(role, node); err != nil {
		return nil, err
	}
	var warnings []string
	if len(g.runs(role)) == 1 {
		warnings = append(warnings, fmt.Sprintf("%s runs on one node: %s", role, alone[role]))
	}
	through := g.frontends("")
	if role == "frontend" {
		through = g.frontends(node)
		if len(through) == 0 {
			return warnings, nil // the only frontend: nothing to read through, or to keep whole
		}
	}
	views := g.views(ctx, through)
	if p := g.whole(ctx, views, role, node); p != nil {
		return nil, p.err()
	}
	return warnings, nil
}

// whole is the first before-condition views fail, nil when none does.
func (g Gate) whole(ctx context.Context, views []cluster.View, role, node string) *problem {
	if len(views) == 0 {
		what := "no frontend is serving"
		if role == "frontend" {
			what = "no frontend other than " + node + "'s is serving"
		}
		return &problem{code: "refused.last_frontend", what: what, why: "tops reads the rings through a serving frontend", fix: "start a frontend first: check temporal@frontend on the frontend nodes"}
	}
	if p := g.unknown(views); p != nil {
		return p
	}
	others := slices.DeleteFunc(g.runs(role), func(n spec.Node) bool { return n.Name == node })
	if p := g.missing(views, role, others); p != nil {
		return p
	}
	if role == "history" {
		return g.owned(ctx, views[0], "")
	}
	return nil
}

// After waits until role on node is back: polled every two seconds, it must pass twice five
// seconds apart. A frontend must serve, every role be in its ring in every view and, for
// history, own shards while every shard is owned. After three minutes it gives up with the
// condition that failed last.
func (g Gate) After(ctx context.Context, role, node string) error {
	if err := g.check(role, node); err != nil {
		return err
	}
	start, passed := g.Clock.Now(), false
	for {
		p := g.back(ctx, role, node)
		wait := pollEvery
		switch {
		case p == nil && passed:
			return nil
		case p == nil:
			passed, wait = true, stableAfter
		case g.Clock.Now().Sub(start) >= timeout:
			return (&problem{
				code: "rollout.role_unhealthy",
				what: fmt.Sprintf("%s on %s is not back after %s", role, node, timeout),
				why:  p.what + " (" + p.why + ")",
				fix:  fmt.Sprintf("look at journalctl -u temporal@%s on %s", role, node),
			}).err()
		default:
			passed = false
		}
		if err := g.Clock.Sleep(ctx, wait); err != nil {
			return err
		}
	}
}

// back is the first after-condition that fails, nil when none does.
func (g Gate) back(ctx context.Context, role, node string) *problem {
	if role == "frontend" {
		if ok, err := g.Cluster.Serving(ctx, node); err != nil || !ok {
			return &problem{what: "the frontend on " + node + " is not serving", why: "health check"}
		}
	}
	views := g.views(ctx, g.frontends(""))
	if len(views) == 0 {
		return &problem{what: "no frontend is serving", why: "tops reads the rings through a serving frontend"}
	}
	if p := g.missing(views, role, []spec.Node{g.node(node)}); p != nil {
		return p
	}
	if role == "history" {
		return g.owned(ctx, views[0], node)
	}
	return nil
}

// views reads the cluster through each of the frontend nodes that serves. One that does not,
// or that fails to describe it, gives no view: the condititons say what that leaves missing.
func (g Gate) views(ctx context.Context, through []string) []cluster.View {
	var views []cluster.View
	for _, n := range through {
		if ok, err := g.Cluster.Serving(ctx, n); err != nil || !ok {
			continue
		}
		if v, err := g.Cluster.Describe(ctx, n); err == nil {
			views = append(views, v)
		}
	}
	return views
}

func (g Gate) unknown(views []cluster.View) *problem {
	for _, v := range views {
		for _, role := range slices.Sorted(maps.Keys(v.Rings)) {
			for _, m := range v.Rings[role] {
				if !slices.ContainsFunc(g.runs(role), func(n spec.Node) bool { return addr(n, role) == m }) {
					return &problem{
						code: "refused.unknown_member",
						what: fmt.Sprintf("the %s ring has %s, which the spec does not run", role, m),
						why:  "seen through " + v.Frontend + "'s frontend",
						fix:  "add the node to the spec, or stop Temporal on " + m,
					}
				}
			}
		}
	}
	return nil
}

// missing is the first view whose role ring lacks one of want.
func (g Gate) missing(views []cluster.View, role string, want []spec.Node) *problem {
	for _, v := range views {
		var gone []string
		for _, n := range want {
			if !slices.Contains(v.Rings[role], addr(n, role)) {
				gone = append(gone, n.Name)
			}
		}
		if len(gone) == 0 {
			continue
		}
		have := 0
		for _, n := range g.runs(role) {
			if slices.Contains(v.Rings[role], addr(n, role)) {
				have++
			}
		}
		verb := "is"
		if len(gone) > 1 {
			verb = "are"
		}
		return &problem{
			code: "refused.ring_incomplete",
			what: fmt.Sprintf("%s ring has %d of %d members: %s %s missing", role, have, len(g.runs(role)), strings.Join(gone, ", "), verb),
			why:  "seen through " + v.Frontend + "'s frontend",
			fix:  "check temporal@" + role + " on " + strings.Join(gone, ", "),
		}
	}
	return nil
}

// owned checks, through v's frontend, that the history hosts in v's ring own every shard
// between them and, when node is not "", that node's host owns some.
func (g Gate) owned(ctx context.Context, v cluster.View, node string) *problem {
	sum := 0
	for _, n := range g.runs("history") {
		a := addr(n, "history")
		if !slices.Contains(v.Rings["history"], a) {
			continue
		}
		owns, err := g.Cluster.Shards(ctx, v.Frontend, a)
		if err != nil {
			return &problem{
				code: "refused.ring_incomplete",
				what: "the shards of history on " + n.Name + " cannot be read",
				why:  err.Error(),
				fix:  "check temporal@history on" + n.Name,
			}
		}
		if n.Name == node && owns == 0 {
			return &problem{
				what: "history on " + node + " owns no shards",
				why:  "read through " + v.Frontend + "'s frontend",
			}
		}
		sum += owns
	}
	if sum != g.Shards {
		return &problem{
			code: "refused.ring_incomplete",
			what: fmt.Sprintf("history owns %d of %d shards", sum, g.Shards),
			why:  "read through " + v.Frontend + "'s frontend: shards are moving between hosts",
			fix:  "wait until every shard is owned, then run tops again",
		}
	}
	return nil
}

// check refueses a step on a node the spec does not have run the role: a bug in the caller.
func (g Gate) check(role, node string) error {
	if !slices.ContainsFunc(g.runs(role), func(n spec.Node) bool { return n.Name == node }) {
		return fmt.Errorf("gate: %s does not run %s in the spec", node, role)
	}
	return nil
}

// runs is every node of the spec that runs role, in spec order.
func (g Gate) runs(role string) []spec.Node {
	var nodes []spec.Node
	for _, n := range g.Nodes {
		if slices.Contains(n.Roles, role) {
			nodes = append(nodes, n)
		}
	}
	return nodes
}

// frontends are the frontend nodes, but except.
func (g Gate) frontends(except string) []string {
	var names []string
	for _, n := range g.runs("frontend") {
		if n.Name != except {
			names = append(names, n.Name)
		}
	}
	return names
}

func (g Gate) node(name string) spec.Node {
	i := slices.IndexFunc(g.Nodes, func(n spec.Node) bool { return n.Name == name })
	return g.Nodes[i]
}

// addr is n's member address in role's ring: its spec address and the role's gRPC port.
func addr(n spec.Node, role string) string {
	return fmt.Sprintf("%s:%d", n.Address, render.PortsOf(role).GRPC)
}
