// Package sizing computes each role's memory limits, CPU weight and database pools on a node
// from the node's detected size, its roles and pins, and constants measured on VPSLAB (TRD
// "Sizing"). It is pure: no I/O, so every number can be checked by hand.
package sizing

import (
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/2SSK/tops/internal/spec"
	"github.com/2SSK/tops/internal/term"
)

const mib = 1 << 20

// Node is what sizing needs to know about one node.
type Node struct {
	Name   string
	Memory int64   // effective bytes, from hello
	Cores  float64 // effective cores, from hello; fractional from cpu.max
	Roles  []string
	Pin    map[string]spec.Pin
}

// NodeSize is the result for one node.
type NodeSize struct {
	Name       string     `json:"name"`
	Reserve    int64      `json:"reserveBytes"` // left to the OS
	Free       int64      `json:"freeBytes"`    // shared among the unpinned roles above their floors
	GoMaxProcs int        `jsson:"GoMaxProcs"`  // the same for every role on the node
	Roles      []RoleSize `json:"roles"`        // in stage order
}

// RoleSize is one role's limits on one node (backend schema "Sizing").
type RoleSize struct {
	Role       string   `json:"role"`
	MemoryMax  int64    `json:"memoryMaxBytes"`
	MemoryHigh int64    `json:"memoryHighBytes"`
	GoMemLimit int64    `json:"goMemLimitBytes"`
	CPUWeight  int      `json:"cpuWeight"`
	DBPool     int      `json:"dbPool"`
	DBVisPool  int      `json:"dbVisibilityPool"`
	Pinned     []string `json:"pinned"`  // the fields that came from a pin
	Formula    string   `json:"formula"` // how MemoryMax was reached
}

// Size sizes every role on n. It refuses (refused.node_too_small) a node whose memory, after
// the OS reserve, does not cover every role's floor, and a pin below its role's floor.
func Size(c Constants, n Node) (NodeSize, error) {
	roles := inStageOrder(n.Roles)
	pinned := map[string]int64{} // role -> pinned memoryMax bytes
	for _, r := range roles {
		if s := n.Pin[r].MemoryMax; s != "" {
			b, err := spec.ParseSize(s)
			if err != nil {
				return NodeSize{}, fmt.Errorf("%s pin.%s.memoryMax: %w", n.Name, r, err)
			}
			if b < c.Floor[r] {
				return NodeSize{}, tooSmall(n.Name, roles, fmt.Sprintf("pin.%s.memoryMax %s is below the %s floor %s", r, Bytes(b), r, Bytes(c.Floor[r])))
			}
			pinned[r] = b
		}
	}

	var needed int64 // floors of the unpinned roles, and the pins
	shares := 0.0
	for _, r := range roles {
		if b, ok := pinned[r]; ok {
			needed += b
			continue
		}
		needed += c.Floor[r]
		shares += c.Share[r]
	}
	free := float64(n.Memory - c.OSBase - needed)
	if free < 0 {
		return NodeSize{}, tooSmall(n.Name, roles, fmt.Sprintf("its roles need %s (floors and pins) and the OS %s, but it has %s", Bytes(needed), Bytes(c.OSBase), Bytes(n.Memory)))
	}
	ns := NodeSize{Name: n.Name, Reserve: c.OSBase, Free: int64(free), GoMaxProcs: GoMaxProcs(n.Cores)}
	for _, r := range roles {
		ns.Roles = append(ns.Roles, c.role(r, n, pinned, free, shares))
	}
	return ns, nil
}

// role computes one role's sizes. Values stay unrounded until the end: limits round down to a
// MiB, pools round up, each once.
func (c Constants) role(r string, n Node, pinned map[string]int64, free, shares float64) RoleSize {
	pin := n.Pin[r]
	rs := RoleSize{Role: r, Pinned: []string{}}

	memMax, ok := float64(pinned[r]), pinned[r] > 0
	if ok {
		rs.Pinned = append(rs.Pinned, "memoryMax")
		rs.Formula = "pinned " + Bytes(pinned[r])
	} else {
		share := c.Share[r] / shares
		memMax = float64(c.Floor[r]) + free*share
		rs.Formula = fmt.Sprintf("floor %s + free %s × share %.2f", Bytes(c.Floor[r]), Bytes(int64(free)), share)
	}
	memHigh := memMax * (1 - c.Burst[r])
	rs.MemoryMax = floorMiB(memMax)
	rs.MemoryHigh = floorMiB(memHigh)
	rs.GoMemLimit = floorMiB(memHigh * (1 - c.NonHeap[r]))

	rs.CPUWeight = clamp(int(math.Round(100*c.CPU[r]/c.meanCPU())), 1, 10000)
	if pin.CPUWeight != nil {
		rs.CPUWeight = *pin.CPUWeight
		rs.Pinned = append(rs.Pinned, "cpuWeight")
	}
	rs.DBPool = clamp(int(math.Ceil(c.Conn[r]*n.Cores)), 2, c.MaxPool)
	if pin.DBPool != nil {
		rs.DBPool = *pin.DBPool
		rs.Pinned = append(rs.Pinned, "dbPool")
	}
	rs.DBVisPool = clamp(int(math.Ceil(c.VisConn[r]*n.Cores)), 1, c.MaxPool)
	if pin.DBVisibilityPool != nil {
		rs.DBVisPool = *pin.DBVisibilityPool
		rs.Pinned = append(rs.Pinned, "dbVisibilityPool")
	}
	return rs
}

// meanCPU is the mean of the four roles' CPU use, so a weight of 100 is an average role.
func (c Constants) meanCPU() float64 {
	sum := 0.0
	for _, r := range spec.Roles {
		sum += c.CPU[r]
	}
	return sum / float64(len(spec.Roles))
}

func inStageOrder(roles []string) []string {
	var out []string
	for _, r := range spec.Roles {
		if slices.Contains(roles, r) {
			out = append(out, r)
		}
	}
	return out
}

// floorMiB rounds a limit down to a whole MiB. The epsilon absorbs floating-point error: the
// shares sum to 1.0000000000000002 in binary, so floor + free x share can land a hair under a
// whole MiB, and must not lose it.
func floorMiB(b float64) int64 { return int64(math.Floor(b/mib+1e-9)) * mib }

func clamp(n, lo, hi int) int {
	return max(lo, min(n, hi))
}

func tooSmall(node string, roles []string, why string) error {
	return &term.UserError{
		Code: "refused.node_too_small",
		What: node + " cannot run " + strings.Join(roles, ", "),
		Why:  why,
		Fix:  "give the node more memory, move a role to another node, or lower a pin",
	}
}

// Bytes formats a size the way specs write it: 512M below a GiB, 1.2G above.
func Bytes(b int64) string {
	if b >= 1<<30 {
		return fmt.Sprintf("%.1fG", float64(b)/(1<<30))
	}
	return fmt.Sprintf("%dM", b/mib)
}
