package sizing

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/2SSK/tops/internal/term"
)

// pools is a sized node whose roles have the given (default, visibility) pools.
func pools(name string, p ...[2]int) NodeSize {
	n := NodeSize{Name: name}
	for _, pp := range p {
		n.Roles = append(n.Roles, RoleSize{Role: "history", DBPool: pp[0], DBVisPool: pp[1]})
	}
	return n
}

// The design brief's example: 300 max, 3 reserved, 111 other sessions, 2 for tops leave 184.
var vpsPG = DBLimits{Host: "vps-pg", MaxConnections: 300, SuperuserReserved: 3, OtherSessions: 111}

func TestCheckDB(t *testing.T) {
	tests := []struct {
		name   string
		limits DBLimits
		nodes  []NodeSize
		want   DBBudget
	}{
		{"fits", vpsPG, []NodeSize{pools("vps-01", [2]int{2, 2}, [2]int{8, 2}), pools("vps-02", [2]int{20, 4})},
			DBBudget{MaxConnections: 300, Reserved: 3, OtherSessions: 111, Budget: 184, Pools: 38, OK: true, host: "vps-pg"}},
		{"exactly the budget fits", vpsPG, []NodeSize{pools("vps-01", [2]int{180, 4})},
			DBBudget{MaxConnections: 300, Reserved: 3, OtherSessions: 111, Budget: 184, Pools: 184, OK: true, host: "vps-pg"}},
		{"one over", vpsPG, []NodeSize{pools("vps-01", [2]int{181, 4})},
			DBBudget{MaxConnections: 300, Reserved: 3, OtherSessions: 111, Budget: 184, Pools: 185, OK: false, host: "vps-pg"}},
		{"PostgreSQL 16 reserved_connections count too", DBLimits{Host: "pg", MaxConnections: 100, SuperuserReserved: 3, Reserved: 5},
			[]NodeSize{pools("vps-01", [2]int{50, 10})},
			DBBudget{MaxConnections: 100, Reserved: 8, Budget: 90, Pools: 60, OK: true, host: "pg"}},
		{"other users can leave nothing", DBLimits{Host: "pg", MaxConnections: 20, SuperuserReserved: 3, OtherSessions: 30},
			[]NodeSize{pools("vps-01", [2]int{2, 1})},
			DBBudget{MaxConnections: 20, Reserved: 3, OtherSessions: 30, Budget: -15, Pools: 3, OK: false, host: "pg"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CheckDB(tt.limits, tt.nodes)
			if got != tt.want {
				t.Errorf("CheckDB =\n  %+v\nwant\n  %+v", got, tt.want)
			}
			if err := got.Refusal(); (err == nil) != got.OK {
				t.Errorf("Refusal() = %v with OK %v", err, got.OK)
			}
		})
	}
}

func TestCheckDBRefusal(t *testing.T) {
	b := CheckDB(vpsPG, []NodeSize{pools("vps-01", [2]int{200, 12})})
	var ue *term.UserError
	if !errors.As(b.Refusal(), &ue) {
		t.Fatalf("Refusal() = %v, want a refusal", b.Refusal())
	}
	want := term.UserError{
		Code: "refused.db_budget",
		What: "not applying",
		Why:  "pools need 212 connections; vps-pg can give 184 (300 max, 3 reserved, 111 other sessions)",
		Fix:  "lower pools with pins, or raise max_connections",
	}
	if *ue != want {
		t.Errorf("refusal =\n  %+v\nwant\n  %+v", *ue, want)
	}
	if term.ExitCode(ue) != 3 {
		t.Errorf("exit code = %d, want 3", term.ExitCode(ue))
	}
}

func TestDBBudgetJSON(t *testing.T) {
	b, err := json.Marshal(CheckDB(vpsPG, []NodeSize{pools("vps-01", [2]int{20, 4})}))
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"maxConnections":300,"reserved":3,"otherSessions":111,"budget":184,"pools":24,"ok":true}`; string(b) != want {
		t.Errorf("json = %s, want %s", b, want)
	}
}
