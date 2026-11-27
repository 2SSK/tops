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
	OpHello    Op = "hello"
	OpFacts    Op = "facts"
	OpStage    Op = "stage"
	OpValidate Op = "validate"
	OpSchema   Op = "schema"
	OpLock     Op = "lock"
	OpWrite    Op = "write"
	OpRestore  Op = "restore"
	OpService  Op = "service"
)

// Timeout is how long one op may take (TRD "Timeouts"): stage downloads releases, schema
// migrates the databases, and service waits for systemd's own stop and start timeouts. The
// helper ends its op then, and with it any fence the op holds
func Timeout(op Op) time.Duration {
	switch op {
	case OpStage, OpSchema:
		return 10 * time.Minute
	case OpService:
		// A restart stops for up to TimeoutStopSec (90 s), kills, and starts for up to
		// TimeoutStartSec (90 s): systemd ends the job within this, so the fence outlives it.
		return 5 * time.Minute
	}
	return time.Minute
}

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
	Applied  *Applied             `json:"applied"`  // what tops last wrote here; nil: never managed by tops
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

// Applied is /var/lib/tops/applied.json: what the last apply wrote on this node.
type Applied struct {
	ApplyID string                 `json:"applyId"`
	Files   map[string]AppliedFile `json:"files"`   // path -> the file as tops wrote it
	Roles   []string               `json:"roles"`   // role units tops enabled
	Current string                 `json:"current"` // the release current pointed to
}

// AppliedFile is one file as tops wrote it.
type AppliedFile struct {
	SHA256 string `json:"sha256"`
	Mode   string `json:"mode"` // "0640"
	Owner  string `json:"owner"`
	Group  string `json:"group"`
}

// Stage is what a node installs before tops writes its files: the temporal user and releases.
type Stage struct {
	User     bool       `json:"user"`
	Releases []Download `json:"releases"`
}

// Download is one release archive to install, verified against the sha256 the workstation read
// from the release's checksums.txt.
type Download struct {
	Kind    string   `json:"kind"`    // "server", "cli"
	Version string   `json:"version"` // "1.31.3"
	URL     string   `json:"url"`
	SHA256  string   `json:"sha256"`
	Extract []string `json:"extract"` // the binaries to take from the archive
}

// StageResult is what stage installed; empty when everything was there already.
type StageResult struct {
	Changed []string `json:"changed"`
}

// Validate is what the helper checks with a staged server release before anything on the
// node changes: each role's files, as that role's temporal-server would read them.
type Validate struct {
	Version string      `json:"version"` // a staged server release, "1.31.3"
	Unit    string      `json:"unit"`    // temporal@.service
	Roles   []RoleFiles `json:"roles"`
}

// RoleFiles is one role's rendered files and environment.
type RoleFiles struct {
	Role          string   `json:"role"`
	Config        string   `json:"config"`        // <role>.yaml
	DynamicConfig string   `json:"dynamicConfig"` // dynamicconfig-<role>.yaml
	Limits        string   `json:"limits"`        // temporal@<role>.service.d/limits.conf
	Env           []string `json:"env"`           // KEY=VALUE: temporal.env, then <role>.env, as systemd passes them
	Secrets       []string `json:"secrets"`       // keys of Env whose values never appear in a message
}

// ValidateResult is what the target binary refused; empty when it accepted every file.
type ValidateResult struct {
	Rejected []Rejection `json:"rejected"`
}

// Rejection is one file the target binary or systemd refused, with its messgae, secrets masked.
type Rejection struct {
	Role    string `json:"role"` // "" for the unit
	File    string `json:"file"` // "config", "dynamicConfig" or "limits", as in RoleFiles, or "unit"
	Message string `json:"message"`
}

// Schema is what the helper creates and migrates with a staged release's temporal-sql-tool:
// each target database, on the PostgreSQL the node reaches.
type Schema struct {
	Release       string         `json:"release"` // a staged server release, "1.31.3"
	Host          string         `json:"host"`
	Port          int            `json:"port"`
	User          string         `json:"user"`
	Password      string         `json:"password"` // to the tool as SQL_PASSWORD, never argv
	TLS           bool           `json:"tls"`
	AdminDatabase string         `json:"adminDatabase"` // to the tool as SQL_PASSWORD, never argv
	Targets       []SchemaTarget `json:"targets"`
}

// SchemaTarget is one database and the schema embedded in the tool that versions it.
type SchemaTarget struct {
	Database   string `json:"database"`   // "temporal"
	SchemaName string `json:"schemaName"` // "postgresql/v12/temporal"
	Create     bool   `json:"create"`     // the workstation found no such database
}

// Lease is /var/lib/tops/lock: the one run that may change this node, until RenewedAt plus
// its TTL by the node's clock.
type Lease struct {
	Env        string    `json:"env"`
	Cluster    string    `json:"cluster"`
	RunID      string    `json:"runId"`      // the request's: fenced ops must carry it
	ApplyID    string    `json:"applyId"`    // the request's
	Command    string    `json:"command"`    // "apply", "rollback 20261023T153012Z-7f3a"
	By         string    `json:"by"`         // "ssk@archbtw"
	PID        int       `json:"pid"`        // the workstation's
	AcquiredAt time.Time `json:"acquiredAt"` // set by the helper, node clock
	RenewedAt  time.Time `json:"renewedAt"`  // set by the helper, node clock
	TTLSeconds int       `json:"ttlSeconds"` // 120
}

// The lock op's actions.
const (
	LockAcquire = "acquire"
	LockRenew   = "renew"
	LockRelease = "release"
)

// Lock asks the helper to acquire, renew or release the node's lease for the request's run.
type Lock struct {
	Action string `json:"action"`
	Lease  Lease  `json:"lease"` // acquire: who and what; the helper sets the run, apply id and times
}

// LockResult says whether the run holds the lease after the action, and whose it is.
type LockResult struct {
	Held     bool      `json:"held"`
	Lease    *Lease    `json:"lease"`    // the node's lease now; nil when free
	Previous *Lease    `json:"previous"` // the stale lease acquire took over
	Now      time.Time `json:"now"`      // the node's clock as it answered: the lease's ages are against it
}

// File is one file write puts in place, whole.
type File struct {
	Path    string `json:"path"`
	Content []byte `json:"content"` // base64 in JSON; secrets travel only here
	Mode    string `json:"mode"`    // "0640"
	Owner   string `json:"owner"`
	Group   string `json:"group"`
}

// Write asks the helper to put files in place and remove others, after it saved what was
// there in the apply's snapshot.
type Write struct {
	Files  []File   `json:"files"`
	Remove []string `json:"remove"`
}

// WriteResult is what write changed: the paths, and the apply id the old state was saved
// under, "" when nothing needed a change.
type WriteResult struct {
	Snapshot string   `json:"snapshot"`
	Written  []string `json:"written"`
	Removed  []string `json:"removed"`
}

// Restore asks the helper to bring back every path an apply changed on the node, as the
// apply's snapshot holds it. The result is a WriteResult: restore is itself a write, saved
// under the request's apply id.
type Restore struct {
	Snapshot string `json:"snapshot"` // the apply id to undo
}

// The service op's actions: one systemd job each; enable-now and disable-now also change
// whether the unit starts at boot.
const (
	ServiceStart      = "start"
	ServiceStop       = "stop"
	ServiceRestart    = "restart"
	ServiceEnableNow  = "enable-now"
	ServiceDisableNow = "disable-now"
)

// Service asks the helper to act on one role's unit and wait for systemd's job to end. The
// result is the unit's UnitState after it.
type Service struct {
	Unit   string `json:"unit"` // temporal@history.service
	Action string `json:"action"`
}
