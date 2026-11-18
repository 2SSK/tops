package cli

import (
	"errors"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/2SSK/tops/internal/datastore/postgres"
	"github.com/2SSK/tops/internal/fleet"
	"github.com/2SSK/tops/internal/sizing"
	"github.com/2SSK/tops/internal/term"
	"github.com/2SSK/tops/internal/wire"
	"github.com/spf13/cobra"
)

// capacityDocument is the --json result of tops capacity (backend schema "--json outputs").
type capacityDocument struct {
	Kind        string            `json:"kind"`
	TopsVersion string            `json:"topsVersion"`
	Env         string            `json:"env"`
	Cluster     string            `json:"cluster"`
	Constants   capacityConstants `json:"constants"`
	Nodes       []nodeCapacity    `json:"nodes"`
	Headroom    sizing.Headroom   `json:"headroom"`
	DB          dbCapacity        `json:"db"`
	refusals    []error           // in the order shown; the first decides the exit
}

type capacityConstants struct {
	Temporal   string `json:"temporal"`
	MeasuredAt string `json:"measuredAt"` // "" while the constants are placeholders
}

// nodeCapacity is one node: what hello detected and how its roles are sized.
type nodeCapacity struct {
	Name    string            `json:"name"`
	Memory  wire.Detected     `json:"memory"`
	Cores   wire.Detected     `json:"cores"`
	Reserve int64             `json:"reserveBytes"`
	Roles   []sizing.RoleSize `json:"roles"`
	Refused string            `json:"refused,omitempty"` // why the roles do not fit
}

// dbCapacity is the database budget, or why it could not be read.
type dbCapacity struct {
	sizing.DBBudget
	Host  string `json:"host"`
	Error string `json:"error,omitempty"` // the store could not be read
}

func newCapacityCommand(opts *Options) *cobra.Command {
	return &cobra.Command{
		Use:     "capacity",
		Short:   "Show each node's role limits, shard headroom and the database budget",
		GroupID: "change",
		Args:    usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			env, err := loadEnv(cmd, opts)
			if err != nil {
				return err
			}
			doc, err := capacity(cmd, opts, env)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if opts.JSON {
				err = term.WriteJSON(out, doc)
			} else {
				_, err = io.WriteString(out, capacityPage(doc, StyleFor(out, *opts)))
			}
			if err != nil {
				return err
			}
			return doc.exit()
		},
	}
}

// capacity reads everything the sizing needs (app-flow "capacity"): hello on every node, then
// PostgreSQL's limits through a tunnel from the first node.
func capacity(cmd *cobra.Command, opts *Options, env *fleet.Env) (*capacityDocument, error) {
	secrets, err := env.ReadSecrets()
	if err != nil {
		return nil, err
	}
	version := env.Cluster.Temporal.Version
	consts, err := sizing.For(version[:strings.LastIndex(version, ".")])
	if err != nil {
		return nil, err
	}
	c, err := connect(cmd, opts, env)
	if err != nil {
		return nil, err
	}
	defer c.Close()

	hellos := make([]wire.Hello, len(c.nodes))
	for i, n := range c.nodes {
		hellos[i] = n.Hello
	}
	pg := env.Cluster.Persistence.Postgres
	limits := func() (sizing.DBLimits, error) {
		return postgres.Limits(cmd.Context(), postgres.Config{Host: pg.Host, Port: pg.Port, User: pg.User, Password: secrets["TOPS_PG_PASSWORD"], Database: pg.AdminDatabase, TLS: pg.TLS, Dial: c.nodes[0].DialContext})
	}
	return sizeEnv(env, consts, hellos, limits)
}

// sizeEnv sizes every node from its hello, checks headroom, and reads the store's limits for
// the budget. A node that does not fit and a store that cannot be read are reported in the
// document, not returned: the rest of the page is still worth showing.
func sizeEnv(env *fleet.Env, consts sizing.Constants, hellos []wire.Hello, limits func() (sizing.DBLimits, error)) (*capacityDocument, error) {
	doc := &capacityDocument{
		Kind: "Capacity", TopsVersion: Version, Env: env.Name, Cluster: env.Cluster.Cluster,
		Constants: capacityConstants{Temporal: consts.Temporal, MeasuredAt: consts.MeasuredAt},
	}
	var sized []sizing.NodeSize
	for i, n := range env.Cluster.Nodes {
		nc := nodeCapacity{Name: n.Name, Memory: hellos[i].Memory, Cores: hellos[i].Cores, Roles: []sizing.RoleSize{}}
		ns, err := sizing.Size(consts, sizing.Node{
			Name: n.Name, Memory: int64(hellos[i].Memory.Value), Cores: hellos[i].Cores.Value, Roles: n.Roles, Pin: n.Pin,
		})
		var ue *term.UserError
		switch {
		case errors.As(err, &ue):
			nc.Refused = ue.Why
			doc.refusals = append(doc.refusals, err)
		case err != nil:
			return nil, err
		default:
			nc.Reserve, nc.Roles = ns.Reserve, ns.Roles
			sized = append(sized, ns)
		}
		doc.Nodes = append(doc.Nodes, nc)
	}
	doc.Headroom = sizing.CheckHeadroom(consts, env.Cluster.Shards, sized)
	if err := doc.Headroom.Refusal(); err != nil {
		doc.refusals = append(doc.refusals, err)
	}
	doc.DB = dbCapacity{Host: env.Cluster.Persistence.Postgres.Host}
	l, err := limits()
	if err != nil {
		doc.DB.Error = err.Error()
		return doc, nil
	}
	doc.DB.DBBudget = sizing.CheckDB(l, sized)
	if err := doc.DB.Refusal(); err != nil {
		doc.refusals = append(doc.refusals, err)
	}
	return doc, nil
}

// exit is the command's result after the document is out: the first refusal (exit 3), else
// an unreadable store (exit 5), else success. The page already showed why, so it is Reported.
func (d *capacityDocument) exit() error {
	switch {
	case len(d.refusals) > 0:
		return &term.Reported{Err: d.refusals[0]}
	case d.DB.Error != "":
		return &term.Reported{Err: &term.UserError{Code: "unhealthy.unreachable", What: "cannot read the limits of " + d.DB.Host, Why: d.DB.Error}}
	}
	return nil
}

// capacityPage is the human output: per node, its detected size and its roles' limits; then
// the cluster-wide checks.
func capacityPage(d *capacityDocument, st term.Style) string {
	var b strings.Builder
	measured := "constants not measured yet: placeholders"
	if d.Constants.MeasuredAt != "" {
		measured = "constants measured " + d.Constants.MeasuredAt + " on VPSLAB"
	}
	fmt.Fprintf(&b, "%s (%s)  %d nodes · %s (Temporal %s)\n", d.Env, d.Cluster, len(d.Nodes), measured, d.Constants.Temporal)
	for _, n := range d.Nodes {
		fmt.Fprintf(&b, "\n%s  %s (%s) · %s (%s)", n.Name, cores(n.Cores.Value), source(n.Cores.Source), sizing.Bytes(int64(n.Memory.Value)), source(n.Memory.Source))
		if n.Refused != "" {
			fmt.Fprintf(&b, "\n%s %s\n", st.Mark(term.Fail), n.Refused)
			continue
		}
		fmt.Fprintf(&b, " · reserve %s\n", sizing.Bytes(n.Reserve))
		fmt.Fprintf(&b, "  %-9s  %-9s  %-10s  %-10s  %-9s  %-4s  %s\n", "role", "MemoryMax", "MemoryHigh", "GOMEMLIMIT", "CPUWeight", "pool", "vis")
		for _, r := range n.Roles {
			pin := ""
			if len(r.Pinned) > 0 {
				pin = "  pin " + strings.Join(r.Pinned, ",")
			}
			fmt.Fprintf(&b, "  %-9s  %-9s  %-10s  %-10s  %-9d  %-4d  %d%s\n", r.Role, sizing.Bytes(r.MemoryMax), sizing.Bytes(r.MemoryHigh), sizing.Bytes(r.GoMemLimit), r.CPUWeight, r.DBPool, r.DBVisPool, pin)
		}
	}
	b.WriteString("\ncluster\n")
	b.WriteString(headroomLine(d.Headroom, st))
	b.WriteString(dbLine(d.DB, st))
	return b.String()
}

func headroomLine(h sizing.Headroom, st term.Style) string {
	switch {
	case h.Skipped != "":
		return fmt.Sprintf("%s headroom   %s\n", st.Mark(term.Warn), h.Skipped)
	case h.HistoryNodes == 0:
		return fmt.Sprintf("%s headroom   no history node could be sized\n", st.Mark(term.Skip))
	}
	mark := st.Mark(term.OK)
	if !h.OK {
		mark = st.Mark(term.Fail)
	}
	return fmt.Sprintf("%s headroom   %s down: %s history owns %d shards, predicted %s of %s GOMEMLIMIT\n", mark, h.Down, h.WorstSurvivor, h.Shards, sizing.Bytes(h.Predicted), sizing.Bytes(h.Limit))
}

func dbLine(db dbCapacity, st term.Style) string {
	if db.Error != "" {
		return fmt.Sprintf("%s database   unreachable: %s\n", st.Mark(term.Fail), db.Error)
	}
	mark := st.Mark(term.OK)
	if !db.OK {
		mark = st.Mark(term.Fail)
	}
	return fmt.Sprintf("%s database   pools %d of %d available (%s max_connections %d · reserved %d · other sessions %d)\n", mark, db.Pools, db.Budget, db.Host, db.MaxConnections, db.Reserved, db.OtherSessions)
}

// cores prints 1.5 cores, 1 core.
func cores(n float64) string {
	if n == 1 {
		return "1 core"
	}
	return fmt.Sprintf("%g cores", n)
}

// source shortens hello's sources for the page: cgroup:/sys/fs/cgroup/memory.max is
// "cgroup memory.max"; meminfo and affinity stay as they are.
func source(s string) string {
	if file, ok := strings.CutPrefix(s, "cgroup:"); ok {
		return "cgroup " + path.Base(file)
	}
	return s
}
