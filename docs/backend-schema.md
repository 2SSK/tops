# tops backend schema

Project: tops (Temporal Ops), `github.com/2SSK/tops`. Status: draft. Date: 2026-10-10.
Sibling docs: [PRD](prd.md) · [TRD](trd.md) · [App flow](app-flow.md) · [Design brief](design-brief.md) ·
[Implementation plan](implementation-plan.md) · [Concepts](concepts.md)

Every data shape tops reads or writes: the spec, secrets, profiles, the helper protocol, node state, the
files it installs, what it renders, the metrics it reads, the `--json` outputs, and the Temporal persistence
it creates. There is no tops state on the workstation besides the run log and the helper cache.

## Where data lives

| Data                         | Location                                                     | Owner            | Lifetime                      |
| ---------------------------- | ------------------------------------------------------------ | ---------------- | ----------------------------- |
| Intent                       | `cluster.yaml`, `dynamicconfig.yaml` in the cluster repo     | Operator (git)   | Forever                       |
| Secrets                      | `secrets.env` next to the spec, 0600, untracked              | Operator         | Forever                       |
| Profiles, templates          | Embedded in the tops binary                                  | tops release     | Per tops version              |
| Run log                      | `${XDG_STATE_HOME:-~/.local/state}/tops/runs/<run-id>.jsonl` | Workstation user | Newest 100                    |
| Helper cache (other arch)    | `${XDG_CACHE_HOME:-~/.cache}/tops/<version>/`                | Workstation user | Until deleted                 |
| Lock, snapshots, history     | `/var/lib/tops/` on each node                                | root             | See "Node state"              |
| Reality                      | systemd, files, iptables on nodes; Temporal APIs; datastore  | -                | Read every run (PRD D2)       |

## Cluster spec (`cluster.yaml`)

### v1 example (temporal-node-scale lab)

```yaml
cluster: tscale-lab
temporal:
  version: 1.31.3
  cli: 1.9.1
  logLevel: info
shards: 512
profile: 2c4g
persistence:
  postgres:
    host: 10.78.0.20
    port: 5432
    user: temporal
    tls: false
secrets: secrets.env
ssh:
  user: deploy
  key: lab/keys/lab_key
  knownHosts: lab/keys/known_hosts
access:
  clients: [10.78.0.1]
  monitors: [10.78.0.1]
  admins: [10.78.0.1]
  healthchecks: []
namespaces:
  - { name: default, retention: 72h }
  - { name: staging, retention: 72h }
dynamicConfig: config/dynamicconfig.yaml
ui:
  node: tscale-01
  version: 2.54.1
nodes:
  - { name: tscale-01, address: 10.78.0.11 }
  - { name: tscale-02, address: 10.78.0.12 }
```

Adopting the bash-built lab: `cluster: active` (the name `config/temporal.yaml` hard-codes; see
"Immutables"). From the first `tops bootstrap` after `make reset`, `cluster: tscale-lab`.

### Fields (v1)

"Fixed" = fixed at creation: `plan`/`apply` refuse when it differs from the live cluster ("build a new
cluster"). "Upgrade" = changed only by `tops upgrade`.

| Field                         | Type                 | Default                 | Validation                                                                                      | Change      |
| ----------------------------- | -------------------- | ----------------------- | ----------------------------------------------------------------------------------------------- | ----------- |
| `cluster`                     | string               | required                | `^[a-z][a-z0-9-]{1,30}$`. Temporal's `currentClusterName`; checked against `DescribeCluster.cluster_name` | Fixed |
| `temporal.version`            | semver string        | required                | A version this tops supports (1.30.x, 1.31.x for tops 1.x; D13, assumes Q11)                   | Upgrade     |
| `temporal.cli`                | semver string        | required                | Release exists (checked at stage)                                                               | apply       |
| `temporal.logLevel`           | enum                 | `info`                  | `debug`, `info`, `warn`, `error`                                                                | apply (restart) |
| `shards`                      | int                  | required                | 1-16384. Headroom: `ceil(shards / (len(nodes) - 1)) <= profile.maxShardsPerHost` when > 1 node (refuse) | Fixed |
| `profile`                     | string               | required                | One of the built-in profiles                                                                    | apply (restart); refused if larger than any host's effective limits |
| `persistence.postgres.host`   | string               | required                | IPv4 address or DNS name                                                                        | apply (restart) |
| `persistence.postgres.port`   | int                  | `5432`                  | 1-65535                                                                                         | apply (restart) |
| `persistence.postgres.user`   | string               | required                | `^[A-Za-z_][A-Za-z0-9_]{0,62}$`                                                                 | apply (restart) |
| `persistence.postgres.tls`    | bool                 | `false`                 | -                                                                                               | apply (restart) |
| `persistence.postgres.adminDatabase` | string        | `postgres`              | Database used for existence checks and `create-database`                                        | apply       |
| persistence kind              | which key is set     | required, exactly one   | v1: `postgres` only                                                                             | Fixed       |
| `secrets`                     | path                 | `secrets.env`           | Exists, mode 0600, owned by the current user, keys valid (below)                                | -           |
| `ssh.user`                    | string               | required                | Has passwordless sudo (preflight)                                                               | -           |
| `ssh.port`                    | int                  | `22`                    | 1-65535                                                                                         | -           |
| `ssh.key`                     | path                 | none (use the agent)    | Readable; not group/world-readable                                                              | -           |
| `ssh.knownHosts`              | path                 | `~/.ssh/known_hosts`    | Readable; must contain every node (checked on connect)                                          | -           |
| `access.clients`              | list of IPv4/CIDR    | `[]`                    | Valid; no duplicates within the list                                                            | apply (firewall) |
| `access.monitors`             | list of IPv4/CIDR    | `[]`                    | as above                                                                                        | apply (firewall) |
| `access.admins`               | list of IPv4/CIDR    | `[]`                    | as above                                                                                        | apply (firewall, nginx) |
| `access.healthchecks`         | list of IPv4/CIDR    | `[]`                    | as above                                                                                        | apply (firewall, nginx) |
| `namespaces[].name`           | string               | -                       | `^[a-zA-Z0-9][a-zA-Z0-9._-]{0,254}$`, unique, not `temporal-system`                             | ns ensure / apply |
| `namespaces[].retention`      | duration             | required                | Go duration with `d` allowed (`72h`, `7d`); >= 1h (Temporal's local minimum, verified)          | ns ensure / apply |
| `dynamicConfig`               | path                 | `dynamicconfig.yaml`    | YAML map; no key also set by the profile; `validate-dynamic-config` clean on the node           | apply (hot-reload) |
| `ui.node`                     | node name            | none (no UI)            | v1: a name in `nodes`. T2 validates `ui` and renders nothing for it (T2 manages the five temporal files only); T3 renders it | apply (ui-restart) |
| `ui.version`                  | semver string        | required if `ui` set    | ui-server release exists (checked at stage)                                                     | apply (ui-restart) |
| `nodes[].name`                | string               | required                | `^[a-z0-9][a-z0-9-]{0,62}$`, unique                                                             | node add/remove |
| `nodes[].address`             | IPv4                 | required                | Unique; used for SSH, `broadcastAddress`, firewall peers, ring identity                         | Fixed per node (remove and add to change) |
| `nodes`                       | list                 | required                | >= 1. Order = rollout order                                                                     | node add/remove |

Spec paths resolve relative to the spec file's directory. Unknown keys are an error with a line number.

### Immutables and how they are checked

| Immutable           | Live value                                   | Read from                                 |
| ------------------- | -------------------------------------------- | ----------------------------------------- |
| `cluster`           | `cluster_name`                               | `AdminService.DescribeCluster`            |
| `shards`            | `history_shard_count`                        | same                                      |
| persistence kind    | `persistence_store`, `visibility_store` (`postgres12`) | same                            |
| `nodes[].address`   | membership identity `ip:port`                | same (`membership_info.rings`)            |
| store identity      | `cluster_name`, `cluster_id` in `cluster_metadata_info` reached through the spec's address | Datastore reader (TRD "Persistence identity") |
| v2: `cassandra.keyspace`, `elasticsearch.index` | Values in the live `temporal.yaml` | Helper `facts`                       |
| v2: `cassandra.datacenter`, `replicationFactor` | Replication map of the live keyspace | `system_schema.keyspaces`          |

With no live cluster (bootstrap), the datastore is checked instead: `bootstrap` refuses if
`cluster_metadata_info` holds a cluster other than this spec's (same name = resume, app-flow journey 1).

### v2 (T6): roles

v2.0.0 changes the spec without a compatibility layer (D7, D12). temporal-node-scale's `cluster.yaml` is
migrated in the same release.

| Change                         | v2 shape                                                                                                   |
| ------------------------------ | ---------------------------------------------------------------------------------------------------------- |
| `nodes[].roles`                | Required list, no default: subset of `frontend`, `history`, `matching`, `worker`. Empty only for the `ui.node` host: then no Temporal, no node_exporter, no firewall chain, no peers rule, no targets |
| `nodes[].profile`              | Required when `roles` is non-empty, forbidden when empty; the top-level `profile` is removed               |
| Built-in profiles              | Adds size-named lab profiles (table under "Profiles")                                                      |
| `persistence.cassandra`        | `hosts` (list, >= `replicationFactor`), `port` (9042), `keyspace` (`temporal`, Fixed), `datacenter` (`dc1`, Fixed), `replicationFactor` (3, Fixed), `user` (optional), `tls` (false) |
| `persistence.elasticsearch`    | `url` (`http://host:9200`), `version` (`v8`), `index` (`temporal_visibility_v1`, Fixed), `user` (optional) |
| Order                          | `nodes` order = rollout order for `apply`, `restart`, `upgrade`; `bootstrap` uses role stages (TRD "Ring gate") |
| persistence kind               | `postgres` (default + visibility) or `cassandra` + `elasticsearch` (both required together); Fixed          |
| `ui.node`                      | May name a node with `roles: []`; then `ui.temporalAddress` (host:port of a frontend or a DNS name) is required |
| Headroom rule                  | Over history nodes, each with its own profile limit                                                        |

```yaml
# v2 example (temporal-role-scale lab, abridged)
cluster: trscale-lab
temporal: { version: 1.31.3, cli: 1.9.1 }
shards: 512
persistence:
  cassandra: { hosts: [10.80.0.51, 10.80.0.52, 10.80.0.53], datacenter: dc1, replicationFactor: 3 }
  elasticsearch: { url: http://10.80.0.61:9200, version: v8 }
ui: { node: trscale-mon, version: 2.54.1, temporalAddress: "frontend.trscale:7233" }
nodes:
  - { name: trscale-hist-1,   address: 10.80.0.21, roles: [history],  profile: lab-768m }
  - { name: trscale-match-1,  address: 10.80.0.31, roles: [matching], profile: lab-384m }
  - { name: trscale-fe-1,     address: 10.80.0.11, roles: [frontend], profile: lab-384m }
  - { name: trscale-worker-1, address: 10.80.0.41, roles: [worker],   profile: lab-256m }
  - { name: trscale-mon,      address: 10.80.0.70, roles: [] }
  # temporal-node-scale after migration:
  # - { name: tscale-01, address: 10.78.0.11, roles: [frontend, history, matching, worker], profile: 2c4g }
```

## Secrets file (`secrets.env`)

```
# secrets.env - tops secrets for tscale-lab. Mode 0600. Never commit.
TOPS_PG_PASSWORD=tscaletemporal
```

| Key                        | Required when                      | Validation                                                                                       |
| -------------------------- | ---------------------------------- | ------------------------------------------------------------------------------------------------ |
| `TOPS_PG_PASSWORD`         | `persistence.postgres`             | `^[A-Za-z0-9._~-]+$` (kept from `lib.sh`: the value goes into a YAML string and a systemd env file) |
| `TOPS_CASSANDRA_PASSWORD`  | v2, `persistence.cassandra.user`   | same charset                                                                                     |
| `TOPS_ES_PASSWORD`         | v2, `persistence.elasticsearch.user` | same charset                                                                                   |

Format: `KEY=VALUE`, `#` comments, blank lines; no quotes, no `export`, no `$` expansion. An unknown key
is an error. File mode must be 0600 and owner the current user.

## Profiles (embedded)

`internal/profile/profiles/<name>.yaml`, moved unchanged from temporal-node-scale `config/profiles/`.

| Field                  | Type      | Used for                                                                 |
| ---------------------- | --------- | ------------------------------------------------------------------------ |
| `cores`                | int       | Host fit: refuse when effective cores < `cores`; warn when > `cores`     |
| `memoryMiB`            | int       | Host fit: refuse when effective memory < 0.85 × `memoryMiB`, or `memoryMax` >= effective memory (the kernel or container OOM would act before systemd); warn above 1.25× |
| `maxShardsPerHost`     | int       | Headroom with one node down                                              |
| `goMaxProcs`           | int       | `GOMAXPROCS` in `temporal.env`                                           |
| `goMemLimit`           | string    | `GOMEMLIMIT` in `temporal.env`                                           |
| `memoryHigh`, `memoryMax` | string | `profile.conf` drop-in                                                   |
| `dbMaxConns`, `dbVisibilityMaxConns` | int | SQL `maxConns`/`maxIdleConns` per datastore; v2 Cassandra `maxConns` (connections per Cassandra host) |
| `dynamicConfig`        | map       | Merged into `dynamicconfig.yaml`                                         |

| Profile | cores | memoryMiB | maxShardsPerHost | GOMEMLIMIT | MemoryHigh / Max | DB conns (default / visibility) |
| ------- | ----- | --------- | ---------------- | ---------- | ---------------- | ------------------------------- |
| 2c4g    | 2     | 4096      | 1024             | 1800MiB    | 2700M / 3200M    | 20 / 5                          |
| 2c8g    | 2     | 8192      | 2048             | 4000MiB    | 6000M / 6800M    | 30 / 5                          |
| 4c16g   | 4     | 16384     | 4096             | 8600MiB    | 13000M / 14000M  | 50 / 10                         |
| 8c32g   | 8     | 32768     | 8192             | 18000MiB   | 27000M / 29000M  | 80 / 10                         |

T6 adds lab profiles named by the container memory they fit (starting values from temporal-role-scale's TRD;
T6 measures them on its lab). Size names, not role names: a frontend-only restart-class change (role-scale
R-D10) is a move between two sizes.

| Profile    | cores | memoryMiB | maxShardsPerHost | GOMEMLIMIT | MemoryHigh / Max | Cassandra conns per host | Profile dynamic config |
| ---------- | ----- | --------- | ---------------- | ---------- | ---------------- | ------------------------ | ---------------------- |
| lab-256m   | 2     | 256       | 512              | 150MiB     | 180M / 210M      | 2                        | -                      |
| lab-384m   | 2     | 384       | 512              | 240MiB     | 290M / 330M      | 2                        | -                      |
| lab-768m   | 2     | 768       | 512              | 520MiB     | 600M / 680M      | 2                        | `history.hostLevelCacheMaxSizeBytes` 64 MiB, `history.eventsHostLevelCacheMaxSizeBytes` 32 MiB |

## Node helper protocol

### Envelope

```go
package wire

// Request is written as one JSON object to the helper's stdin.
type Request struct {
	RunID   string          `json:"runId"`             // workstation run, e.g. "r-20261010T153012Z-9c1e"
	ApplyID string          `json:"applyId,omitempty"` // set for mutating ops
	By      string          `json:"by"`                // "ssk@thinkpad" (user@workstation host)
	Args    json.RawMessage `json:"args"`              // op-specific, below
}

// Response is the last JSON object on stdout.
type Response struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *Error          `json:"error,omitempty"`
}

type Error struct {
	Code    string `json:"code"`           // stable, e.g. "file.write", "systemd.job_failed", "release.checksum"
	Message string `json:"message"`        // what happened
	Detail  string `json:"detail,omitempty"` // e.g. last lines of a tool's stderr (secrets never included)
}

// Event is one NDJSON line before the final Response for --stream ops.
type Event struct {
	Kind string          `json:"kind"` // "journal", "progress"
	Data json.RawMessage `json:"data"`
}
```

Command line: `sudo -n /usr/local/lib/tops/<v>/tops node <op> [--stream]`. Exit 0 = `ok: true`, 1 = `ok: false`,
2 = undecodable request.

### Ops

| Op         | Args                                                                                         | Result                                                              | Stream |
| ---------- | -------------------------------------------------------------------------------------------- | ------------------------------------------------------------------- | ------ |
| `hello`    | `{}`                                                                                          | `Hello`                                                             | no     |
| `facts`    | `{ "want": {"<path>": "<sha256>"}, "units": ["temporal.service", …] }`                       | `Facts` (content only for paths whose hash differs from `want`)    | no     |
| `validate` | `{ "release": "1.31.3", "files": [File], "env": {"K": "V"} }`                                 | `{ "checks": [ {name, ok, detail} ] }`                              | no     |
| `stage`    | `Stage`                                                                                       | `{ "changed": ["release 1.31.3", "package nginx", …] }`            | progress |
| `lock`     | `{ "action": "acquire"|"renew"|"release", "lease": Lease }`                                  | `{ "held": bool, "lease": Lease, "previous": Lease? }`             | no     |
| `write`    | `{ "files": [File], "remove": ["<path>"], "symlinks": [ {path, target} ], "effects": ["daemon-reload","firewall","nginx","journald","rsyslog"] }` | `WriteResult` | no |
| `restore`  | `{ "snapshot": "<apply-id>" }`                                                                | `WriteResult` (its own snapshot under the new apply id)             | no     |
| `service`  | `{ "unit": "temporal.service", "action": "start"|"stop"|"restart"|"enable-now"|"disable-now" }` | `UnitState` after the job completes                               | no     |
| `schema`   | `Schema`                                                                                      | `{ "before": "1.18", "after": "1.19", "created": false }` per database | progress |
| `top`      | `{ "sampleMs": 1000 }`                                                                        | `Top`                                                               | no     |
| `journal`  | `{ "unit": "temporal.service", "since": RFC3339?, "until": RFC3339?, "lines": 50, "grep": ["…"], "follow": bool }` | `{ "count": n }`                          | `journal` events: `JournalEntry` |
| `history`  | `{ "action": "list", "limit": 20 }`                                                           | `{ "records": [History] }`                                          | no     |

### Types

```go
type Hello struct {
	HelperVersion string `json:"helperVersion"`
	SHA256        string `json:"sha256"`        // of /proc/self/exe
	Hostname      string `json:"hostname"`
	MachineID     string `json:"machineId"`     // /etc/machine-id: detects two spec entries on one host
	OS            string `json:"os"`            // "ubuntu 24.04"
	Arch          string `json:"arch"`          // "amd64" | "arm64"
	Cores         int    `json:"cores"`         // nproc
	MemTotalBytes int64  `json:"memTotalBytes"` // /proc/meminfo
	EffCores      float64 `json:"effCores"`     // min(nproc, cpuset and cpu.max of PID 1's cgroup): profile fit
	EffMemBytes   int64  `json:"effMemBytes"`   // min(MemTotal, memory.max of PID 1's cgroup): profile fit
	ClockSynced   bool   `json:"clockSynced"`   // adjtimex STA_UNSYNC clear
	Listeners     []Listener `json:"listeners"` // TCP listeners on ports tops needs, with owning unit/process
}

type Listener struct { Port int `json:"port"`; Process string `json:"process"`; Unit string `json:"unit,omitempty"` }

type File struct {
	Path    string `json:"path"`
	Content string `json:"content"`           // text; all managed files are text
	Mode    string `json:"mode"`              // "0640"
	Owner   string `json:"owner"`             // "root"
	Group   string `json:"group"`             // "temporal"
}

type FileState struct {
	Exists  bool   `json:"exists"`
	SHA256  string `json:"sha256,omitempty"`
	Mode    string `json:"mode,omitempty"`
	Owner   string `json:"owner,omitempty"`
	Group   string `json:"group,omitempty"`
	Content *string `json:"content,omitempty"` // only when sha256 != want
}

type Facts struct {
	Files     map[string]FileState `json:"files"`
	Unknown   []string             `json:"unknown"`   // files in tops-owned dirs that tops did not render (T3+)
	Current   string               `json:"current"`   // target of /opt/temporal/current, "" if absent
	Releases  []Release            `json:"releases"`
	Units     map[string]UnitState `json:"units"`
	Firewall  Firewall             `json:"firewall"`
	Applied   *Applied             `json:"applied"`   // /var/lib/tops/applied.json, nil = never managed by tops
	Lock      *Lease               `json:"lock"`
	Removed   *History             `json:"removed,omitempty"` // last `node remove` record, if the unit is still disabled by it
}

type Release struct { Kind string `json:"kind"`; Version string `json:"version"`; Verified bool `json:"verified"` } // kind: server|cli|ui

type UnitState struct {
	Active    string    `json:"active"`    // ActiveState: active|inactive|failed|activating|deactivating
	Enabled   bool      `json:"enabled"`
	MainPID   int       `json:"mainPid"`
	Exe       string    `json:"exe"`       // readlink /proc/<pid>/exe
	Since     time.Time `json:"since"`
	NRestarts int       `json:"nRestarts"`
}

type Firewall struct {
	Jump  bool   `json:"jump"`   // INPUT has -j TEMPORAL_INPUT
	Rules []Rule `json:"rules"`  // live per-source ACCEPT rules in TEMPORAL_INPUT
	Jump6 bool   `json:"jump6"`
}

type Rule struct { Ports string `json:"ports"`; Source string `json:"source"` } // "7233", "10.78.0.1/32"

type Stage struct {
	User     bool        `json:"user"`      // ensure system user temporal, dirs
	Packages []string    `json:"packages"`  // "nginx", "prometheus-node-exporter"
	Releases []Download  `json:"releases"`
}

type Download struct {
	Kind    string   `json:"kind"`     // server|cli|ui
	Version string   `json:"version"`
	URL     string   `json:"url"`      // https://github.com/temporalio/temporal/releases/download/v1.31.3/temporal_1.31.3_linux_amd64.tar.gz
	SHA256  string   `json:"sha256"`   // from checksums.txt, fetched by the workstation
	Extract []string `json:"extract"`  // ["temporal-server","temporal-sql-tool","tdbg"]
}

type WriteResult struct {
	Changed  []string `json:"changed"`  // paths actually changed
	Snapshot string   `json:"snapshot"` // apply id, "" when nothing changed
}

type Schema struct {
	Tool      string `json:"tool"`      // "sql" (v1); "cassandra", "elasticsearch" (T6)
	Release   string `json:"release"`   // binaries from /opt/temporal/releases/<release>
	Action    string `json:"action"`    // "ensure" = create if missing, setup -v 0.0 if unversioned, update to the release's version
	Endpoint  string `json:"endpoint"`
	Port      int    `json:"port"`
	User      string `json:"user"`
	Password  secret `json:"password"`  // passed to the tool via its env var only
	TLS       bool   `json:"tls"`
	Targets   []SchemaTarget `json:"targets"` // [{database: temporal, schemaName: postgresql/v12/temporal}, {database: temporal_visibility, schemaName: postgresql/v12/visibility}]
}

type Top struct {
	CPUPercentOfCore float64 `json:"cpuPercentOfCore"` // cgroup cpu.stat usage_usec delta / sample
	Cores            int     `json:"cores"`
	CPUPressure10    float64 `json:"cpuPressureAvg10"`
	Throttled        int64   `json:"throttled"`
	MemCurrent       int64   `json:"memCurrentBytes"`
	MemPeak          int64   `json:"memPeakBytes"`
	MemHigh          int64   `json:"memHighBytes"`   // "max" -> MemTotal
	MemMax           int64   `json:"memMaxBytes"`
	MemPressure10    float64 `json:"memPressureAvg10"`
	Events           map[string]int64 `json:"events"` // memory.events: high, max, oom, oom_kill
	KernelOOMKills   int     `json:"kernelOomKills"`  // this boot, for temporal-server
	NRestarts        int     `json:"nRestarts"`
	Since            time.Time `json:"since"`
	GoMemLimit       string  `json:"goMemLimit"`
	Load1            float64 `json:"load1"`
	MemAvailable     int64   `json:"memAvailableBytes"`
	MemTotal         int64   `json:"memTotalBytes"`
}

type JournalEntry struct {
	TS     time.Time         `json:"ts"`      // Temporal's "ts", else __REALTIME_TIMESTAMP
	Level  string            `json:"level"`   // debug|info|warn|error|dpanic|panic|fatal; "" for non-JSON lines
	Msg    string            `json:"msg"`
	Fields map[string]string `json:"fields"`  // remaining zap fields, in source order kept by Keys
	Keys   []string          `json:"keys"`
	Raw    string            `json:"raw"`     // the original MESSAGE
}
```

Example: `facts` request and response (abridged).

```json
{"runId":"r-20261010T153012Z-9c1e","by":"ssk@thinkpad","args":{
  "want":{"/etc/temporal/temporal.yaml":"8f3c…","/etc/temporal/dynamicconfig.yaml":"11aa…"},
  "units":["temporal.service","temporal-firewall.service"]}}
```

```json
{"ok":true,"result":{
  "files":{
    "/etc/temporal/temporal.yaml":{"exists":true,"sha256":"8f3c…","mode":"0640","owner":"root","group":"temporal"},
    "/etc/temporal/dynamicconfig.yaml":{"exists":true,"sha256":"02be…","mode":"0640","owner":"root","group":"temporal",
      "content":"limit.maxIDLength:\n  - value: 255\n…"}},
  "current":"releases/1.31.3",
  "releases":[{"kind":"server","version":"1.31.3","verified":true},{"kind":"cli","version":"1.9.1","verified":true}],
  "units":{"temporal.service":{"active":"active","enabled":true,"mainPid":4121,
    "exe":"/opt/temporal/releases/1.31.3/temporal-server","since":"2026-10-10T14:02:11Z","nRestarts":0}},
  "firewall":{"jump":true,"jump6":true,"rules":[{"ports":"7233","source":"10.78.0.1/32"}]},
  "applied":{"applyId":"20261010T140155Z-3b7d","files":{"/etc/temporal/temporal.yaml":{"sha256":"8f3c…"}}},
  "lock":null}}
```

## Node state (`/var/lib/tops`)

```
/var/lib/tops/                       0700 root:root
├── lock                             0600  Lease JSON (absent = free)
├── applied.json                     0600  Applied: sha256 of every file as tops last wrote it
├── history/
│   └── 20261010T153012Z-7f3a.json   0600  History, newest 100 kept
└── snapshots/
    └── 20261010T153012Z-7f3a/       0700  newest 10 kept
        ├── manifest.json                  entries before the change
        └── files/<sha256>                 content-addressed copies (temporal.env copies stay 0600)
```

Apply id: `YYYYMMDDTHHMMSSZ-xxxx` (UTC start time of the run, 4 hex random), shared by every node in one
run so `rollback <id>` names one cluster-wide change. Run id: `r-` + the same pattern; one per invocation.

```go
type Lease struct {
	Cluster    string    `json:"cluster"`
	RunID      string    `json:"runId"`
	ApplyID    string    `json:"applyId"`
	Command    string    `json:"command"`    // "apply", "upgrade 1.31.3", "restart --all", …
	By         string    `json:"by"`         // "ssk@thinkpad"
	PID        int       `json:"pid"`        // workstation pid
	AcquiredAt time.Time `json:"acquiredAt"` // node clock
	RenewedAt  time.Time `json:"renewedAt"`  // node clock
	TTLSeconds int       `json:"ttlSeconds"` // 120
}

type Applied struct {
	ApplyID string                   `json:"applyId"`
	Files   map[string]AppliedFile   `json:"files"` // path -> {sha256, mode, owner, group}
	Current string                   `json:"current"`
}

type SnapshotManifest struct {
	ApplyID string          `json:"applyId"`
	TakenAt time.Time       `json:"takenAt"`
	Command string          `json:"command"`
	Entries []SnapshotEntry `json:"entries"`
}

type SnapshotEntry struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`             // "file" | "symlink" | "absent"
	SHA256 string `json:"sha256,omitempty"` // file content under files/
	Target string `json:"target,omitempty"` // symlink target, e.g. "releases/1.30.7"
	Mode, Owner, Group string
}

type History struct {
	ApplyID      string    `json:"applyId"`
	RunID        string    `json:"runId"`
	Command      string    `json:"command"`
	By           string    `json:"by"`
	TopsVersion  string    `json:"topsVersion"`
	SpecSHA256   string    `json:"specSha256"`
	Node         string    `json:"node"`
	StartedAt    time.Time `json:"startedAt"`
	FinishedAt   time.Time `json:"finishedAt"`
	Changes      []Change  `json:"changes"`   // {path, action: create|update|remove|symlink, effect, before, after (sha256 only)}
	Restarted    bool      `json:"restarted"`
	Result       string    `json:"result"`    // "ok" | "failed" | "interrupted"
	Error        string    `json:"error,omitempty"`
	RollbackOf   string    `json:"rollbackOf,omitempty"` // apply id this run restored
	Snapshot     bool      `json:"snapshot"`  // a snapshot exists for this apply id on this node
}
```

`rollback list` merges history records from all nodes by apply id: one row per apply id, with the nodes it
changed and whether every changed node still has its snapshot (rollbackable).

## Files tops installs on a node

`E` = effect class (TRD "Plan and apply"). `T` = milestone that starts managing the path. tops owns
`/etc/temporal`, `/etc/temporal-ui`, `/usr/local/lib/tops`, `/var/lib/tops` entirely from T3 (assumes Q9):
files there that tops did not render are shown as removals in `plan`. v2: a `roles: []` host gets only the
UI rows, nginx and the helper.

| Path                                                        | Owner:group      | Mode | E            | T  | Content                                                       |
| ----------------------------------------------------------- | ---------------- | ---- | ------------ | -- | ------------------------------------------------------------- |
| `/usr/local/lib/tops/<v>/tops`                              | root:root        | 0755 | -            | T1 | Helper; newest 3 versions kept                                |
| `/var/lib/tops/…`                                           | root:root        | 0700 | -            | T2 | Lock, snapshots, history (above)                              |
| `/run/tops/<run-id>/`                                       | root:root        | 0700 | -            | T2 | Validation scratch, removed after the op                      |
| `/etc/temporal/`                                            | root:temporal    | 0750 | -            | T2 |                                                               |
| `/etc/temporal/temporal.yaml`                               | root:temporal    | 0640 | restart      | T2 | Server config (below); no secrets                             |
| `/etc/temporal/temporal.env`                                | root:root        | 0600 | restart      | T2 | Secrets and Go runtime env (below)                            |
| `/etc/temporal/dynamicconfig.yaml`                          | root:temporal    | 0640 | hot-reload   | T2 | Cluster file + profile block                                  |
| `/etc/systemd/system/temporal.service`                      | root:root        | 0644 | restart      | T2 | Unit (kept from `config/temporal.service`)                    |
| `/etc/systemd/system/temporal.service.d/profile.conf`       | root:root        | 0644 | restart      | T2 | `MemoryHigh`, `MemoryMax`                                     |
| `/etc/temporal/firewall.rules`, `firewall6.rules`           | root:root        | 0644 | firewall     | T3 | `TEMPORAL_INPUT` chains, iptables-restore format              |
| `/etc/systemd/system/temporal-firewall.service`             | root:root        | 0644 | firewall     | T3 | Boot unit (kept from `config/temporal-firewall.service`)      |
| `/etc/nginx/conf.d/temporal.conf`                           | root:root        | 0644 | nginx        | T3 | `/health` on every node (v2: frontend hosts only); `/` -> UI only on `ui.node` |
| `/etc/nginx/temporal-admin-allow.conf`, `temporal-healthcheck-allow.conf` | root:root | 0644 | nginx | T3 | `allow <ip>;` lines                                   |
| `/etc/nginx/sites-enabled/default`                          | -                | -    | nginx        | T3 | Removed (it shadows `server_name _`)                           |
| `/etc/systemd/journald.conf.d/temporal.conf`                | root:root        | 0644 | host         | T3 | `SystemMaxUse=1G`, `SystemMaxFileSize=100M`, `MaxRetentionSec=2week` |
| `/etc/rsyslog.d/30-temporal.conf`                           | root:root        | 0644 | host         | T3 | `:programname, isequal, "temporal" stop` (only if rsyslog is installed) |
| `/opt/temporal/releases/<v>/`                               | root:root        | 0755 | -            | T3 | `temporal-server`, `temporal-sql-tool`, `tdbg`, `.tops.json` (sha256 of tarball and files); T6 adds the cassandra and elasticsearch tools |
| `/opt/temporal/current` -> `releases/<v>`                   | root:root        | link | version      | T3 | Switched only by `upgrade`/`rollback`                         |
| `/opt/temporal/cli/<v>/temporal`                            | root:root        | 0755 | -            | T3 | Temporal CLI for operators on the node                        |
| `/usr/local/bin/{temporal,tdbg,temporal-sql-tool}`          | root:root        | link | -            | T3 | Links into `cli/<v>` and `current`                            |
| `/opt/temporal-ui/releases/<v>/ui-server`                   | root:root        | 0755 | ui-restart   | T3 | `ui.node` only                                                |
| `/etc/temporal-ui/production.yaml`                          | root:root        | 0644 | ui-restart   | T3 | `temporalGrpcAddress: 127.0.0.1:7233`, `host: 127.0.0.1`, `port: 8080`, `enableUi: true` |
| `/etc/systemd/system/temporal-ui.service`                   | root:root        | 0644 | ui-restart   | T3 | `ExecStart=/opt/temporal-ui/releases/<v>/ui-server --root /etc/temporal-ui --config . --env production start`, `DynamicUser=yes` |
| user `temporal`, `/var/lib/temporal` (0750 temporal:temporal) | -              | -    | -            | T3 | System user, no shell                                         |
| packages `nginx`, `prometheus-node-exporter`                | -                | -    | host         | T3 | apt; node_exporter enabled on :9100. v2: nginx on frontend and UI hosts, node_exporter on hosts with roles |

Left alone (not managed): other iptables chains, `/etc/ssh`, other users, other nginx sites, anything else.
Leftovers of the bash scripts that tops does not own are listed in [app-flow.md](app-flow.md#2-adopt-the-cluster-the-bash-scripts-built).

## Generated files

### `temporal.yaml` (v1, tscale-01)

Structure kept from temporal-node-scale `config/temporal.yaml`; values rendered literally; pprof added on
loopback (PRD scope boundary).

```yaml
# enable-template
# Rendered by tops 1.0.0 for tscale-lab/tscale-01. tops apply overwrites hand edits.
log:
  stdout: true
  level: info
persistence:
  numHistoryShards: 512
  defaultStore: default
  visibilityStore: visibility
  datastores:
    default:
      sql:
        pluginName: postgres12
        databaseName: temporal
        connectAddr: "10.78.0.20:5432"
        connectProtocol: tcp
        user: "temporal"
        password: "{{ env "TOPS_PG_PASSWORD" }}"
        maxConns: 20
        maxIdleConns: 20
        maxConnLifetime: 1h
        tls: { enabled: false, enableHostVerification: true, serverName: "10.78.0.20" }
    visibility:
      sql:
        pluginName: postgres12
        databaseName: temporal_visibility
        connectAddr: "10.78.0.20:5432"
        connectProtocol: tcp
        user: "temporal"
        password: "{{ env "TOPS_PG_PASSWORD" }}"
        maxConns: 5
        maxIdleConns: 5
        maxConnLifetime: 1h
        tls: { enabled: false, enableHostVerification: true, serverName: "10.78.0.20" }
global:
  membership:
    maxJoinDuration: 30s
    broadcastAddress: "10.78.0.11"
  pprof:
    port: 7936
    host: 127.0.0.1
  metrics:
    prometheus:
      framework: tally
      timerType: histogram
      listenAddress: 0.0.0.0:9091
services:
  frontend: { rpc: { grpcPort: 7233, membershipPort: 6933, httpPort: 7243, bindOnIP: 0.0.0.0 } }
  history:  { rpc: { grpcPort: 7234, membershipPort: 6934, bindOnIP: 0.0.0.0 } }
  matching: { rpc: { grpcPort: 7235, membershipPort: 6935, bindOnIP: 0.0.0.0 } }
  worker:   { rpc: { grpcPort: 7239, membershipPort: 6939, bindOnIP: 0.0.0.0 } }
clusterMetadata:
  enableGlobalNamespace: false
  failoverVersionIncrement: 10
  masterClusterName: tscale-lab
  currentClusterName: tscale-lab
  clusterInformation:
    tscale-lab:
      enabled: true
      initialFailoverVersion: 1
      rpcName: frontend
      rpcAddress: 127.0.0.1:7233
dcRedirectionPolicy:
  policy: noop
dynamicConfigClient:
  filepath: /etc/temporal/dynamicconfig.yaml
  pollInterval: 10s
```

T6: `persistence.datastores.default.cassandra` (`hosts`, `keyspace`, `datacenter`, `user`,
`password: "{{ env "TOPS_CASSANDRA_PASSWORD" }}"`, `maxConns` from the profile) and `datastores.visibility.elasticsearch`
(`version: v8`, `url` with sprig `urlParse` as measured for role-scale, `indices.visibility`).

### `temporal.env`

```
# Rendered by tops 1.0.0. 0600 root. Read by systemd for temporal.service.
TOPS_PG_PASSWORD=tscaletemporal
GOMAXPROCS=2
GOMEMLIMIT=1800MiB
```

`plan` shows this file with secret values masked (`~ TOPS_PG_PASSWORD=…`); `GOMAXPROCS` and `GOMEMLIMIT` are shown.

### `temporal.service.d/profile.conf`

```ini
[Service]
MemoryHigh=2700M
MemoryMax=3200M
```

### `firewall.rules` (IPv4; IPv6 allows only lo, replies, ICMPv6, 22)

```
*filter
:TEMPORAL_INPUT - [0:0]
-F TEMPORAL_INPUT
-A TEMPORAL_INPUT -i lo -j ACCEPT
-A TEMPORAL_INPUT -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
-A TEMPORAL_INPUT -p icmp -j ACCEPT
-A TEMPORAL_INPUT -p tcp --dport 22 -j ACCEPT
-A TEMPORAL_INPUT -s 10.78.0.11/32 -p tcp -m multiport --dports 6933:6939,7233:7243 -j ACCEPT
-A TEMPORAL_INPUT -s 10.78.0.12/32 -p tcp -m multiport --dports 6933:6939,7233:7243 -j ACCEPT
-A TEMPORAL_INPUT -s 10.78.0.1/32 -p tcp -m multiport --dports 7233 -j ACCEPT
-A TEMPORAL_INPUT -s 10.78.0.1/32 -p tcp -m multiport --dports 9091,9100 -j ACCEPT
-A TEMPORAL_INPUT -s 10.78.0.1/32 -p tcp -m multiport --dports 80 -j ACCEPT
-A TEMPORAL_INPUT -j DROP
COMMIT
```

| Port group                | Sources                         | Plan label   |
| ------------------------- | ------------------------------- | ------------ |
| `6933:6939,7233:7243`     | every node                      | peers        |
| `7233`                    | `access.clients`                | clients      |
| `9091,9100`               | `access.monitors`               | monitors     |
| `80`                      | `access.admins` ∪ `access.healthchecks` | http |

The workstation needs no rule (D4). pprof (7936) and the UI (8080) bind to loopback.

### `dynamicconfig.yaml`

The cluster repo's file followed by the profile's `dynamicConfig` block, keys in source order. A key in both
is a `validate` error.

## Metrics tops reads

Names verified in Temporal 1.31.3 `common/metrics/metric_defs.go`. Prometheus exposes timers as
`<name>_bucket|_sum|_count` (`timerType: histogram`). A metric missing from a scrape is reported as
"metric missing", never as healthy (PRD R5). Fixtures per supported minor live in `internal/audit/testdata/<minor>/`.

| Metric (`:9091`)                                       | Type      | Labels used                              | Used by             |
| ------------------------------------------------------ | --------- | ---------------------------------------- | ------------------- |
| `service_requests`                                     | counter   | `service_name`, `operation`              | audit               |
| `service_latency_bucket`                               | histogram | `service_name`, `operation`, `le`        | audit (p50/p99; long polls excluded) |
| `service_error_with_type`                              | counter   | `service_name`, `error_type`             | audit (server-side types only) |
| `service_errors_resource_exhausted`                    | counter   | `service_name`                           | audit               |
| `persistence_requests`                                 | counter   | `service_name`, `operation`              | audit               |
| `persistence_latency_bucket`                           | histogram | `operation`, `le`                        | audit               |
| `persistence_error_with_type`                          | counter   | `error_type` (`ConditionFailed`, `NotFound` ignored) | audit   |
| `dd_shard_io_semaphore_latency_bucket`                 | histogram | `le`                                     | audit               |
| `history_workflow_execution_cache_lock_hold_duration_bucket` | histogram | `le`                              | audit               |
| `task_latency_queue_bucket`                            | histogram | `le`                                     | audit               |
| `task_dispatch_latency_bucket`                         | histogram | `le`                                     | audit               |
| `syncmatch_latency_count`, `asyncmatch_latency_count`  | counter   | -                                        | audit (sync match %) |
| `approximate_backlog_count`, `approximate_backlog_age_seconds` | gauge | `taskqueue`                         | audit               |
| `numshards_gauge`                                      | gauge     | -                                        | audit (shards row; `status` uses the admin API) |
| `memory_heap`, `num_goroutines`                        | gauge     | `service_name` (any one; same process)   | top                 |
| `visibility_persistence_requests`, `visibility_persistence_latency_bucket` | counter, histogram | `operation` | audit (T6 ES row) |

| Metric (`:9100`, node_exporter)                         | Labels  | Used by |
| ------------------------------------------------------- | ------- | ------- |
| `node_cpu_seconds_total`                                | `mode`  | audit   |
| `node_memory_MemAvailable_bytes`, `node_memory_MemTotal_bytes` | - | audit |
| `node_load1`                                            | -       | audit   |

### Labels tops emits (`tops targets`)

```json
[
  {"targets": ["10.78.0.11:9091"], "labels": {"job": "temporal", "cluster": "tscale-lab", "node": "tscale-01"}},
  {"targets": ["10.78.0.11:9100"], "labels": {"job": "node", "cluster": "tscale-lab", "node": "tscale-01"}}
]
```

`--job temporal|node` prints one job's groups (one file per scrape job). T6 adds `"role": "history"`
(comma-joined when a node runs several); a `roles: []` host emits no group.

No `shards` or `profile` label: PromQL cannot turn a label value into a number, so a label would not
remove the constants from recording rules. Cluster repos keep `shards` and profile cores as constant rules
and check them against `cluster.yaml` in their lint.

## `--json` outputs

Every JSON document on stdout has `kind` and `topsVersion`. Fields are only added within a major version.
Times are RFC 3339 UTC; sizes in bytes; durations in milliseconds (`…Ms`).

| Command               | `kind`         | Top-level fields                                                                                         |
| --------------------- | -------------- | -------------------------------------------------------------------------------------------------------- |
| any failure           | `Error`        | `error: {code, what, why, fix}`, `nodes: [{name, error}]` when per node                                  |
| `validate`            | `Validation`   | `ok`, `errors: [{path, message}]`, `warnings`                                                            |
| `status`              | `Status`       | below                                                                                                    |
| `plan`                | `Plan`         | `changes: bool`, `cluster: {namespaces: [...], unknownMembers: [...]}`, `nodes: [NodePlan]`, `refusals: [...]` |
| `apply`, `restart`, `upgrade`, `rollback`, `node add/remove`, `bootstrap` | `Rollout` | `applyId`, `result: ok|failed|refused`, `plan`, `steps: [{node, action, startedAt, durationMs, result, gate: {serving, rings, shardsOwned, shardsTotal}}]`, `rollback: "tops rollback <id>"` |
| `top`                 | `Top`          | `nodes: [{name, top: Top, goHeapBytes, goroutines}]`                                                     |
| `audit`               | `Audit`        | `windowMs`, `verdict: ok|warn|red`, `closest`, `layers: [{name, level: ok|warn|red|idle|missing, figures, advice}]`, `detail` (with `-v`) |
| `profile`             | `Profile`      | `node`, `seconds`, `samples`, `shares: [{role, percent}]`, `rawPath`                                     |
| `ns list`, `ns ensure`| `Namespaces`   | `namespaces: [{name, state, retentionMs, running, managed: spec|system|unmanaged, changed}]`            |
| `rollback list`       | `ApplyHistory` | `applies: [{applyId, command, by, startedAt, nodes, result, rollbackable}]`                             |
| `targets`             | (file-SD array, no envelope) | as above; the format Prometheus reads                                                    |
| `version`             | `Version`      | `tops: {version, commit, goVersion}`, `nodes: [{name, helper, sha256Match}]`                             |

`NodePlan`: `{name, reachable, adopt (never managed by tops), version: {live, spec}, files: [{path, action, effect, diff, drift}], firewall: [{group, ports, add: [...], remove: [...]}], restart: bool, hotReload: bool, validated: {ok, release, checks}}`.

`Status` example (abridged):

```json
{
  "kind": "Status", "topsVersion": "1.0.0", "cluster": "tscale-lab", "at": "2026-10-10T15:30:12Z", "healthy": true,
  "temporal": {
    "clusterName": "tscale-lab", "clusterId": "6b0e…", "serverVersion": "1.31.3",
    "persistenceStore": "postgres12", "visibilityStore": "postgres12",
    "shards": {"spec": 512, "live": 512, "owned": 512},
    "rings": {
      "frontend": {"expected": 2, "members": ["10.78.0.11:7233", "10.78.0.12:7233"]},
      "history":  {"expected": 2, "members": ["10.78.0.11:7234", "10.78.0.12:7234"]},
      "matching": {"expected": 2, "members": ["10.78.0.11:7235", "10.78.0.12:7235"]},
      "worker":   {"expected": 2, "members": ["10.78.0.11:7239", "10.78.0.12:7239"]}
    },
    "unknownMembers": []
  },
  "datastore": {"kind": "postgres", "reachable": true, "via": "tscale-01",
                "schemas": {"temporal": "1.19", "temporal_visibility": "1.14"}},
  "nodes": [
    {"name": "tscale-01", "address": "10.78.0.11", "reachable": true, "service": "active",
     "version": "1.31.3", "serving": true, "rings": ["frontend", "history", "matching", "worker"],
     "shards": 263, "memory": {"currentBytes": 1288490188, "highBytes": 2831155200},
     "clockSynced": true, "firewall": "ok",
     "checks": [{"name": "service", "ok": true, "detail": "active since 14:02, 0 restarts"}]}
  ]
}
```

Drill assertion: `tops status --json | jq -e '.temporal.shards.owned == 512 and (.temporal.rings.history.members | length) == 2'`.

v2 (T6, N5): `datastore.kind` is `cassandra`; `schemas` holds the keyspace version; two objects are added.
One Cassandra host down with quorum kept, or ES `yellow`, is a warning (`!`, exit 0); quorum lost, ES `red`
or the index missing fails the check (exit 5).

```json
"datastore": {"kind": "cassandra", "reachable": true, "via": "trscale-fe-1", "schemas": {"temporal": "1.13"},
  "cassandra": {"keyspace": "temporal", "replication": {"dc1": 3}, "quorum": true,
    "hosts": [{"address": "10.80.0.51", "up": true, "version": "5.0.9"},
              {"address": "10.80.0.52", "up": false, "error": "dial tcp 10.80.0.52:9042: i/o timeout"},
              {"address": "10.80.0.53", "up": true, "version": "5.0.9"}]},
  "elasticsearch": {"url": "http://10.80.0.61:9200", "status": "green", "nodes": 1,
    "index": "temporal_visibility_v1", "indexExists": true}}
```

## Temporal persistence

### v1: PostgreSQL

tops creates both databases if missing (`temporal-sql-tool create-database`), versions them
(`setup-schema -v 0.0`), and migrates them with the schema embedded in the target release's tool
(`update-schema --schema-name …`, verified). It never drops or alters anything else. Existing empty databases
created by a DBA are used as they are (kept from `setup-databases.sh`).

| Database               | `--schema-name`              | Schema for 1.30.x (verified 1.30.7) | Schema for 1.31.x (verified 1.31.3) |
| ---------------------- | ---------------------------- | ----------------------------------- | ----------------------------------- |
| `temporal`             | `postgresql/v12/temporal`    | 1.18                                | 1.19                                |
| `temporal_visibility`  | `postgresql/v12/visibility`  | 1.13                                | 1.14                                |

| Table read by tops                        | Why                                                                    |
| ----------------------------------------- | ---------------------------------------------------------------------- |
| `schema_version` (`curr_version`)         | `status`, `upgrade` (before/after), `bootstrap`                         |
| `cluster_metadata_info`                   | `bootstrap` refuses when it has rows (a cluster already lives here)    |
| `pg_database` (in `adminDatabase`)        | Does the database exist                                                |
| `max_connections` (`SHOW`)                | Connection-budget warning (above)                                      |
| `pg_stat_activity` (own role only)        | `investigate`: connections by state, statements running > 2 s         |

Datastore prerequisites (the operator's, printed by `bootstrap` preflight): role `persistence.postgres.user`
with `CREATEDB` or the two databases pre-created and owned by it; `pg_hba` admits every node address;
`max_connections >= Σ over nodes (roles in the process × (dbMaxConns + dbVisibilityMaxConns)) + 10`. Each
role in a process opens its own pools, so an all-in-one 2c4g node needs 4 × 25 = 100, not 25. `plan`,
`bootstrap` and `node add` read `SHOW max_connections` and warn below it; temporal-node-scale M2 checks the
formula against `persistence_sql_open_conn` under load.

### T6: Cassandra and Elasticsearch

| Store          | Object                                         | tops does (from one node, target release tool)                                               | Version (1.31.3) |
| -------------- | ---------------------------------------------- | --------------------------------------------------------------------------------------------- | ---------------- |
| Cassandra      | keyspace `temporal`, `NetworkTopologyStrategy {dc1: 3}` | `temporal-cassandra-tool create-keyspace -k temporal --rf 3 --dc dc1` (env `CASSANDRA_HOST`, `CASSANDRA_PASSWORD`), `setup-schema -v 0.0`, `update-schema --schema-name cassandra/temporal` | 1.13 (verified) |
| Elasticsearch  | cluster settings + index template; index `temporal_visibility_v1` | `temporal-elasticsearch-tool setup-schema`, `create-index --index temporal_visibility_v1`, `update-schema --index temporal_visibility_v1` (env `ES_SERVER`, `ES_USER`, `ES_PWD`). The tool is labelled EXPERIMENTAL in 1.31.3 | Template from the release |

All of these were measured idempotent on 2026-10-05 (ran twice). Read-only health (T6): per Cassandra host
`SELECT release_version FROM system.local` (up/down; quorum = hosts up >= floor(RF/2) + 1), keyspace replication
from `system_schema.keyspaces`, `temporal.schema_version`, `cluster_metadata_info` (identity); ES
`GET /_cluster/health` (status) and `HEAD /temporal_visibility_v1`. Status shape: "`--json` outputs" (N5).
