// Package wire holds the types tops and its node helper exchange as JSON.
package wire

// Detected is one measured property of a node and where its value came from.
type Detected struct {
	Value   float64 `json:"value"`
	Source  string  `json:"source"`  // "meminfo", "cgroup:/sys/fs/cgroup/memory.max", "affinity", "cgroup:cpu.max"
	Machine float64 `json:"machine"` // MemTotal or logical CPUs, for display
}
