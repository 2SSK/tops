// Package wire holds the types tops and its node helper exchange as JSON.
package wire

import "encoding/json"

// Detected is one measured property of a node and where its value came from.
type Detected struct {
	Value   float64 `json:"value"`
	Source  string  `json:"source"`  // "meminfo", "cgroup:/sys/fs/cgroup/memory.max", "affinity", "cgroup:cpu.max"
	Machine float64 `json:"machine"` // MemTotal or logical CPUs, for display
}

// Op names one helper operation: tops node <op>.
type Op string

// The ops the helper runs.
const (
	OpHello Op = "hello"
)

// Request is the one JSON document a helper op reads on stdin.
type Request struct {
	RunID   string          `json:"runId"`             // "r-20261023T153012Z-9c1e"
	ApplyID string          `json:"applyId,omitempty"` // fenced ops
	By      string          `json:"by"`
	Args    json.RawMessage `json:"args"`
}

// Response is the one JSON document a helper op writes on stdout.
type Response struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *Error          `json:"error,omitempty"`
}

// Error is why an op did not succeed. Detail never holds a secret.
type Error struct {
	Code    string `json:"code"` // backend schema "Helper error codes"
	Message string `json:"message"`
	Detail  string `json:"detail,omitempty"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// Hello is what a node is: the helper's build, the machine, and its effective size.
type Hello struct {
	HelperVersion string   `json:"helperVersion"`
	Hostname      string   `json:"hostname"`
	MachineID     string   `json:"machineId"` // two spec entries on one host
	OS            string   `json:"os"`        // "ubuntu 24.04"
	Arch          string   `json:"arch"`
	Memory        Detected `json:"memory"` // effective bytes
	Cores         Detected `json:"cores"`  // effective cores, fractional from cpu.max
}
