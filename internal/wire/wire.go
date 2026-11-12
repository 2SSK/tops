// Package wire holds the types tops and its node helper exchange as JSON.
package wire

import (
	"encoding/json"
	"time"
)

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
	OpFacts Op = "facts"
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
	HelperVersion string     `json:"helperVersion"`
	SHA256        string     `json:"sha256"` // of the helper binary itself: the workstation checks the upload
	Hostname      string     `json:"hostname"`
	MachineID     string     `json:"machineId"` // two spec entries on one host
	OS            string     `json:"os"`        // "ubuntu 24.04"
	Arch          string     `json:"arch"`
	Memory        Detected   `json:"memory"` // effective bytes
	Cores         Detected   `json:"cores"`  // effective cores, fractional from cpu.max
	ClockSynced   bool       `json:"clockSynced"`
	Listeners     []Listener `json:"listeners"` // every TCP listener; the workstation checks the ports it needs
}

// Listener is one listening TCP socket and the processes that hold it.
type Listener struct {
	Port    int    `json:"port"`
	Address string `json:"address"` // "0.0.0.0", "::", "127.0.0.1"
	Process string `json:"process"` // comm of each holder, sorted and joined: "sshd,systemd"; "" if none is visible
}

// FactsArgs asks for the state of the files tops renders.
type FactsArgs struct {
	Want map[string]string `json:"want"` // absolute path -> the sha256 tops would write
	Dirs []string          `json:"dirs"` // tops-owned directories: a file there not in Want in unknown
}

// Facts is what is on a node now, compare with what tops would write.
type Facts struct {
	Files    map[string]FileState `json:"files"`    // every path of FactsArgs.Want
	Unknown  []string             `json:"unknown"`  // files in tops-owned dirs that tops did not render
	Current  string               `json:"current"`  // target of /opt/temporal/current, "" if absent
	Releases []string             `json:"releases"` // directories under /opt/temporal/releases
	Units    map[string]UnitState `json:"units"`    // every temporal@*.service instance systemd has loaded, and temporal-ui
}

// UnitState is one systemd unit as systemd reports it.
type UnitState struct {
	Active    string    `json:"active"` // systemd ActiveState: active, inactive, failed, activating...
	Enabled   bool      `json:"enabled"`
	MainPID   int       `json:"mainPid"`   // 0 when not running
	Exe       string    `json:"exe"`       // the binary MainPID runs, ""  when not running
	Since     time.Time `json:"since"`     // when it last became active;
	NRestarts int       `json:"nRestarts"` // restarts by systemd since it was started
}

// FileState is one file as it is on the node.
type FileState struct {
	Exists  bool   `json:"exists"`
	SHA256  string `json:"sha256,omitempty"`
	Mode    string `json:"mode,omitempty"` // "0640"
	Owner   string `json:"owner,omitempty"`
	Group   string `json:"group,omitempty"`
	Content []byte `json:"content,omitempty"` // only when SHA256 differs from the wanted one; base64 in JSON
}
