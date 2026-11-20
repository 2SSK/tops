package cli

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/2SSK/tops/internal/fleet"
	"github.com/2SSK/tops/internal/plan"
	"github.com/2SSK/tops/internal/render"
	"github.com/2SSK/tops/internal/settings"
	"github.com/2SSK/tops/internal/sizing"
	"github.com/2SSK/tops/internal/spec"
	"github.com/2SSK/tops/internal/term"
	"github.com/2SSK/tops/internal/wire"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
)

// planDocument is the --json result of tops plan (backend schema "--json outputs": Plan).
type planDocument struct {
	Kind        string          `json:"kind"`
	TopsVersion string          `json:"topsVersion"`
	Env         string          `json:"env"`
	Cluster     string          `json:"cluster"`
	Changes     bool            `json:"changes"`
	Drift       bool            `json:"drift"`
	Nodes       []plan.NodePlan `json:"nodes"`
	Refusals    []refusal       `json:"refusals"`
	capacity    *capacityDocument
}

// refusal is one reason apply would not start.
type refusal struct {
	Code string `json:"code"`
	What string `json:"what"`
	Why  string `json:"why,omitempty"`
	Fix  string `json:"fix,omitempty"`
}

// factsFunc reads the facts of node i, the i-th of the spec.
type factsFunc func(ctx context.Context, i int, args wire.FactsArgs) (wire.Facts, error)

func newPlanCommand(opts *Options) *cobra.Command {
	return &cobra.Command{
		Use:     "plan",
		Short:   "Show what apply change on every node, changing nothing",
		GroupID: "change",
		Args:    usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			env, err := loadEnv(cmd, opts)
			if err != nil {
				return err
			}
			doc, err := planOnNodes(cmd, opts, env)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if opts.JSON {
				err = term.WriteJSON(out, doc)
			} else {
				_, err = io.WriteString(out, planPage(doc, StyleFor(out, *opts)))
			}
			if err != nil {
				return err
			}
			return doc.exit()
		},
	}
}

// planOnNodes checks the spec, connects, sizes every node, and plans each from its facts
func planOnNodes(cmd *cobra.Command, opts *Options, env *fleet.Env) (*planDocument, error) {
	errs, warnings, err := check(cmd.Context(), env)
	if err != nil {
		return nil, err
	}
	if len(errs) > 0 {
		return nil, invalidSpec(env, errs, warnings)
	}
	secrets, err := env.ReadSecrets()
	if err != nil {
		return nil, err
	}
	consts, err := sizingConstants(env)
	if err != nil {
		return nil, err
	}
	c, err := connect(cmd, opts, env)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	sized, err := sizeNodes(cmd, env, consts, secrets, c)
	if err != nil {
		return nil, err
	}
	facts := func(ctx context.Context, i int, args wire.FactsArgs) (wire.Facts, error) {
		return c.nodes[i].Facts(ctx, args)
	}
	return planEnv(cmd.Context(), env, sized, secrets, facts)
}

// planEnv renders every node and plans it against its facts. A sizing refusal plans
// nothing: there are no sizes to render the refused node's files from.
func planEnv(ctx context.Context, env *fleet.Env, sized *capacityDocument, secrets fleet.Secrets, facts factsFunc) (*planDocument, error) {
	doc := &planDocument{
		Kind: "Plan", TopsVersion: Version, Env: env.Name, Cluster: env.Cluster.Cluster, Nodes: []plan.NodePlan{}, Refusals: []refusal{}, capacity: sized,
	}
	for _, err := range sized.refusals {
		doc.Refusals = append(doc.Refusals, toRefusal(err))
	}
	if len(doc.Refusals) > 0 || sized.DB.Error != "" {
		return doc, nil
	}
	inputs, err := renderEnv(env, sized.Sized, secrets)
	if err != nil {
		return nil, err
	}
	values := make([]string, 0, len(secrets))
	for _, v := range secrets {
		values = append(values, v.Reveal())
	}
	if doc.Nodes, err = planNodes(ctx, env.Cluster.Temporal.Version, inputs, values, facts); err != nil {
		return nil, err
	}
	for _, n := range doc.Nodes {
		doc.Changes = doc.Changes || n.Changes()
		doc.Drift = doc.Drift || slices.ContainsFunc(n.Files, func(f plan.FileChange) bool { return f.Drift })
	}
	return doc, nil
}

// planNodes renders and plans each node, reading the nodes' facts in parallel.
func planNodes(ctx context.Context, version string, inputs []render.NodeInput, secrets []string, facts factsFunc) ([]plan.NodePlan, error) {
	nodes := make([]plan.NodePlan, len(inputs))
	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(8)
	for i, in := range inputs {
		g.Go(func() error {
			files, err := render.Render(in)
			if err != nil {
				return err
			}
			f, err := facts(ctx, i, plan.Want(files))
			if err != nil {
				return err
			}
			nodes[i] = plan.Node(in.Node, version, files, f, secrets)
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return nodes, nil
}

// renderEnv is every node's render input: the spec, the sizes, each role's merged dynamic
// config, and the secrets.
func renderEnv(env *fleet.Env, sizes []sizing.NodeSize, secrets fleet.Secrets) ([]render.NodeInput, error) {
	c, err := env.Catalog()
	if err != nil {
		return nil, err
	}
	layers, _, err := env.Settings(c) // check has refused a spec with problems
	if err != nil {
		return nil, err
	}
	dynamic := map[string]settings.File{}
	for _, role := range spec.Roles {
		dynamic[role] = layers.Merge(role, c)
	}
	e := render.Env{Name: env.Name, Cluster: env.Cluster, Sizes: sizes, Dynamic: dynamic, Secrets: secrets}
	return e.Inputs(), nil
}

func toRefusal(err error) refusal {
	var ue *term.UserError
	if errors.As(err, &ue) {
		return refusal{Code: ue.Code, What: ue.What, Why: ue.Why, Fix: ue.Fix}
	}
	return refusal{Code: "unexpected", What: err.Error()}
}

// exit is the command's result after the plan is out: a refusal (3), an unreadable store (5),
// changes or drift pending (2), else nothing to do (0). The page already showed why.
func (d *planDocument) exit() error {
	if err := d.capacity.exit(); err != nil {
		return err
	}
	if d.Changes || d.Drift {
		return &term.Reported{Err: &term.UserError{Code: "pending.changes", What: "apply would change " + d.Env}}
	}
	return nil
}

// planPage is the human output (app-flow "plan"): per node and role, each file and what
// happens to the role, then a summary.
func planPage(d *planDocument, st term.Style) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s (%s)  plan for %d nodes\n", d.Env, d.Cluster, len(d.Nodes))
	for _, r := range d.Refusals {
		fmt.Fprintf(&b, "\n%s %s: %s\n  %s\n", st.Mark(term.Fail), r.What, r.Why, r.Fix)
	}
	if d.capacity.DB.Error != "" {
		fmt.Fprintf(&b, "\n%s database unreachable: %s\n", st.Mark(term.Fail), d.capacity.DB.Error)
	}
	for _, n := range d.Nodes {
		b.WriteString(nodePage(n, st))
	}
	b.WriteString(d.summary())
	return b.String()
}

// summary is the plan's last lines: what it counts, and the command that applies it.
func (d *planDocument) summary() string {
	if len(d.Refusals) > 0 || d.capacity.DB.Error != "" {
		return "\nnothing planned until the refusals above are fixed\n"
	}
	if !d.Changes && !d.Drift {
		return "\nnothing to do\n"
	}
	changing, restarts, drift := 0, 0, 0
	for _, n := range d.Nodes {
		if n.Changes() {
			changing++
		}
		restarts += countOf(n.Roles, func(r plan.RoleChange) bool { return r.Restart })
		drift += countOf(n.Files, func(f plan.FileChange) bool { return f.Drift })
	}
	return fmt.Sprintf("\n%d nodes change · %d restarts · %d drift\napply with: tops -e %s apply\n", changing, restarts, drift, d.Env)
}

// countOf is how many of s match.
func countOf[T any](s []T, match func(T) bool) int {
	n := 0
	for _, v := range s {
		if match(v) {
			n++
		}
	}
	return n
}

// nodePage is one node: its shared files, then each role's files and what happens to it.
func nodePage(n plan.NodePlan, st term.Style) string {
	var b strings.Builder
	for _, check := range n.Validated.Checks {
		fmt.Fprintf(&b, "  %s %s\n", st.Mark(term.Warn), check)
	}
	b.WriteString(rolePage(n, ""))
	for _, r := range n.Roles {
		b.WriteString(rolePage(n, r.Role))
	}
	for _, f := range n.Files {
		if f.Drift {
			fmt.Fprintf(&b, "  %s drift: %s is not what tops last wrote; apply overwrites it\n", st.Mark(term.Warn), f.Path)
		}
	}
	if b.Len() == 0 {
		return "\n" + n.Name + "  no changes\n"
	}
	return "\n" + n.Name + "\n" + b.String()
}

// fileSign marks a file's action on the page
var fileSign = map[plan.Action]string{plan.Create: "+", plan.Update: "~", plan.Remove: "-"}

// rolePage is one role's files and its unit's action; role "" is the node's shared files.
func rolePage(n plan.NodePlan, role string) string {
	var b strings.Builder
	for _, f := range n.Files {
		if f.Role != role {
			continue
		}
		what := string(f.Action)
		if f.Action == plan.Update {
			what = string(f.Effect)
		}
		fmt.Fprintf(&b, "    %s %-60s %s\n", fileSign[f.Action], f.Path, what)
		for line := range strings.Lines(f.Diff) {
			b.WriteString("        " + line)
		}
	}
	for _, r := range n.Roles {
		if r.Role == role {
			b.WriteString(roleLine(r))
		}
	}
	if b.Len() == 0 {
		return ""
	}
	return "  " + cmp.Or(role, "shared") + "\n" + b.String()
}

// roleLine is what happens to a role's unit, or nothing for a role kept as it is.
func roleLine(r plan.RoleChange) string {
	switch {
	case r.Action == plan.Start:
		return "    + starts " + r.Role + "\n"
	case r.Action == plan.Stop:
		return "    - stops " + r.Role + "\n"
	case r.Restart:
		return "    → restarts " + r.Role + "\n"
	}
	return ""
}
