package sizing

import (
	"fmt"

	"github.com/2SSK/tops/internal/term"
)

// topsConnections are the connections tops itself may hold while it checks and applies.
const topsConnections = 2

// DBLimits are PostgreSQL's connection limits, read from the server at plan time.
type DBLimits struct {
	Host              string // as the spec names it, for messages
	MaxConnections    int    // SHOW max_connections
	SuperuserReserved int    // SHOW superuser_reserved_connections
	Reserved          int    // SHOW reserved_connections; 0 before PostgreSQL 16
	OtherSessions     int    // pg_stat_activity: other users' client sessions
}

// DBBudget is the database check (TRD "Sizing", refused.db_budget): every role's pools, on
// every node, must fit in what PostgreSQL leaves after its reservations and other users.
type DBBudget struct {
	MaxConnections int  `json:"maxConnections"`
	Reserved       int  `json:"reserved"` // superuser and reserved_connections together
	OtherSessions  int  `json:"otherSessions"`
	Budget         int  `json:"budget"` // what Temporal may open
	Pools          int  `json:"pools"`  // what the sized roles would open, at most
	OK             bool `json:"ok"`
	host           string
}

// CheckDB adds up every role's default and visibility pool on every node and compares  the sum
// with what the server can give.
func CheckDB(l DBLimits, nodes []NodeSize) DBBudget {
	b := DBBudget{
		MaxConnections: l.MaxConnections,
		Reserved:       l.SuperuserReserved + l.Reserved,
		OtherSessions:  l.OtherSessions,
		host:           l.Host,
	}
	b.Budget = b.MaxConnections - b.Reserved - b.OtherSessions - topsConnections
	for _, n := range nodes {
		for _, r := range n.Roles {
			b.Pools += r.DBPool + r.DBVisPool
		}
	}
	b.OK = b.Pools <= b.Budget
	return b
}

// Refusal is refused.db_budget when the pools do not fit, else nil.
func (b DBBudget) Refusal() error {
	if b.OK {
		return nil
	}
	return &term.UserError{
		Code: "refused.db_budget",
		What: "not applying",
		Why:  fmt.Sprintf("pools need %d connections; %s can give %d (%d max, %d reserved, %d other sessions)", b.Pools, b.host, b.Budget, b.MaxConnections, b.Reserved, b.OtherSessions),
		Fix:  "lower pools with pins, or raise max_connections",
	}
}
