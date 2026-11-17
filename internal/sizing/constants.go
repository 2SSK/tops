package sizing

import "fmt"

// Constants are the measured inputs of the formula for one Temporal minor (TRD "Constants
// and how they are measured"). Per-role values are keyed by role.
type Constants struct {
	Temporal   string // the minor these were measured on, "1.31"
	MeasuredAt string // the measurement's date; "" while the values are placeholders
	OSBase     int64  // bytes the OS keeps, idle, without Temporal
	Floor      map[string]int64
	Share      map[string]float64 // of the free memory under load; sums to 1
	Burst      map[string]float64 // MemoryHigh's margin below MemoryMax
	NonHeap    map[string]float64 // GOMEMLIMIT's margin below MemoryHigh
	CPU        map[string]float64 // relative CPU use under load
	Conn       map[string]float64 // default-store connections per core
	VisConn    map[string]float64 // visibility-store connections per core
	MaxPool    int
	PerShard   int64 // history memory per owned shard
}

// placeholder stands in until T2.1b's measurements replace it: every value is a guess sized
// so the VPSLAB nodes fit, and MeasuredAt is empty so tops capacity says so.
var placeholder = Constants{
	OSBase:   160 * mib,
	Floor:    map[string]int64{"frontend": 80 * mib, "history": 128 * mib, "matching": 80 * mib, "worker": 64 * mib},
	Share:    map[string]float64{"frontend": 0.2, "history": 0.45, "matching": 0.25, "worker": 0.1},
	Burst:    map[string]float64{"frontend": 0.1, "history": 0.1, "matching": 0.1, "worker": 0.1},
	NonHeap:  map[string]float64{"frontend": 0.25, "history": 0.25, "matching": 0.25, "worker": 0.25},
	CPU:      map[string]float64{"frontend": 1, "history": 2, "matching": 1.2, "worker": 0.25},
	Conn:     map[string]float64{"frontend": 1, "history": 4, "matching": 2, "worker": 1},
	VisConn:  map[string]float64{"frontend": 2, "history": 1, "matching": 0.5, "worker": 0.5},
	MaxPool:  50,
	PerShard: 256 << 10,
}

// For returns the constants for a Temporal minor ("1.31").
func For(minor string) (Constants, error) {
	switch minor {
	case "1.30", "1.31":
		c := placeholder
		c.Temporal = minor
		return c, nil
	}
	return Constants{}, fmt.Errorf("no sizing constants for Temporal %s", minor)
}
