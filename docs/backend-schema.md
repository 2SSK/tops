# tops backend schema

Project: tops (Temporal Ops), `github.com/2SSK/tops`. Status: draft. Date: 2026-10-23.
Sibling docs: [PRD](prd.md) · [TRD](trd.md) · [App flow](app-flow.md) · [Design brief](design-brief.md) ·
[Implementation plan](implementation-plan.md) · [Concepts](concepts.md) · [Roadmap](roadmap.md)

Every data shape tops reads or writes: the fleet repo, the spec, settings, secrets, the catalog, sizing, the
helper protocol, node state, the run log, files on a node, metrics, `--json` outputs, and Temporal
persistence. Rules and algorithms are in the [TRD](trd.md). Numbers in examples are illustrative until T2.1
measures the sizing constants. Examples use tops' e2e fixture fleet (`test/e2e/fleet`, environment `lab`,
cluster `tops-e2e`).

Conventions for every JSON shape: field names camelCase; times RFC 3339 UTC; sizes in bytes (`…Bytes`);
durations in milliseconds (`…Ms`); absent optional fields are omitted, empty lists are `[]`, never `null`.

## Where data lives

| Data                       | Location                                                       | Owner            | Lifetime          |
| -------------------------- | -------------------------------------------------------------- | ---------------- | ----------------- |
| Intent                     | Fleet repo: `base/`, `environments/<env>/{cluster,overrides}.yaml` | Operator (git) | Forever           |
| Secrets                    | `environments/<env>/secrets.env`, 0600, untracked              | Operator         | Forever           |
| Catalog, classes, templates, sizing constants | Embedded in the tops binary                 | tops release     | Per tops version  |
| Run log                    | `${XDG_STATE_HOME:-~/.local/state}/tops/runs/<run-id>.jsonl`   | Workstation user | Newest 100        |
| Helper binaries (other arch) | `${XDG_CACHE_HOME:-~/.cache}/tops/<version>/tops-linux-<arch>` | Workstation user | Until deleted     |
| Profiles, reports          | `./pprof-*.pb.gz`, `./investigate-*.md` in the working directory | Operator       | Until deleted     |
| Helper on a node           | `/usr/local/lib/tops/<sha16>/tops`                             | root             | Newest 3          |
| Lock, snapshots, history, applied | `/var/lib/tops/` on each node                           | root             | "Node state"      |
| Scratch                    | `/run/tops/<run-id>/` on each node                             | root             | One helper op     |
| Reality                    | systemd, files, iptables; Temporal APIs; the datastore         | -                | Read every run    |

## Fleet repo layout

```
<root>/                         found by walking up to the first directory with environments/ (or $TOPS_ROOT)
├── base/                       dynamic config shared by every environment (all optional)
│   ├── common.yaml             keys delivered to every role that reads them
│   ├── frontend.yaml
│   ├── history.yaml
│   ├── matching.yaml
│   └── worker.yaml
└── environments/
    ├── lab/
    │   ├── cluster.yaml        complete spec of this cluster
    │   ├── overrides.yaml      dynamic config that differs here (optional)
    │   └── secrets.env         0600, untracked
    └── staging/
        ├── cluster.yaml
        ├── overrides.yaml
        └── secrets.env
```

Merge order per role (later wins, whole key): Temporal default -> `base/common.yaml` -> `base/<role>.yaml` ->
`environments/<env>/overrides.yaml`; only keys the catalog says the role reads (TRD "Dynamic-config merge
order"). The fleet repo's `.gitignore` must contain `environments/*/secrets.env`; `validate` warns when
`git check-ignore` says otherwise (and when the fleet root is not a git repository, skips the check).

Environment names: `^[a-z][a-z0-9-]{0,30}$`. Other files under `environments/<env>/` are ignored.

## Cluster spec (`environments/<env>/cluster.yaml`)

### v1 example (tops' e2e fixture, as of T6)

```yaml
cluster: tops-e2e
temporal:
  version: 1.31.3
  cli: 1.9.1
  logLevel: info
shards: 512
persistence:
  postgres:
    host: 10.80.0.20
    port: 5432
    user: temporal
    tls: false
ssh:
  user: deploy
  key: ~/Code/Projects/building/tops/lab/keys/id_ed25519
  knownHosts: ~/Code/Projects/building/tops/lab/keys/known_hosts
access:
  clients: [10.80.0.1]
  monitors: [10.80.0.1]
  admins: [10.80.0.1]
  healthchecks: []
namespaces:
  - { name: default, retention: 72h }
  - { name: drills, retention: 24h }
ui:
  version: 2.54.1
nodes:
  - name: vps-01
    address: 10.80.0.11
    roles: [frontend, history, matching, worker, ui]
  - name: vps-02
    address: 10.80.0.12
    roles: [frontend, history, matching, worker]
  - name: vps-03
    address: 10.80.0.13
    roles: [frontend, matching]
    pin:
      matching: { memoryMax: 512M }
```

`10.80.0.1` is the workstation as the VPSLAB nodes see it (the Docker network gateway). Before T6 the fixture
has no `ui` block and no `ui` role; before T2.4 no `pin`.

### Fields

"Fixed" = fixed at creation; `plan`/`apply` refuse a change. "Upgrade" = changed only by `tops upgrade`.
"Since" = the step that adds the field to `spec.Cluster` (fields not yet added are unknown keys, an error).

| Field                                | Type              | Default            | Validation                                                                                 | Change        | Since |
| ------------------------------------ | ----------------- | ------------------ | ------------------------------------------------------------------------------------------ | ------------- | ----- |
| `cluster`                            | string            | required           | `^[a-z][a-z0-9-]{1,30}$`; Temporal's `currentClusterName`                                  | Fixed         | done  |
| `temporal.version`                   | semver            | required           | `^\d+\.\d+\.\d+$`; minor 1.30 or 1.31 (D13)                                                | Upgrade       | done  |
| `temporal.cli`                       | semver            | required           | `^\d+\.\d+\.\d+$`; release exists (checked at stage)                                       | apply         | done  |
| `temporal.logLevel`                  | enum              | `info`             | `debug`, `info`, `warn`, `error`                                                           | apply (restart all) | done |
| `shards`                             | int               | required           | 1-16384                                                                                    | Fixed         | done  |
| `persistence.postgres.host`          | string            | required           | IPv4 or DNS name                                                                           | apply (identity-checked) | done |
| `persistence.postgres.port`          | int               | `5432`             | 1-65535                                                                                    | apply (identity-checked) | done |
| `persistence.postgres.user`          | string            | required           | `^[A-Za-z_][A-Za-z0-9_]{0,62}$`                                                            | apply (restart all) | done |
| `persistence.postgres.tls`           | bool              | `false`            | -                                                                                          | apply (restart all) | done |
| `persistence.postgres.adminDatabase` | string            | `postgres`         | Used for existence checks and `create-database`                                            | apply         | done  |
| persistence kind                     | which key is set  | exactly one        | v1: `postgres`                                                                             | Fixed         | done  |
| `ssh.user`                           | string            | required           | Non-empty; passwordless sudo (preflight)                                                   | -             | done  |
| `ssh.port`                           | int               | `22`               | 1-65535                                                                                    | -             | done  |
| `ssh.key`                            | path              | none (agent)       | `~` expanded; relative to the environment dir; not group/world-readable (T2.3)             | -             | done  |
| `ssh.knownHosts`                     | path              | `~/.ssh/known_hosts` | `~` expanded; relative to the environment dir. Coverage of every node's address is checked when connecting (plan, apply, …), never by offline `validate`, so a files-only environment validates | -             | done  |
| `access.clients`                     | IPv4/CIDR list    | `[]`               | Valid, no duplicates                                                                       | apply (firewall, T6) | done |
| `access.monitors`                    | IPv4/CIDR list    | `[]`               | as above                                                                                   | apply (firewall, T6) | done |
| `access.admins`                      | IPv4/CIDR list    | `[]`               | as above                                                                                   | apply (firewall, nginx, T6) | done |
| `access.healthchecks`                | IPv4/CIDR list    | `[]`               | as above                                                                                   | apply (firewall, nginx, T6) | P0.3 (validation) |
| `namespaces[].name`                  | string            | -                  | `^[a-zA-Z0-9][a-zA-Z0-9._-]{0,254}$`, unique, not `temporal-system`                        | apply / ns ensure | done |
| `namespaces[].retention`             | duration          | required           | Go duration or `<n>d`; >= 1h                                                               | apply / ns ensure | done |
| `nodes`                              | list              | required           | >= 1; order = rollout order within a role                                                   | node add/remove | done |
| `nodes[].name`                       | string            | required           | `^[a-z0-9][a-z0-9-]{0,62}$`, unique                                                        | node add/remove | done |
| `nodes[].address`                    | IPv4              | required           | Unique; SSH address, broadcast address, firewall peer, ring identity                       | Fixed per node | done |
| `nodes[].roles`                      | list              | required           | Non-empty; each `frontend`, `history`, `matching`, `worker` (and `ui` from T6.2); no duplicates; each Temporal role on >= 1 node | apply (start/stop role) | P0.5 |
| `nodes[].pin.<role>.memoryMax`       | size (`512M`, `2G`, binary units) | formula | Role listed on the node, not `ui`; >= the role's floor                               | apply (restart role) | T2.4 |
| `nodes[].pin.<role>.cpuWeight`       | int               | formula            | 1-10000                                                                                    | apply (restart role) | T2.4 |
| `nodes[].pin.<role>.dbPool`          | int               | formula            | >= 1                                                                                       | apply (restart role) | T2.4 |
| `nodes[].pin.<role>.dbVisibilityPool`| int               | formula            | >= 1                                                                                       | apply (restart role) | T2.4 |
| `ui.version`                         | semver            | required iff a node lists `ui` | ui-server release exists (stage)                                               | apply (ui-restart) | T6.2 |
| `ui.temporalAddress`                 | host:port         | none               | Required iff a `ui` node runs no `frontend`; else the UI dials `127.0.0.1:7233`           | apply (ui-restart) | T6.2 |

Removed in P0.4 (they exist in the code today and are obsolete): `profile` (replaced by per-node sizing),
`secrets` (the path is always `environments/<env>/secrets.env`), `dynamicConfig` (replaced by `base/` and
`overrides.yaml`), `ui.node` (replaced by the `ui` role in T6.2; the whole `ui` block leaves until then).

Unknown keys are errors with a line number. Every `Problem` is `{path, line, message}`.

### Immutables

| Immutable          | Live value                                      | Read from                                |
| ------------------ | ----------------------------------------------- | ---------------------------------------- |
| `cluster`          | `cluster_name`                                  | `AdminService.DescribeCluster`           |
| `shards`           | `history_shard_count`                           | same                                     |
| persistence kind   | `persistence_store`, `visibility_store`         | same                                     |
| `nodes[].address`  | ring identity `ip:port`                         | same                                     |
| store identity     | `cluster_name`, `cluster_id` in `cluster_metadata_info` through the spec's address | datastore reader |
| v2: `cassandra.keyspace`, `elasticsearch.index` | values in the live `<role>.yaml` | helper `facts`              |
| v2: `cassandra.datacenter`, `replicationFactor` | replication of the live keyspace | `system_schema.keyspaces`   |

## Dynamic config files

`base/<role>.yaml`, `base/common.yaml` and `environments/<env>/overrides.yaml` use Temporal's own file format,
so a rendered file is the merge of these maps:

```yaml
# base/history.yaml
history.cacheTTL:
  - value: 15m
# environments/lab/overrides.yaml
frontend.namespaceRPS:
  - value: 400
    constraints: { namespace: drills }
  - value: 2400
```

A key maps to a list of `{value, constraints?}`; the entry without constraints is the fallback. Values are
checked against the catalog type; constraint names against the key's precedence (`namespace`,
`namespaceId`, `taskQueueName`, `taskQueueType`, `shardId`, `taskType`, `destination`). Keys compare
case-insensitively.

## Secrets (`environments/<env>/secrets.env`)

```
# secrets.env - lab. Mode 0600. Never commit.
TOPS_PG_PASSWORD=<the vps-pg POSTGRES_PASSWORD>
```

| Key                       | Required when                       | Validation                                       | Since |
| ------------------------- | ----------------------------------- | ------------------------------------------------ | ----- |
| `TOPS_PG_PASSWORD`        | `persistence.postgres`              | `^[A-Za-z0-9._~-]+$` (goes into a YAML string and a systemd env file) | T1.5 |
| `TOPS_CASSANDRA_PASSWORD` | v2, `persistence.cassandra.user` set | same                                            | T10.2 |
| `TOPS_ES_PASSWORD`        | v2, `persistence.elasticsearch.user` set | same                                        | T10.2 |

Grammar: one `KEY=VALUE` per line, `#` comment lines, blank lines; no quotes, no `export`, no `$` expansion,
no trailing spaces; unknown keys are errors; a missing required key is an error. File: regular, mode 0600,
owner the current user (`secrets.mode` otherwise).

## Settings catalog

`internal/catalog/catalog-<minor>.json`, generated by `tools/cataloggen` (TRD "Settings catalog"):

```json
{
  "temporal": "1.31.3",
  "generatedFrom": "github.com/temporalio/temporal@v1.31.3 common/dynamicconfig/constants.go",
  "settings": [
    {
      "key": "history.hostLevelCacheMaxSizeBytes",
      "type": "int",
      "precedence": "global",
      "constraints": [],
      "default": 1048576000,
      "defaultExpr": "256000*4*1024",
      "description": "…",
      "roles": ["history"],
      "candidate": "unknown"
    }
  ]
}
```

| Field         | Values                                                                                        |
| ------------- | --------------------------------------------------------------------------------------------- |
| `type`        | `bool`, `int`, `float`, `string`, `duration`, `map`, `typed` (a struct; validated by the node's `validate-dynamic-config`) |
| `precedence`  | `global`, `namespace`, `namespaceId`, `taskQueue`, `shardId`, `taskType`, `destination`, `chasmTaskType` |
| `constraints` | Constraint names allowed in files for this precedence                                         |
| `default`     | Evaluated value; `null` with `defaultExpr` kept when not constant; a list for constrained defaults |
| `roles`       | Any of `frontend`, `history`, `matching`, `worker`, or `["shared"]` (delivered to every role)  |
| `candidate`   | Static hint: `hot`, `restart`, `unknown`                                                      |

Settings are sorted by key; the file has no timestamp, so a re-run is byte-identical.

`internal/catalog/class-<minor>.yaml` (hand-made):

```yaml
<key>:
  apply: hot | restart
  evidence: "<file and function of the call site, and what it does with the value>"
  lab: pending | "<date>: <what was changed and observed>"     # required for hot from T4.3 on
```

Effective class = the entry's `apply`, else `unknown` (planned as restart).

`tools/cataloggen/versions.txt`: one Temporal version per line, the patch release of each supported minor
(`1.30.x`, `1.31.3`).

## Sizing

Inputs per node: `hello` (effective memory and cores with sources), the node's roles and pins, the embedded
constants for the spec's minor, and PostgreSQL's limits (`SHOW max_connections`,
`superuser_reserved_connections`, `reserved_connections`, `pg_stat_activity` count of other users).

```go
type RoleSize struct {
	Role       string   `json:"role"`
	MemoryMax  int64    `json:"memoryMaxBytes"`
	MemoryHigh int64    `json:"memoryHighBytes"`
	GoMemLimit int64    `json:"goMemLimitBytes"`
	CPUWeight  int      `json:"cpuWeight"`
	DBPool     int      `json:"dbPool"`
	DBVisPool  int      `json:"dbVisibilityPool"`
	Pinned     []string `json:"pinned"`  // fields that came from a pin
	Formula    string   `json:"formula"` // e.g. "floor 140M + free 1.2G × share 0.41"
}
```

`internal/sizing/testdata/measure-<date>.json` (raw evidence, T2.1b):

```json
{
  "temporal": "1.31.3",
  "load": {"tool": "omes", "commit": "<sha>", "scenario": "workflow_with_single_noop_activity", "rate": "<n>/s", "durationS": 900},
  "workstation": {"memAvailableMinBytes": 4831838208},
  "runs": [
    {"node": "vps-01", "capMemoryBytes": 1342177280, "capCpus": 1.5, "roles": ["frontend","history","matching","worker"],
     "phase": "idle|load|double", "run": 1,
     "samples": [{"t": "2026-11-02T10:00:01Z", "unit": "temporal@history",
                  "memoryCurrentBytes": 0, "memoryPeakBytes": 0, "cpuUsageUsec": 0,
                  "memHeapInuseBytes": 0, "sqlInUse": 0, "sqlVisInUse": 0, "gomaxprocs": 0}]}
  ],
  "s3FitVps03": {"capMemoryBytes": 805306368, "roles": ["frontend","history","matching","worker"], "freeBytes": 0, "fits": true},
  "perShard": [{"shards": 128, "memoryCurrentBytes": 0}, {"shards": 256, "memoryCurrentBytes": 0}, {"shards": 512, "memoryCurrentBytes": 0}]
}
```

`internal/sizing/constants.go` holds one `Constants` value per minor (`OSBase`, `Aux`, `Floor`, `Share`,
`Burst`, `NonHeap`, `CPU`, `Conn`, `VisConn`, `MaxPool`, `PerShard`), each recomputed from the samples by a
test.

## Node helper protocol

### Envelope

```go
package wire

type Op string // "hello", "facts", "validate", "stage", "schema", "lock", "write", "restore",
               // "service", "history", "top", "journal", "run"

type Request struct {
	RunID   string          `json:"runId"`             // "r-20261023T153012Z-9c1e"
	ApplyID string          `json:"applyId,omitempty"` // fenced ops
	By      string          `json:"by"`                // "ssk@thinkpad"
	Args    json.RawMessage `json:"args"`
}

type Response struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *Error          `json:"error,omitempty"`
}

type Error struct {
	Code    string `json:"code"`             // see "Helper error codes"
	Message string `json:"message"`
	Detail  string `json:"detail,omitempty"` // never secrets
}

type Event struct {
	Kind string          `json:"kind"` // "journal", "progress", "output"
	Data json.RawMessage `json:"data"`
}
```

### Ops

| Op         | Args                                                                                   | Result                                       | Stream   |
| ---------- | -------------------------------------------------------------------------------------- | -------------------------------------------- | -------- |
| `hello`    | `{}`                                                                                   | `Hello`                                      | no       |
| `facts`    | `{ "want": {"<path>": "<sha256>"}, "dirs": ["/etc/temporal", …] }`                     | `Facts`                                      | no       |
| `validate` | `{ "release": "1.31.3", "roles": [{"role", "files": [File], "env": {"K": "V"}}], "units": [File] }` | `{ "checks": [{"name", "role", "ok", "detail"}] }` | no |
| `stage`    | `Stage`                                                                                | `{ "changed": ["…"] }`                       | progress |
| `schema`   | `Schema`                                                                               | `{ "databases": [{"name", "before", "after", "created"}] }` | progress |
| `lock`     | `{ "action": "acquire"\|"renew"\|"release", "lease": Lease, "takeOverStale": bool }`   | `{ "held": bool, "lease": Lease, "previous": Lease? }` | no |
| `write`    | `{ "files": [File], "remove": ["<path>"], "symlinks": [{"path", "target"}], "roles": {"enable": [], "disable": []}, "effects": ["daemon-reload", "firewall", "nginx", "journald"] }` | `WriteResult` | no |
| `restore`  | `{ "snapshot": "<apply-id>" }`                                                         | `WriteResult`                                | no       |
| `service`  | `{ "unit": "temporal@history.service", "action": "start"\|"stop"\|"restart"\|"enable-now"\|"disable-now" }` | `UnitState`      | no       |
| `history`  | `{ "action": "append", "record": History }` or `{ "action": "list", "limit": 20 }`     | `{ "records": [History] }`                   | no       |
| `top`      | `{ "units": ["…"], "sampleMs": 1000 }`                                                 | `{ "units": {"<unit>": Top} }`               | no       |
| `journal`  | `{ "units": ["…"], "since", "until", "lines": 200, "grep": ["…"], "follow": bool }`    | `{ "count": n }`                             | `journal` |
| `run` (v1.1) | `{ "role": "history" }`                                                              | `{ "exitCode": n }`                          | `output` |

### Types

```go
type Hello struct {
	HelperVersion string     `json:"helperVersion"`
	SHA256        string     `json:"sha256"`
	Hostname      string     `json:"hostname"`
	MachineID     string     `json:"machineId"` // two spec entries on one host
	OS            string     `json:"os"`        // "ubuntu 24.04"
	Arch          string     `json:"arch"`
	Memory        Detected   `json:"memory"`    // effective bytes
	Cores         Detected   `json:"cores"`     // effective cores (fractional from cpu.max)
	ClockSynced   bool       `json:"clockSynced"`
	Listeners     []Listener `json:"listeners"` // TCP listeners on ports tops needs: {port, address, process}
}

type Detected struct {
	Value   float64 `json:"value"`
	Source  string  `json:"source"`  // "meminfo", "cgroup:/sys/fs/cgroup/memory.max", "affinity", "cgroup:cpu.max"
	Machine float64 `json:"machine"` // MemTotal / logical CPUs, for display
}

type File struct {
	Path    string `json:"path"`
	Content string `json:"content"` // base64 for binary; secrets travel only here
	Mode    string `json:"mode"`    // "0640"
	Owner   string `json:"owner"`
	Group   string `json:"group"`
}

type Facts struct {
	Files    map[string]FileState `json:"files"`   // {sha256, mode, owner, group, exists, content?}; content only when sha256 != want
	Unknown  []string             `json:"unknown"` // files in tops-owned dirs that tops did not render
	Current  string               `json:"current"` // target of /opt/temporal/current
	Releases []string             `json:"releases"`
	Units    map[string]UnitState `json:"units"`   // every temporal@*.service instance found, plus temporal-ui
	Firewall *Firewall            `json:"firewall,omitempty"` // T6
	Applied  *Applied             `json:"applied"` // nil = never managed by tops
	Lock     *Lease               `json:"lock"`
	Removed  *History             `json:"removed,omitempty"` // last node-remove record
}

type UnitState struct {
	Active    string    `json:"active"`  // systemd ActiveState
	Enabled   bool      `json:"enabled"`
	MainPID   int       `json:"mainPid"`
	Exe       string    `json:"exe"`
	Since     time.Time `json:"since"`
	NRestarts int       `json:"nRestarts"`
}

type WriteResult struct {
	Snapshot string   `json:"snapshot"` // apply id the snapshot was stored under
	Written  []string `json:"written"`
	Removed  []string `json:"removed"`
	Effects  []string `json:"effects"`  // effects that ran
}

type Top struct {
	CPUPercentOfCore float64          `json:"cpuPercentOfCore"`
	CPUWeight        int              `json:"cpuWeight"`
	CPUPressure10    float64          `json:"cpuPressureAvg10"`
	MemCurrent       int64            `json:"memCurrentBytes"`
	MemPeak          int64            `json:"memPeakBytes"`
	MemHigh          int64            `json:"memHighBytes"` // 0 = max (unset)
	MemMax           int64            `json:"memMaxBytes"`
	MemPressure10    float64          `json:"memPressureAvg10"`
	Events           map[string]int64 `json:"events"` // memory.events: high, max, oom, oom_kill
	NRestarts        int              `json:"nRestarts"`
	Since            time.Time        `json:"since"`
	GoMemLimit       string           `json:"goMemLimit"`
}

type Stage struct {
	User     bool       `json:"user"`
	Packages []string   `json:"packages"` // T6: "nginx", "prometheus-node-exporter"
	Releases []Download `json:"releases"`
}

type Download struct {
	Kind    string   `json:"kind"`    // "server", "cli", "ui"
	Version string   `json:"version"`
	URL     string   `json:"url"`
	SHA256  string   `json:"sha256"`  // from checksums.txt, fetched by the workstation
	Extract []string `json:"extract"` // ["temporal-server", "temporal-sql-tool", "tdbg"]
}

type Schema struct {
	Tool     string         `json:"tool"`    // "temporal-sql-tool"
	Release  string         `json:"release"` // tool from this staged release
	Action   string         `json:"action"`  // "ensure" (create if missing, setup 0.0, update) | "update"
	Endpoint string         `json:"endpoint"`
	Port     int            `json:"port"`
	User     string         `json:"user"`
	Password secret.Value   `json:"password"` // passed to the tool as SQL_PASSWORD, never argv
	TLS      bool           `json:"tls"`
	Targets  []SchemaTarget `json:"targets"` // [{database: temporal, schemaName: postgresql/v12/temporal}, …]
}

type JournalEntry struct {
	TS     time.Time         `json:"ts"`
	Node   string            `json:"node"`  // added by the workstation
	Unit   string            `json:"unit"`  // temporal@history.service
	Level  string            `json:"level"`
	Msg    string            `json:"msg"`
	Fields map[string]string `json:"fields"`
	Raw    string            `json:"raw"`
}
```

### Helper error codes

| Code                    | Meaning                                                     |
| ----------------------- | ----------------------------------------------------------- |
| `helper.not_root`       | Not run through sudo                                        |
| `helper.bad_request`    | stdin did not decode as the op's args                       |
| `lock.lost`             | Fenced op: the lease names another run, or expired          |
| `file.write`            | Write, fsync or rename failed (path in detail)              |
| `systemd.job_failed`    | A unit job ended in `failed` (unit and result in detail)    |
| `release.checksum`      | Downloaded archive does not match the given sha256          |
| `release.download`      | HTTP error fetching a release                               |
| `schema.failed`         | Schema tool exited non-zero (stderr without secrets)        |
| `validate.failed`       | A check failed (each check in the result)                   |
| `snapshot.missing`      | `restore` of a rotated-out snapshot                         |

## Node state (`/var/lib/tops`)

```
/var/lib/tops/                       0700 root:root
├── lock                             0600  Lease (absent = free)
├── lock.flock                       0600  empty; flock(2) target serialising fenced ops and takeover
├── applied.json                     0600  Applied
├── history/<apply-id>.json          0600  History, newest 100
└── snapshots/<apply-id>/            0700  newest 10
    ├── manifest.json                0600  SnapshotManifest
    └── files/<sha256>               0600  content as it was
```

Apply id `YYYYMMDDTHHMMSSZ-xxxx` (UTC, 4 random hex), shared by every node in one run; run id is `r-` + the
same pattern. Read-only runs have a run id but no apply id.

```go
type Lease struct {
	Env        string    `json:"env"`
	Cluster    string    `json:"cluster"`
	RunID      string    `json:"runId"`
	ApplyID    string    `json:"applyId"`
	Command    string    `json:"command"`   // "apply", "rollback 20261023T153012Z-7f3a"
	By         string    `json:"by"`        // "ssk@thinkpad"
	PID        int       `json:"pid"`       // workstation pid
	AcquiredAt time.Time `json:"acquiredAt"`
	RenewedAt  time.Time `json:"renewedAt"`
	TTLSeconds int       `json:"ttlSeconds"` // 120
}

type Applied struct {
	ApplyID string                 `json:"applyId"`
	Files   map[string]AppliedFile `json:"files"`   // path -> {sha256, mode, owner, group}
	Roles   []string               `json:"roles"`   // role units tops enabled
	Current string                 `json:"current"`
}

// History is the audit record: who changed what on this node, when, and with what result.
type History struct {
	ApplyID     string    `json:"applyId"`
	RunID       string    `json:"runId"`
	Command     string    `json:"command"`
	By          string    `json:"by"`
	TopsVersion string    `json:"topsVersion"`
	Env         string    `json:"env"`
	Cluster     string    `json:"cluster"`
	Node        string    `json:"node"`
	SpecSHA256  string    `json:"specSha256"` // cluster.yaml + base/ + overrides.yaml, as loaded
	StartedAt   time.Time `json:"startedAt"`
	FinishedAt  time.Time `json:"finishedAt"`
	Changes     []Change  `json:"changes"`    // {path, role, action: create|update|remove, effect, beforeSha256, afterSha256}
	Restarted   []string  `json:"restarted"`  // roles restarted on this node
	Started     []string  `json:"started"`
	Stopped     []string  `json:"stopped"`
	Result      string    `json:"result"`     // ok | failed | interrupted
	Error       string    `json:"error,omitempty"`
	RollbackOf  string    `json:"rollbackOf,omitempty"`
	Snapshot    bool      `json:"snapshot"`   // false once rotated out
}

type SnapshotManifest struct {
	ApplyID string          `json:"applyId"`
	Entries []SnapshotEntry `json:"entries"`
}

type SnapshotEntry struct {
	Path    string `json:"path"`
	Kind    string `json:"kind"`              // "file", "symlink", "absent"
	SHA256  string `json:"sha256,omitempty"`  // file: key under files/
	Target  string `json:"target,omitempty"`  // symlink
	Mode    string `json:"mode,omitempty"`
	Owner   string `json:"owner,omitempty"`
	Group   string `json:"group,omitempty"`
}
```

History records never contain file contents or secrets: only paths and hashes.

## Run log (workstation)

`${XDG_STATE_HOME:-~/.local/state}/tops/runs/<run-id>.jsonl`, one `slog` JSON record per line:

```json
{"time":"2026-10-23T15:30:12.004Z","level":"INFO","msg":"step","run_id":"r-20261023T153012Z-9c1e","env":"lab","cluster":"tops-e2e","command":"apply","node":"vps-01","role":"history","op":"service","action":"restart","duration_ms":38211,"result":"ok"}
```

| Field          | Present on                         |
| -------------- | ---------------------------------- |
| `time`, `level`, `msg`, `run_id`, `env`, `cluster`, `command` | every record |
| `node`, `role`, `op`, `action`, `duration_ms`, `result`        | per-step records |
| `error_code`, `error`                                          | failures |
| `helper`                                                        | helper stderr records, copied with `node` |

## Files tops installs on a node

`E` = effect class (TRD "Plan and apply"); `T` = step that starts managing the path. tops owns
`/etc/temporal`, `/etc/temporal-ui`, `/usr/local/lib/tops`, `/var/lib/tops` entirely: files there it did not
render are shown as removals.

| Path                                                      | Owner:group    | Mode | E                 | T  |
| --------------------------------------------------------- | -------------- | ---- | ----------------- | -- |
| `/usr/local/lib/tops/<sha16>/tops`                        | root:root      | 0755 | -                 | T2.3 |
| `/var/lib/tops/…`                                         | root:root      | 0700 | -                 | T3.4 |
| `/run/tops/<run-id>/`                                     | root:root      | 0700 | -                 | T3.2 |
| `/etc/temporal/<role>.yaml`                               | root:temporal  | 0640 | restart role      | T3.5 |
| `/etc/temporal/<role>.env`                                | root:temporal  | 0640 | restart role      | T3.5 |
| `/etc/temporal/temporal.env`                              | root:root      | 0600 | restart all roles | T3.5 |
| `/etc/temporal/dynamicconfig-<role>.yaml`                 | root:temporal  | 0640 | hot or restart role | T3.5 |
| `/etc/systemd/system/temporal@.service`                   | root:root      | 0644 | restart all roles | T3.5 |
| `/etc/systemd/system/temporal@<role>.service.d/limits.conf` | root:root    | 0644 | restart role      | T3.5 |
| user `temporal`, `/var/lib/temporal` (0750)               | -              | -    | -                 | T3.1 |
| `/opt/temporal/releases/<v>/`, `current`                  | root:root      | 0755 | version           | T3.1 |
| `/opt/temporal/cli/<v>/temporal`, `/usr/local/bin/{temporal,tdbg,temporal-sql-tool}` | root:root | 0755 | - | T3.1 |
| `/etc/temporal/firewall.rules`, `firewall6.rules`, `temporal-firewall.service` | root:root | 0644 | firewall | T6.1 |
| `/etc/nginx/conf.d/temporal.conf`, allow lists           | root:root      | 0644 | nginx             | T6.1 |
| `/etc/systemd/journald.conf.d/temporal.conf`              | root:root      | 0644 | host              | T6.1 |
| packages `nginx`, `prometheus-node-exporter`              | -              | -    | host              | T6.2 |
| `/opt/temporal-ui/releases/<v>/ui-server`, `/etc/temporal-ui/production.yaml`, `temporal-ui.service` | root:root | 0644/0755 | ui-restart | T6.2 |

nginx and `/health` are installed on nodes that run `frontend` or `ui`; node_exporter on every node.

## Generated files (vps-01, history)

### `/etc/temporal/history.yaml`

```yaml
# enable-template
# Rendered by tops 0.3.0 for lab/tops-e2e/vps-01 role history. tops apply overwrites hand edits.
log: { stdout: true, level: info }
persistence:
  numHistoryShards: 512
  defaultStore: default
  visibilityStore: visibility
  datastores:
    default:
      sql:
        pluginName: postgres12
        databaseName: temporal
        connectAddr: "10.80.0.20:5432"
        connectProtocol: tcp
        user: "temporal"
        password: "{{ env "TOPS_PG_PASSWORD" }}"
        maxConns: 12
        maxIdleConns: 12
        maxConnLifetime: 1h
    visibility:
      sql:
        pluginName: postgres12
        databaseName: temporal_visibility
        connectAddr: "10.80.0.20:5432"
        connectProtocol: tcp
        user: "temporal"
        password: "{{ env "TOPS_PG_PASSWORD" }}"
        maxConns: 3
        maxIdleConns: 3
        maxConnLifetime: 1h
global:
  membership: { maxJoinDuration: 30s, broadcastAddress: "10.80.0.11" }
  pprof: { port: 7934, host: 127.0.0.1 }
  metrics:
    prometheus: { framework: tally, timerType: histogram, listenAddress: "0.0.0.0:9234" }
services:
  frontend: { rpc: { grpcPort: 7233, membershipPort: 6933, httpPort: 7243, bindOnIP: 0.0.0.0 } }
  history:  { rpc: { grpcPort: 7234, membershipPort: 6934, bindOnIP: 0.0.0.0 } }
  matching: { rpc: { grpcPort: 7235, membershipPort: 6935, bindOnIP: 0.0.0.0 } }
  worker:   { rpc: { grpcPort: 7239, membershipPort: 6939, bindOnIP: 0.0.0.0 } }
clusterMetadata:
  enableGlobalNamespace: false
  failoverVersionIncrement: 10
  masterClusterName: tops-e2e
  currentClusterName: tops-e2e
  clusterInformation:
    tops-e2e: { enabled: true, initialFailoverVersion: 1, rpcName: frontend, rpcAddress: 127.0.0.1:7233 }
dcRedirectionPolicy: { policy: noop }
dynamicConfigClient:
  filepath: /etc/temporal/dynamicconfig-history.yaml
  pollInterval: 10s
```

`clusterMetadata` values other than the name are template constants pinned by a golden test. On a node that
runs no frontend, `rpcAddress` is the first frontend node's `<address>:7233`. The worker role's file adds
`publicClient.hostPort` when T2.5 confirms it is needed.

### `/etc/temporal/history.env`, `temporal.env`, limits

```
# history.env - rendered by tops. 0640.
GOMEMLIMIT=1000MiB
```

```
# temporal.env - rendered by tops. 0600 root. Shared by every role on the node.
TOPS_PG_PASSWORD=…
```

```ini
# /etc/systemd/system/temporal@history.service.d/limits.conf
[Service]
MemoryHigh=1200M
MemoryMax=1300M
CPUWeight=150
```

### `/etc/systemd/system/temporal@.service`

```ini
[Unit]
Description=Temporal %i
Wants=network-online.target
After=network-online.target

[Service]
User=temporal
Group=temporal
EnvironmentFile=/etc/temporal/temporal.env
EnvironmentFile=/etc/temporal/%i.env
ExecStart=/opt/temporal/current/temporal-server --config-file /etc/temporal/%i.yaml start --service %i
Restart=on-failure
RestartSec=5
TimeoutStopSec=90
LimitNOFILE=65536
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
ReadWritePaths=/var/lib/temporal

[Install]
WantedBy=multi-user.target
```

`TimeoutStopSec` must exceed the drain durations set in `base/` (`validate` checks it against the merged
`*.shutdownDrainDuration` plus `frontend.shutdownFailHealthCheckDuration`).

### `firewall.rules` (IPv4; IPv6 allows only lo, replies, ICMPv6, 22)

```
*filter
:TEMPORAL_INPUT - [0:0]
-F TEMPORAL_INPUT
-A TEMPORAL_INPUT -i lo -j ACCEPT
-A TEMPORAL_INPUT -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
-A TEMPORAL_INPUT -p icmp -j ACCEPT
-A TEMPORAL_INPUT -p tcp --dport 22 -j ACCEPT
-A TEMPORAL_INPUT -s 10.80.0.11/32 -p tcp -m multiport --dports 6933:6939,7233:7243 -j ACCEPT
-A TEMPORAL_INPUT -s 10.80.0.12/32 -p tcp -m multiport --dports 6933:6939,7233:7243 -j ACCEPT
-A TEMPORAL_INPUT -s 10.80.0.13/32 -p tcp -m multiport --dports 6933:6939,7233:7243 -j ACCEPT
-A TEMPORAL_INPUT -s 10.80.0.1/32 -p tcp -m multiport --dports 7233 -j ACCEPT
-A TEMPORAL_INPUT -s 10.80.0.1/32 -p tcp -m multiport --dports 9233:9239,9100 -j ACCEPT
-A TEMPORAL_INPUT -s 10.80.0.1/32 -p tcp -m multiport --dports 80 -j ACCEPT
-A TEMPORAL_INPUT -j DROP
COMMIT
```

| Port group                | Sources                                               | Plan label |
| ------------------------- | ----------------------------------------------------- | ---------- |
| `6933:6939,7233:7243`     | every node running a Temporal role                    | peers      |
| `7233`                    | `access.clients`, plus `ui` nodes without a frontend  | clients    |
| `9233:9239,9100`          | `access.monitors`                                     | monitors   |
| `80`                      | `access.admins` ∪ `access.healthchecks`               | http       |

The client rule exists only on frontend nodes; pprof and the UI bind to loopback. The chain is jumped to
from `INPUT` by the boot unit; tops never edits any other chain (Docker's included).

## Metrics tops reads

Names verified in v1.31.3 `common/metrics/metric_defs.go`. Each role is scraped on its own port, so the
role is known without a label. A missing metric is "metric missing", never healthy. Fixtures per supported
minor in `internal/audit/testdata/<minor>/`.

| Metric                                                    | Type      | Used by                     |
| --------------------------------------------------------- | --------- | --------------------------- |
| `service_requests`, `service_latency_bucket`              | counter, histogram | audit (frontend; long polls excluded) |
| `service_error_with_type`, `service_errors_resource_exhausted` | counter | audit                  |
| `persistence_requests`, `persistence_latency_bucket`, `persistence_error_with_type` | counter, histogram | audit |
| `persistence_sql_in_use`, `persistence_sql_max_open_conn` | gauge     | audit (pool saturation), sizing measurement |
| `task_latency_queue_bucket`, `dd_shard_io_semaphore_latency_bucket` | histogram | audit (history) |
| `task_dispatch_latency_bucket`, `syncmatch_latency_count`, `asyncmatch_latency_count` | histogram, counter | audit (matching) |
| `approximate_backlog_count`, `approximate_backlog_age_seconds` | gauge | audit (workers)            |
| `memory_heap`, `memory_heapinuse`, `gomaxprocs`, `num_goroutines` | gauge | top, sizing measurement  |
| `numshards_gauge`                                         | gauge     | audit (history)             |
| node_exporter `node_cpu_seconds_total`, `node_memory_MemAvailable_bytes`, `node_load1` | | audit (servers, T6) |

### `tops targets`

```json
[
  {"targets": ["10.80.0.11:9234"], "labels": {"job": "temporal", "env": "lab", "cluster": "tops-e2e", "node": "vps-01", "role": "history"}},
  {"targets": ["10.80.0.11:9100"], "labels": {"job": "node", "env": "lab", "cluster": "tops-e2e", "node": "vps-01"}}
]
```

`--job temporal|node` prints one job; the `node` job exists from T6. Sorted by node, then role. No `shards`
label: PromQL cannot turn a label into a number.

## `--json` outputs

Every document has `kind`, `topsVersion` and, where an environment is loaded, `env` and `cluster`. Exactly
one document on stdout per run, also on failure. Fields are only added within a major.

| Command                     | `kind`         | Top-level fields                                                                                  | Since |
| --------------------------- | -------------- | ------------------------------------------------------------------------------------------------- | ----- |
| any failure                 | `Error`        | `error: {code, what, why?, fix?}`, `nodes?: [{name, error: {code, what}}]`                        | done  |
| `validate`                  | `Validation`   | `ok`, `errors: [Problem]`, `warnings: [Problem]`; `Problem` = `{path, line, message}` (`file` added for settings files) | done; `env`, `line` P0.6/T1.4 |
| `knobs`                     | `Knobs`        | `temporal`, `settings: [{key, type, precedence, roles, apply, default, description, evidence?, layers?: {default, common?, role?, override?, effective}}]` | T1.6 |
| `diff`                      | `EnvDiff`      | `from`, `to`, `promotable: [{path, from, to}]`, `byDesign: [{path, from, to}]`                    | T1.9  |
| `init`                      | `Init`         | `env`, `created: [path]`                                                                          | T1.10 |
| `version`                   | `Version`      | `tops: {version, commit, goVersion}`, `nodes?: [{name, helperSha256, uploaded}]`                  | P0.2; nodes T2.3 |
| `capacity`                  | `Capacity`     | `constants: {temporal, measuredAt}`, `nodes: [{name, memory: Detected, cores: Detected, reserveBytes, roles: [RoleSize]}]`, `headroom: {historyNodes, worstSurvivor, predictedBytes, limitBytes, ok, skipped?}`, `db: {maxConnections, reserved, otherSessions, budget, pools, ok}` | T2.4 |
| `plan`                      | `Plan`         | `changes` (bool), `drift` (bool), `nodes: [NodePlan]`, `cluster: {namespaces: [{name, action}], unknownMembers}`, `refusals: [{code, what, why, fix}]` | T2.6 |
| `bootstrap`, `apply`, `restart`, `rollback`, `upgrade`, `node add`, `node remove`, `ns ensure` | `Rollout` | `command`, `applyId`, `result` (`ok`, `failed`, `refused`, `nothing`), `plan`, `steps: [Step]`, `rollback?` | T3.8 |
| `status`                    | `Status`       | below                                                                                             | T3.9  |
| `ns`                        | `Namespaces`   | `namespaces: [{name, state, retentionMs, managed: spec\|system\|unmanaged, changed}]`             | T3.9  |
| `rollback list`             | `ApplyHistory` | `applies: [{applyId, command, by, startedAt, nodes, result, rollbackable}]`                       | T4.4  |
| `drift`                     | `Drift`        | `drift` (bool), `nodes: [{name, reachable, files: [{path, finding}], units: [{unit, finding}], binary?}]`, `unknownMembers` | T4.5 |
| `promote`                   | `Promotion`    | `from`, `to`, `written: [{file, path, before, after}]`, `refused: [{path, reason}]`               | T4.6  |
| `top`                       | `Top`          | `nodes: [{name, reachable, roles: [{role, top: Top, goHeapBytes, goMaxProcs}]}]`                 | T5.1  |
| `audit`                     | `Audit`        | `windowMs`, `verdict` (`ok`, `warn`, `fail`), `closest?`, `layers: [{name, level, figures: {…}, advice, missing: [metric]}]` | T5.3 |
| `pprof`                     | `Profile`      | `node`, `role`, `kind`, `seconds`, `path`, `bytes`                                               | T5.4  |
| `targets`                   | file-SD array  | no envelope                                                                                       | T5.6  |
| `logs --raw`                | NDJSON         | one `JournalEntry` per line, no envelope                                                          | T5.2  |

`NodePlan`: `{name, reachable, version: {live, spec}, roles: [{role, action: keep|start|stop, restart, hotReload, keys: [{key, apply}]}], files: [{path, role, action: create|update|remove, effect, diff, drift}], firewall: [{group, ports, add, remove}], validated: {ok, release, checks}}`. `diff` is a unified diff with secret values replaced by `***`.

`Step`: `{node, role, action: gate|write|restart|start|stop|hot|stage|schema|namespace|firewall|lock|unlock, startedAt, durationMs, result: ok|failed|skipped, gate?: {conditions: [{name, ok, detail}]}, error?}`.

`Status` (abridged):

```json
{
  "kind": "Status", "topsVersion": "0.3.0", "env": "lab", "cluster": "tops-e2e", "healthy": true,
  "temporal": {
    "clusterName": "tops-e2e", "clusterId": "6b0e…", "serverVersion": "1.31.3",
    "persistenceStore": "postgres12", "visibilityStore": "postgres12",
    "shards": {"spec": 512, "live": 512, "owned": 512},
    "rings": {
      "frontend": {"expected": 3, "members": ["10.80.0.11:7233", "10.80.0.12:7233", "10.80.0.13:7233"]},
      "history":  {"expected": 2, "members": ["10.80.0.11:7234", "10.80.0.12:7234"]},
      "matching": {"expected": 3, "members": ["10.80.0.11:7235", "10.80.0.12:7235", "10.80.0.13:7235"]},
      "worker":   {"expected": 2, "members": ["10.80.0.11:7239", "10.80.0.12:7239"]}
    },
    "unknownMembers": []
  },
  "datastore": {"kind": "postgres", "reachable": true, "via": "vps-01",
                "schemas": {"temporal": "1.19", "temporal_visibility": "1.14"}},
  "nodes": [
    {"name": "vps-01", "address": "10.80.0.11", "reachable": true, "clockSynced": true,
     "roles": [
       {"role": "frontend", "unit": "active", "version": "1.31.3", "serving": true, "inRing": true,
        "memory": {"currentBytes": 188743680, "highBytes": 650117120}},
       {"role": "history", "unit": "active", "version": "1.31.3", "inRing": true, "shards": 263,
        "memory": {"currentBytes": 524288000, "highBytes": 1310720000}}
     ]}
  ]
}
```

Drill assertion: `tops -e lab status --json | jq -e '.temporal.shards.owned == 512 and (.temporal.rings.history.members | length) == 2'`.

## Temporal persistence

### v1: PostgreSQL

tops creates both databases if missing (`temporal-sql-tool create-database`), versions them
(`setup-schema -v 0.0`) and migrates them with the schema embedded in the target release's tool. It never
drops or alters anything else.

| Database              | `--schema-name`             | 1.30.x | 1.31.x |
| --------------------- | --------------------------- | ------ | ------ |
| `temporal`            | `postgresql/v12/temporal`   | 1.18   | 1.19   |
| `temporal_visibility` | `postgresql/v12/visibility` | 1.13   | 1.14   |

| Read by tops                         | Why                                                         |
| ------------------------------------ | ----------------------------------------------------------- |
| `schema_version`                     | status, upgrade, bootstrap                                  |
| `cluster_metadata_info`              | bootstrap refuses a store holding another cluster; identity |
| `pg_database`                        | existence                                                   |
| `max_connections`, `superuser_reserved_connections`, `reserved_connections`, `pg_stat_activity` (other users) | DB budget |
| `pg_stat_activity` (own role: state, wait events, query start) | investigate (no extension needed; `pg_stat_statements` is not assumed) |

Prerequisites (printed by bootstrap preflight): role `persistence.postgres.user` with `CREATEDB` or both
databases pre-created and owned by it; `pg_hba` admits every node; a DB budget that fits (`capacity`).

### v2: Cassandra and Elasticsearch

| Store         | Object                                                            | tops does (one node, target release tool)                                    | Version (1.31.3) |
| ------------- | ----------------------------------------------------------------- | ---------------------------------------------------------------------------- | ---------------- |
| Cassandra     | keyspace (`temporal`), `NetworkTopologyStrategy {<dc>: <rf>}`     | `temporal-cassandra-tool create-keyspace`, `setup-schema -v 0.0`, `update-schema --schema-name cassandra/temporal` | 1.13 |
| Elasticsearch | cluster settings, index template, index (`temporal_visibility_v1`) | `temporal-elasticsearch-tool setup-schema`, `create-index`, `update-schema` (labelled EXPERIMENTAL in 1.31.3) | from the release |

v2 spec fields: `persistence.cassandra` = `hosts` (>= `replicationFactor`), `port` (9042), `keyspace`
(`temporal`, Fixed), `datacenter` (`dc1`, Fixed), `replicationFactor` (3, Fixed), `user`, `tls`;
`persistence.elasticsearch` = `url`, `version` (`v8`), `index` (`temporal_visibility_v1`, Fixed), `user`.
Both together, exclusive with `postgres`.

v2 `status` adds:

```json
"datastore": {"kind": "cassandra", "reachable": true, "via": "vps-01", "schemas": {"temporal": "1.13"},
  "cassandra": {"keyspace": "temporal", "replication": {"dc1": 3}, "quorum": true,
    "hosts": [{"address": "10.80.0.51", "up": true, "version": "5.0.9"},
              {"address": "10.80.0.52", "up": false, "error": "dial tcp 10.80.0.52:9042: i/o timeout"},
              {"address": "10.80.0.53", "up": true, "version": "5.0.9"}]},
  "elasticsearch": {"url": "http://10.80.0.61:9200", "status": "green", "index": "temporal_visibility_v1", "indexExists": true}}
```

One Cassandra host down with quorum kept, or ES `yellow`, is a warning; quorum lost, ES `red` or the index
missing fails the check (exit 5).
