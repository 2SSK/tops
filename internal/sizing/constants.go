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

// measured131 are the constants measured on VPSLAB for Temporal 1.31.3 (testdata/README.md):
// a quick set, one idle and one 5-minute reference-load run, on the quiet power profile.
// TestConstantsFromMeasurements recomputes every measured value from testdata/measure-*.json.
// MaxPool and PerShard are not measured yet and keept their first guesses; history's and
var measured131 = Constants{
	MeasuredAt: "2026-11-21",
	OSBase:     444 * mib,
	Floor:      map[string]int64{"frontend": 37 * mib, "history": 130 * mib, "matching": 42 * mib, "worker": 40 * mib},
	Share:      map[string]float64{"frontend": 0.224, "history": 0.657, "matching": 0.065, "worker": 0.054},
	Burst:      map[string]float64{"frontend": 0.054, "history": 0.046, "matching": 0.065, "worker": 0.028},
	NonHeap:    map[string]float64{"frontend": 0.536, "history": 0.45, "matching": 0.478, "worker": 0.386},
	CPU:        map[string]float64{"frontend": 27.1, "history": 49.1, "matching": 14.6, "worker": 3.1},
	Conn:       map[string]float64{"frontend": 3, "history": 20, "matching": 20, "worker": 2},
	VisConn:    map[string]float64{"frontend": 1.5, "history": 2, "matching": 1.5, "worker": 1},
	MaxPool:    50,
	PerShard:   256 << 10,
}

// For returns the constants for a Temporal minor ("1.31").
func For(minor string) (Constants, error) {
	switch minor {
	case "1.30", "1.31":
		c := measured131
		c.Temporal = minor
		return c, nil
	}
	return Constants{}, fmt.Errorf("no sizing constants for Temporal %s", minor)
}
