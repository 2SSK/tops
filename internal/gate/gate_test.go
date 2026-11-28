package gate

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/2SSK/tops/internal/cluster"
	"github.com/2SSK/tops/internal/spec"
	"github.com/2SSK/tops/internal/term"
)

// clock is a fake Clock: Sleep moves it on at once.
type clock struct{ now time.Time }

func (c *clock) Now() time.Time { return c.now }

func (c *clock) Sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.now = c.now.Add(d)
	return nil
}

// state is the cluster at one moment: which frontends serve, each one's rings, and the
// shards each history host owns.
type state struct {
	serving map[string]bool
	rings   map[string]map[string][]string // frontend node -> role -> members
	shards  map[string]int                 // history address -> shards owned
}

// fake is a cluster whose state follows the fake clock: at(elapsed) says what it is then.
type fake struct {
	clock *clock
	start time.Time
	at    func(elapsed time.Duration) state
}

func (f *fake) now() state { return f.at(f.clock.now.Sub(f.start)) }

func (f *fake) Serving(_ context.Context, node string) (bool, error) {
	return f.now().serving[node], nil
}

func (f *fake) Describe(_ context.Context, node string) (cluster.View, error) {
	rings, ok := f.now().rings[node]
	if !ok {
		return cluster.View{}, errors.New("unavailable")
	}
	return cluster.View{Frontend: node, Rings: rings}, nil
}

func (f *fake) Shards(_ context.Context, _, addr string) (int, error) {
	n, ok := f.now().shards[addr]
	if !ok {
		return 0, errors.New("unavailable")
	}
	return n, nil
}

// lab is three nodes: frontend and matching on each, history and worker on the first two.
var lab = []spec.Node{
	{Name: "vps-01", Address: "10.80.0.11", Roles: []string{"frontend", "history", "matching", "worker"}},
	{Name: "vps-02", Address: "10.80.0.12", Roles: []string{"frontend", "history", "matching", "worker"}},
	{Name: "vps-03", Address: "10.80.0.13", Roles: []string{"frontend", "matching"}},
}

// whole is lab with every member in every ring, as each frontend sees it, and 512 shards
// split between the two history hosts.
func whole() state {
	s := state{serving: map[string]bool{}, rings: map[string]map[string][]string{}, shards: map[string]int{"10.80.0.11:7234": 256, "10.80.0.12:7234": 256}}
	for _, fe := range []string{"vps-01", "vps-02", "vps-03"} {
		s.serving[fe] = true
		s.rings[fe] = map[string][]string{
			"frontend": {"10.80.0.11:7233", "10.80.0.12:7233", "10.80.0.13:7233"},
			"history":  {"10.80.0.11:7234", "10.80.0.12:7234"},
			"matching": {"10.80.0.11:7235", "10.80.0.12:7235", "10.80.0.13:7235"},
			"worker":   {"10.80.0.11:7239", "10.80.0.12:7239"},
		}
	}
	return s
}

// drop takes member out of role's ring in the views of the frontends named, every view when
// none is.
func (s state) drop(role, member string, views ...string) state {
	for fe := range s.rings {
		if len(views) == 0 || slices.Contains(views, fe) {
			s.rings[fe][role] = slices.DeleteFunc(slices.Clone(s.rings[fe][role]), func(m string) bool { return m == member })
		}
	}
	return s
}

// gateOn is a gate over nodes whose cluster is at(elapsed), and its clock.
func gateOn(nodes []spec.Node, at func(time.Duration) state) (Gate, *clock) {
	c := &clock{now: time.Date(2026, 11, 28, 10, 0, 0, 0, time.UTC)}
	return Gate{Cluster: &fake{clock: c, start: c.now, at: at}, Nodes: nodes, Shards: 512, Clock: c}, c
}

func always(s state) func(time.Duration) state { return func(time.Duration) state { return s } }

// refusal is err's code and what it says; it fails the test if err is not a UserError.
func refusal(t *testing.T, err error) (string, string, string) {
	t.Helper()
	var ue *term.UserError
	if !errors.As(err, &ue) {
		t.Fatalf("err = %v, want a *term.UserError", err)
	}
	return ue.Code, ue.What, ue.Why
}

// TestBeforeWhole: with every ring whole and every shard owned, any role on any of its nodes
// may be touched, with no warning.
func TestBeforeWhole(t *testing.T) {
	g, _ := gateOn(lab, always(whole()))
	for _, n := range lab {
		for _, role := range n.Roles {
			if w, err := g.Before(t.Context(), role, n.Name); err != nil || len(w) != 0 {
				t.Errorf("%s on %s: %q, %v", role, n.Name, w, err)
			}
		}
	}
}

// TestBeforeRingIncomplete: another member missing from one frontend's view is enough to
// refuse, as that view sees it; the node about to be touched may be missing itself.
func TestBeforeRingIncomplete(t *testing.T) {
	g, _ := gateOn(lab, always(whole().drop("history", "10.80.0.12:7234", "vps-03")))
	code, what, why := refusal(t, func() error { _, err := g.Before(t.Context(), "history", "vps-01"); return err }())
	if code != "refused.ring_incomplete" || what != "history ring has 1 of 2 members: vps-02 is missing" || why != "seen through vps-03's frontend" {
		t.Errorf("refusal %s: %s (%s)", code, what, why)
	}
	g, _ = gateOn(lab, always(whole().drop("matching", "10.80.0.12:7235").drop("matching", "10.80.0.13:7235")))
	if _, what, _ := refusal(t, func() error { _, err := g.Before(t.Context(), "matching", "vps-01"); return err }()); what != "matching ring has 1 of 3 members: vps-02, vps-03 are missing" {
		t.Errorf("two missing: %s", what)
	}
	g, _ = gateOn(lab, always(whole().drop("worker", "10.80.0.11:7239")))
	if _, err := g.Before(t.Context(), "worker", "vps-01"); err != nil {
		t.Errorf("the node itself missing: %v", err)
	}
}

// TestBeforeShardDip: while shards move, history may not be touched; the other roles may.
func TestBeforeShardDip(t *testing.T) {
	s := whole()
	s.shards["10.80.0.12:7234"] = 44
	g, _ := gateOn(lab, always(s))
	code, what, _ := refusal(t, func() error { _, err := g.Before(t.Context(), "history", "vps-02"); return err }())
	if code != "refused.ring_incomplete" || what != "history owns 300 of 512 shards" {
		t.Errorf("refusal %s: %s", code, what)
	}
	if _, err := g.Before(t.Context(), "matching", "vps-02"); err != nil {
		t.Errorf("matching during a dip: %v", err)
	}
}

// TestBeforeUnknownMember: a member the spec does not run, in any ring of any view, is refused
// before anything is touched.
func TestBeforeUnknownMember(t *testing.T) {
	s := whole()
	s.rings["vps-02"]["matching"] = append(s.rings["vps-02"]["matching"], "10.80.0.99:7235")
	g, _ := gateOn(lab, always(s))
	code, what, why := refusal(t, func() error { _, err := g.Before(t.Context(), "worker", "vps-01"); return err }())
	if code != "refused.unknown_member" || what != "the matching ring has 10.80.0.99:7235, which the spec does not run" || why != "seen through vps-02's frontend" {
		t.Errorf("refusal %s: %s (%s)", code, what, why)
	}
	s = whole()
	s.rings["vps-01"]["history"] = append(s.rings["vps-01"]["history"], "10.80.0.13:7234") // vps-03 runs no history
	g, _ = gateOn(lab, always(s))
	if code, _, _ := refusal(t, func() error { _, err := g.Before(t.Context(), "matching", "vps-01"); return err }()); code != "refused.unknown_member" {
		t.Errorf("a known node in a ring it does not run: %s", code)
	}
}

// TestBeforeLastFrontend: a frontend is touched only while another serves; any other role
// needs one frontend serving, its own node's included.
func TestBeforeLastFrontend(t *testing.T) {
	s := whole()
	s.serving["vps-02"], s.serving["vps-03"] = false, false
	g, _ := gateOn(lab, always(s))
	code, what, _ := refusal(t, func() error { _, err := g.Before(t.Context(), "frontend", "vps-01"); return err }())
	if code != "refused.last_frontend" || what != "no frontend other than vps-01's is serving" {
		t.Errorf("refusal %s: %s", code, what)
	}
	s.rings["vps-01"]["frontend"] = []string{"10.80.0.11:7233"} // the others are down: vps-01 sees itself alone
	if _, err := g.Before(t.Context(), "history", "vps-01"); err != nil {
		t.Errorf("history with vps-01's frontend serving: %v", err)
	}
	s.serving["vps-01"] = false
	if code, what, _ := refusal(t, func() error { _, err := g.Before(t.Context(), "history", "vps-01"); return err }()); code != "refused.last_frontend" || what != "no frontend is serving" {
		t.Errorf("none serving: %s: %s", code, what)
	}
}

// TestBeforeSingleInstance: a role on one node has no other member to keep whole: it may be
// touched with a warning; a single frontend needs no frontend to read through.
func TestBeforeSingleInstance(t *testing.T) {
	one := []spec.Node{
		{Name: "vps-01", Address: "10.80.0.11", Roles: []string{"frontend", "history", "matching", "worker"}},
		{Name: "vps-02", Address: "10.80.0.12", Roles: []string{"matching"}},
	}
	s := state{serving: map[string]bool{}, shards: map[string]int{"10.80.0.11:7234": 512}, rings: map[string]map[string][]string{}}
	g, _ := gateOn(one, always(s))
	if w, err := g.Before(t.Context(), "frontend", "vps-01"); err != nil || !slices.Equal(w, []string{"frontend runs on one node: the cluster takes no requests while it restarts"}) {
		t.Errorf("the only frontend, down: %q, %v", w, err)
	}
	s.serving["vps-01"] = true
	s.rings["vps-01"] = map[string][]string{"frontend": {"10.80.0.11:7233"}, "history": {"10.80.0.11:7234"}, "matching": {"10.80.0.11:7235", "10.80.0.12:7235"}, "worker": {"10.80.0.11:7239"}}
	if w, err := g.Before(t.Context(), "history", "vps-01"); err != nil || !slices.Equal(w, []string{"history runs on one node: its shards are unowned while it restarts"}) {
		t.Errorf("the only history: %q, %v", w, err)
	}
	if w, err := g.Before(t.Context(), "matching", "vps-02"); err != nil || len(w) != 0 {
		t.Errorf("matching on two nodes: %q, %v", w, err)
	}
}

// TestNotTheSpec: a step on a node the spec does not have run the role is a caller's bug.
func TestNotTheSpec(t *testing.T) {
	g, _ := gateOn(lab, always(whole()))
	if _, err := g.Before(t.Context(), "history", "vps-03"); err == nil || !strings.Contains(err.Error(), "vps-03 does not run history") {
		t.Errorf("before: %v", err)
	}
	if err := g.After(t.Context(), "history", "vps-09"); err == nil {
		t.Error("after: no error")
	}
}

// backAt is whole() with role on node missing until elapsed reaches up.
func backAt(role, member string, up time.Duration) func(time.Duration) state {
	return func(elapsed time.Duration) state {
		if elapsed < up {
			return whole().drop(role, member)
		}
		return whole()
	}
}

// TestAfterBack: polled every two seconds, a role is back once it passes twice five seconds
// apart, in every view.
func TestAfterBack(t *testing.T) {
	for role, member := range map[string]string{"frontend": "10.80.0.11:7233", "history": "10.80.0.11:7234", "matching": "10.80.0.11:7235", "worker": "10.80.0.11:7239"} {
		g, c := gateOn(lab, backAt(role, member, 9*time.Second))
		start := c.now
		if err := g.After(t.Context(), role, "vps-01"); err != nil {
			t.Fatalf("%s: %v", role, err)
		}
		if took := c.now.Sub(start); took != 15*time.Second { // fails at 0..8, passes at 10 and 15
			t.Errorf("%s back after %s, want 15s", role, took)
		}
	}
}

// TestAfterFrontendServing: a frontend is back only once it serves, as well as in its ring.
func TestAfterFrontendServing(t *testing.T) {
	g, c := gateOn(lab, func(elapsed time.Duration) state {
		s := whole()
		s.serving["vps-01"] = elapsed >= 20*time.Second
		return s
	})
	start := c.now
	if err := g.After(t.Context(), "frontend", "vps-01"); err != nil || c.now.Sub(start) != 25*time.Second {
		t.Errorf("after %s: %v", c.now.Sub(start), err)
	}
}

// TestAfterFlapping: a member that keeps leaving the ring never passes twice in a row; After
// gives up after three minutes with the condition that failed last.
func TestAfterFlapping(t *testing.T) {
	g, c := gateOn(lab, func(elapsed time.Duration) state {
		if int(elapsed.Seconds())%7 == 0 { // in the ring one second in seven
			return whole()
		}
		return whole().drop("matching", "10.80.0.11:7235", "vps-02")
	})
	start := c.now
	code, what, why := refusal(t, g.After(t.Context(), "matching", "vps-01"))
	if code != "rollout.role_unhealthy" || what != "matching on vps-01 is not back after 3m0s" || why != "matching ring has 2 of 3 members: vps-01 is missing (seen through vps-02's frontend)" {
		t.Errorf("%s: %s (%s)", code, what, why)
	}
	if took := c.now.Sub(start); took < timeout || took > timeout+stableAfter {
		t.Errorf("gave up after %s", took)
	}
}

// TestAfterShardDip: history is back only when its host owns shards and every shard is owned,
// both times.
func TestAfterShardDip(t *testing.T) {
	g, c := gateOn(lab, func(elapsed time.Duration) state {
		s := whole()
		switch {
		case elapsed < 4*time.Second: // in the ring, nothing acquired yet
			s.shards["10.80.0.11:7234"], s.shards["10.80.0.12:7234"] = 0, 512
		case elapsed < 6*time.Second: // acquiring: some shards between owners
			s.shards["10.80.0.11:7234"], s.shards["10.80.0.12:7234"] = 100, 300
		}
		return s
	})
	start := c.now
	if err := g.After(t.Context(), "history", "vps-01"); err != nil || c.now.Sub(start) != 11*time.Second {
		t.Errorf("after %s: %v", c.now.Sub(start), err)
	}
	g, _ = gateOn(lab, func(time.Duration) state {
		s := whole()
		s.shards["10.80.0.11:7234"], s.shards["10.80.0.12:7234"] = 0, 512
		return s
	})
	if _, _, why := refusal(t, g.After(t.Context(), "history", "vps-01")); why != "history on vps-01 owns no shards (read through vps-01's frontend)" {
		t.Errorf("owns none: %s", why)
	}
}

// TestAfterCanceled: an interrupt ends the wait at once.
func TestAfterCanceled(t *testing.T) {
	g, _ := gateOn(lab, backAt("history", "10.80.0.11:7234", time.Hour))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := g.After(ctx, "history", "vps-01"); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v", err)
	}
}

// TestSystemClock: the real clock sleeps, and stops sleeping when the context ends.
func TestSystemClock(t *testing.T) {
	if err := System.Sleep(t.Context(), time.Millisecond); err != nil {
		t.Error(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := System.Sleep(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled sleep: %v", err)
	}
	if d := time.Since(System.Now()); d < 0 || d > time.Minute {
		t.Errorf("now is %s off", d)
	}
}
