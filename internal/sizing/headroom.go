package sizing

import (
	"cmp"
	"fmt"
	"slices"

	"github.com/2SSK/tops/internal/term"
)

// Headroom is the shard check (TRD "Sizing", refused.headroom): with one history node down, its
// shards move to the others, and the survivor with the least room must still hold its new share.
type Headroom struct {
	HistoryNodes  int    `json:"historyNodes"`
	Down          string `json:"down,omitempty"`
	WorstSurvivor string `json:"worstSurvivor,omitempty"`
	Shards        int    `json:"shards,omitempty"` // the survivor's, after the failover
	Predicted     int64  `json:"predictedBytes"`   // floor + perShard × shards
	Limit         int64  `json:"limitBytes"`       // the survivor's history GOMEMLIMIT
	OK            bool   `json:"ok"`
	Skipped       string `json:"skipped,omitempty"` // why there is nothing to check
}

// CheckHeadroom checks the cluster's history nodes, sized by Size. Every survivor takes an
// equal part of the shards, ceil(shards / (h - 1)), so the worst survivor is the one with
// the smallest history GOMEMLIMIT, and any other history node is the one down.
func CheckHeadroom(c Constants, shards int, nodes []NodeSize) Headroom {
	type history struct {
		node  string
		limit int64
	}
	var hs []history
	for _, n := range nodes {
		for _, r := range n.Roles {
			if r.Role == "history" {
				hs = append(hs, history{n.Name, r.GoMemLimit})
			}
		}
	}
	h := Headroom{HistoryNodes: len(hs), OK: true}
	if len(hs) < 2 {
		h.Skipped = "one history node: no survivor to take its shards"
		return h
	}
	worst := slices.MinFunc(hs, func(a, b history) int { return cmp.Compare(a.limit, b.limit) })
	down := hs[0]
	if down.node == worst.node {
		down = hs[1]
	}
	h.Down, h.WorstSurvivor, h.Limit = down.node, worst.node, worst.limit
	h.Shards = (shards + len(hs) - 2) / (len(hs) - 1) // ceil(shards / (h -1))
	h.Predicted = c.Floor["history"] + c.PerShard*int64(h.Shards)
	h.OK = h.Predicted <= h.Limit
	return h
}

// Refusal is refused.headroom when the check failed, else nil.
func (h Headroom) Refusal() error {
	if h.OK {
		return nil
	}
	return &term.UserError{
		Code: "refused.headroom",
		What: "not applying",
		Why:  fmt.Sprintf("with %s down, %s history would own %d shards: predicted %s, GOMEMLIMIT %s", h.Down, h.WorstSurvivor, h.Shards, Bytes(h.Predicted), Bytes(h.Limit)),
		Fix:  "more history nodes or more memory on " + h.WorstSurvivor,
	}
}
