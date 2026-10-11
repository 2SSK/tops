# tops roadmap

A navigation and build path for `tops`, distilled from [prd.md](prd.md), [trd.md](trd.md),
[app-flow.md](app-flow.md), [design-brief.md](design-brief.md), [backend-schema.md](backend-schema.md),
[concepts.md](concepts.md) and [implementation-plan.md](implementation-plan.md).

The implementation plan is the detailed task list. This file is the *path*: what to read, what to build,
what proves it, and how the "deploy nodes" and "deploy individual roles" capabilities are grown on top of
one another. When this file and a sibling disagree, the sibling wins.

---

## 0. What you are building

One Go binary, `tops`, run from your workstation, that deploys, controls and investigates a bare-metal
Temporal cluster over SSH. One declarative spec (`cluster.yaml`) describes the cluster. `tops plan` shows
what would change per node; `tops apply` changes it node by node, restarting a node only when every other
node is in the ring.

The binary has two faces (TRD "Architecture"):

- the **CLI** on your workstation: loads the spec, renders files in memory, decides, orchestrates, prints;
- the **node helper** — the *same binary*, copied to a node and run as root (`tops node <op>`) — executes
  one typed operation per invocation and exits. It never decides; it reports facts.

Two supported topology shapes, released as two majors (PRD "Summary"):

| Version | Supports | Project that uses it |
| --- | --- | --- |
| v1 (`1.x`) | **All-in-one nodes**: all four Temporal roles in one process per node, PostgreSQL, Web UI | temporal-node-scale |
| v2 (`2.0`) | **Role topology**: frontend / history / matching / worker on separate hosts, Cassandra + Elasticsearch | temporal-role-scale |

Build order across the whole set (PRD "Summary", plan "Order and dependencies"):

```
tops v1 (T1-T5, v0.1.0 .. v1.0.0)
   -> temporal-node-scale M1-M4         (uses tops v1)
      -> temporal-role-scale R0 (its lab, 10.80.0.0/24)
         -> tops v2 (T6, v2.0.0)
            -> temporal-role-scale R1-R5
```

v2 is committed, not optional (PRD D6). It is not designed blind: it is built against role-scale's real lab.

---

## 1. How to read these documents

Read in this order, and revisit as the plan says per task:

1. **prd.md** — what and why. Goals G1-G7, non-goals, decisions D1-D15, safety table, milestones T1-T6,
   testing strategy.
2. **concepts.md** — the "learn first" spine. Read it fully before T1. If you cannot answer a concept's
   question without notes, read its link before typing that task's code.
3. **trd.md** — how it is built: constraints, architecture, package layout, library choices, the SSH/helper
   protocol, the ring gate, the lock, snapshots, plan/apply, upgrades, exit codes, the T6 extension points.
4. **design-brief.md** — command grammar, global flags, help text style, output design, `--json` contract,
   error catalogue, example outputs.
5. **backend-schema.md** — the exact shapes: spec, secrets, profiles, helper protocol types, node state,
   generated files, metrics, `--json` outputs, persistence.
6. **app-flow.md** — the journeys (bootstrap, adopt, plan/apply, restart, node add/remove, upgrade,
   rollback, incident, ns ensure) and the refusal index.
7. **implementation-plan.md** — the task list T1.1...T6.8, each with Goal / Learn first / Build / Verify /
   Pitfalls, plus the acceptance tables, CI, release steps, definition of done.

Rule of the plan: for every task, write the failing table-driven test first, make it pass with the least
code, refactor. Read the task's **Learn first** links *before* writing.

---

## 2. The thread that never changes: nodes vs roles

Everything you build serves two shapes that differ in exactly one place. Keep that place clean (TRD
"Dependency direction", PRD R6).

| Question | v1 (nodes) | v2 (roles) |
| --- | --- | --- |
| What runs on a host? | one `temporal-server` process running all four roles | one process per host running its `roles` set |
| Where is the answer coded? | `internal/topology.Services(node)` returns all four | `internal/topology.Services(node)` returns `node.roles` (empty for a UI-only host) |
| Spec | no `roles` field | `nodes[].roles` required; `nodes[].profile` required iff `roles` non-empty; top-level `profile` removed |
| Shard headroom | over all nodes | over history nodes only |
| Peers firewall rule | every node | nodes with roles |
| `/health`, nginx | every node | frontend hosts + the UI host |
| Ring reads | every node's frontend | frontend hosts only |
| Bootstrap order | spec order | role stages: frontend → history → matching → worker → UI host |
| Datastore | PostgreSQL | Cassandra + Elasticsearch (both together) |

So the *capability* you are growing is the same; v2 swaps the single seam. The TRD's "T6 extension points"
table lists every seam and its v1 behaviour — read it now, and keep each seam in one place as you build v1,
so T6 is a swap and not a rewrite.

---

## 3. The capability ladder ("deploy" means five different things)

"Deploy" is not one task. You grow it in layers (AGENTS.md: start from the smallest version that works end
to end, add on top). Each rung is a milestone with a release.

| Rung | You can... | Milestone | Depends on |
| --- | --- | --- | --- |
| 0 | **See** a running cluster from one terminal: status, audit, top, logs, investigate | T1 / v0.1.0 | a cluster built by something else |
| 1 | **Manage existing all-in-one nodes**: plan, apply, restart, rollback, namespaces | T2 / v0.2.0 | rung 0 |
| 2 | **Build a node from bare metal**: bootstrap, node add/remove, firewall, nginx, node_exporter, UI | T3 / v0.3.0 | rung 1 |
| 3 | **Upgrade** the binary with schema migration | T4 / v0.4.0 | rung 2 |
| 4 | **Profile** per-role CPU share; harden every refusal | T5 / v1.0.0 | rung 3 |
| 5 | **Deploy roles**: split topology, Cassandra + ES, per-role gate and bootstrap | T6 / v2.0.0 | rung 4 + role-scale R0 |

Mapping the two questions you asked to the ladder:

- **"deploy nodes"** = T1.3 (spec) + T2 (render/plan/apply/rollback/restart) + T3 (bootstrap, node add/remove,
  serve).
- **"deploy individual roles"** = T6.1 (spec v2 roles) + T6.2 (topology, units, firewall, nginx, targets per
  role) + T6.3 (ring gate per role, bootstrap stages) + T6.4/T6.5 (Cassandra/ES) + T6.6 (persistence
  identity) + T6.7 (per-role node add/remove).

---

## 4. Phase-by-phase path

Each phase lists: end state, what to read, the tasks, what you create, and the proof. The proof commands in
the plan assume the `temporalScale` lab; §6 maps them onto your VPSLAB.

### Phase 0 — Repo, toolchain, CI (T1.1)

**End state.** An empty `tops` that builds, lints and tests in CI.

**Read.** Plan T1.1; concepts "Supply chain"; TRD "Package layout".

**Build.** `go.mod` (`go 1.26`, `toolchain go1.26.8`); `cmd/tops/main.go` with the root command and grouped
help (design brief "Command grammar" — groups Spec, Change, Look, Integrate); the Makefile (plan "Repository
layout and make targets"); `.golangci.yml` (plan "CI"); `.github/workflows/ci.yml`; README stub; CHANGELOG.

**Prove.**
```sh
make lint && make test && make build && ./bin/tops --help | grep -q 'Look'
test $(stat -c %s bin/tops) -lt 36700160
```

**Pitfalls.** `go.temporal.io/server` v1.31.3 needs Go >= 1.26.8 — pin the `toolchain` line. Add no
dependency a task does not use yet. `gocyclo` max 10 bites command handlers first: keep them thin.

### Phase 1 — Read-only tops / v0.1.0 (T1.2-T1.13)

**End state.** Against a running 2-node cluster: `status` (exit 5 when unhealthy), `top`, `logs`, `audit`
(names the busiest layer), `investigate`, `ns list`, `targets`, `version`, `validate`, `init`, `completion`.

**Read.** concepts "Temporal", "Linux hosts", "Remote operation", "Metrics"; design brief "Output design",
"Errors"; TRD "Exit codes", "Error model"; backend-schema "Cluster spec", "Node helper protocol", "Node
state".

**Build, in order.**

| Task | Builds | Key learning (read first) |
| --- | --- | --- |
| T1.2 | `internal/term`: `UserError{Code,What,Why,Fix}`, JSON writer, symbols, progress; exit-code mapping in `main` | clig.dev; `NO_COLOR`; lipgloss tables |
| T1.3 | `internal/spec` (types, `Defaults`, `Validate` returning all errors, 15-line secrets parser, mode check), `internal/profile`; `tops validate`, `tops init` | `yaml.Decoder.KnownFields`; backend-schema spec/secrets/profiles; Temporal shards |
| T1.4 | `internal/wire` (envelope, op types), `internal/helper`: root check, stdin decode, exit 0/1/2; ops `hello`, `facts`, `top`, `journal` | cgroup v2, PSI, `proc(5)`, go-systemd D-Bus, `journalctl -o json` |
| T1.5 | `internal/remote`: SSH clients, `knownhosts`, agent, preflight, tunnels, helper upload + newest-3 cleanup; `test/helper` image; `make helper-test` | `x/crypto/ssh`, `direct-tcpip` RFC 4254, `AllowTcpForwarding`, `sudo -n` |
| T1.6 | `internal/cluster` (health, `DescribeCluster`, `DescribeHistoryHost`), `internal/datastore/postgres` (schema version, `cluster_metadata_info`, `max_connections`) over tunnels | Temporal architecture; gRPC health; `grpc.WithContextDialer`; admin API; pgx `DialFunc` |
| T1.7 | `tops status` | design brief "`tops status`"; exit codes |
| T1.8 | `internal/metrics`, `internal/audit`, `tops audit` | exposition format; histogram quantiles; expfmt; Temporal metrics |
| T1.9 | `tops top` | Go GC guide (`GOMEMLIMIT` soft vs `MemoryMax` hard); `memory.events` |
| T1.10 | `internal/journal`, `tops logs` | journalctl JSON fields; zap JSON lines |
| T1.11 | `internal/investigate`, `tops investigate` | workflow execution, task queues, UI URL shape |
| T1.12 | `ns list`, `targets`, `version`, `completion` | Prometheus `file_sd_config`; cobra completion + `ValidArgsFunction` |
| T1.13 | `test/e2e` harness, lab-contract check, bash fixture, `release.yml`, v0.1.0 | build tags; GoReleaser; attestations; SLSA |

**Proof.** Every row of the plan's "T1 acceptance" table (v0.1.0 installs with `go install`; status shard sum
512 and ring 2 of 2; audit names a busy layer under load; logs merged from both nodes; helper uploaded once).

**Pitfalls.** An unreachable node is a finding (exit 5), not a crash. Never print "healthy" when a value
could not be read. A missing metric is "metric missing", never green. Only one JSON document on stdout.

### Phase 2 — Manage existing nodes / v0.2.0 (T2.1-T2.11)

**End state.** `tops apply` adopts the cluster the bash scripts built; second `plan` exits 0; dynamic-config
changes apply without restart; static changes restart one node at a time; killing a node mid-apply stops the
rollout; rollback and its undo both pass status; the cluster lock fences a second run.

**Read.** app-flow journeys 2, 3, 4, 7, 9; TRD "Spec, secrets and rendering", "Plan and apply", "Cluster
lock", "Snapshots, history and rollback"; backend-schema "Generated files", "Node state"; concepts
"Declarative desired state", "Leases and fencing", "Rolling change".

**Build, in order.**

| Task | Builds |
| --- | --- |
| T2.1 | `internal/render`: `[[ ]]` templates, `NodeInput`, `FileSet` with effect classes, dynamic-config merge; golden files per profile × node |
| T2.2 | Helper op `validate` using the target `temporal-server` binary |
| T2.3 | `internal/plan`: desired vs live, unified diffs, secret masking, drift, exit 0/2/3 |
| T2.4 | `internal/safety`: every refusal in the PRD Safety table as a pure function |
| T2.5 | Cluster lock: helper `lock`, renewal loop, fencing on every mutating op |
| T2.6 | Helper `write`, `restore`, `history`; `applied.json`; atomic temp+rename |
| T2.7 | `internal/gate`: before/after conditions, 2 s poll, stability, timeout report |
| T2.8 | `internal/rollout`; `tops apply`, `tops restart [--all] [--force]` |
| T2.9 | `tops rollback list`, `tops rollback <id>` |
| T2.10 | `tops ns ensure` + the namespace step at the end of `apply` |
| T2.11 | e2e, v0.2.0 |

**Proof.** Plan "T2 acceptance" rows: first apply adopts; a pre-existing workflow completes; second plan is
clean; dynamic-config change restarts nothing; static change restarts nodes one at a time with shards owned
throughout; killing 02 mid-apply stops the rollout (exit 1, node untouched); rollback and undo; lock refuses
a second run; a fenced run cannot write; `ns ensure` idempotent.

**Pitfalls.** Re-plan inside `apply`, never apply an earlier plan. `--force` skips the before-gate only and
never with `-y`. Drift (hand edit) and change (spec edit) are different plan lines. Snapshot before the
first byte changes.

**This is where "manage nodes" lands.** After T2 you can drive an all-in-one cluster declaratively: render
each node's five files, diff, apply only what changed, gate restarts, snapshot, roll back.

### Phase 3 — Build nodes from bare metal / v0.3.0 (T3.1-T3.9)

**End state.** `make reset && make up && tops bootstrap` gives a healthy 2-node cluster with UI in under 5
minutes; `node remove` then `node add` round-trips with shards owned throughout.

**Read.** app-flow journeys 1, 5; TRD "Ring gate"; backend-schema "Stage", "Files tops installs on a node";
concepts "Cluster immutables", "History shards", "atomic file replacement", "iptables".

**Build, in order.**

| Task | Builds |
| --- | --- |
| T3.1 | `internal/release`: URLs per arch, `checksums.txt` fetch/parse, supported-minors table |
| T3.2 | Helper `stage`: `temporal` user, packages, verified releases, `.tops.json`, symlinks |
| T3.3 | Host files: firewall chain + unit, nginx site + allow lists, journald/rsyslog drop-ins, ui-server config + unit |
| T3.4 | `plan` for host files: firewall delta per port group, unknown files as removals |
| T3.5 | Helper `schema`: create databases, `setup-schema -v 0.0`, `update-schema --schema-name`; `max_connections` budget |
| T3.6 | `tops bootstrap`: preflight, plan, stage, schema, validate, firewall-first writes, serial first start with the gate, UI, namespaces, manual steps, resume |
| T3.7 | `tops node add`: firewall on existing nodes first, then stage/write/start/gate; shard movement reported |
| T3.8 | `tops node remove`: headroom refusal, `disable-now`, wait for full ownership |
| T3.9 | Drop the bash fixture; e2e; v0.3.0 |

**Proof.** Plan "T3 acceptance" rows: fresh lab to healthy cluster < 5 min with UI; `/health`; second plan
clean; firewall from the spec; bootstrap refuses a used datastore; bootstrap resumes; remove/add round trip
with shards owned throughout and dips < 10 s; remove refused when survivors cannot hold the shards.

**Pitfalls.** Resume is decided by the datastore (this cluster's name in `cluster_metadata_info`), not by
node appearance. Do not assert an even shard split, and do not assert "never below 512" — bound the dip.
`DEBIAN_FRONTEND=noninteractive`, or apt hangs. `ESTABLISHED,RELATED` before `DROP`.

**This is the "deploy nodes" rung.** After T3 a fresh Ubuntu host becomes a Temporal node with one command,
and nodes join and leave a running cluster safely.

### Phase 4 — Upgrade / v0.4.0 (T4.1-T4.4)

**Read.** app-flow journey 6; TRD "Upgrade and schema migration"; concepts "Schema versions".

| Task | Builds |
| --- | --- |
| T4.1 | Templates, schema targets and audit fixtures for 1.30.x and 1.31.x |
| T4.2 | Upgrade path rules: downgrade, minor skip, unsupported, mixed versions, backup confirmation |
| T4.3 | `rollout` upgrade: stage everywhere, migrate schema once, roll binaries with the gate, resume |
| T4.4 | Spec write-back (`temporal.version` via `yaml.Node`, comments kept); e2e; v0.4.0 |

**Proof.** Plan "T4 acceptance" rows: bootstrap at 1.30.7; upgrade migrates once (schema 1.18→1.19, 1.13→1.14)
and rolls with no unowned shards; `schema_update_history` has exactly one row; two-minor jump and downgrade
refused; upgrade rollback puts the binary back but the schema stays; resume after interruption.

### Phase 5 — Harden v1 / v1.0.0 (T5.1-T5.3)

**Read.** concepts "Metrics and delivery"; app-flow refusal index; PRD Safety table.

| Task | Builds |
| --- | --- |
| T5.1 | `internal/roleprof`, `tops profile <node>` (CPU share per role inside the all-in-one process) |
| T5.2 | One `TestRefuse…` per row of the refusal index |
| T5.3 | Coverage gates, README, generated command reference, completions, v1.0.0 |

**Proof.** Profile attributes >= 90% of samples to a role/shared; `grep -c 'func TestRefuse'` equals the
refusal index; `make cover` meets gates; full suite on the release binary.

**This closes temporal-node-scale M1** (plan "T5 acceptance").

### Interlude — the other two projects (not tops code)

- **temporal-node-scale M1-M4** consumes tops v1; this is where you delete the bash scripts the milestones
  let you delete (plan "Script deletion map").
- **temporal-role-scale R0** builds the split-topology lab on `10.80.0.0/24` (its deliverable). T6 needs it.

Read role-scale's own TRD ("How the roles talk") and concepts before starting T6.

### Phase 6 — Roles, Cassandra, Elasticsearch / v2.0.0 (T6.1-T6.8)

**End state.** On the role-scale lab: bootstrap a split cluster; per-role restart gate; a frontend-only
profile change restarts frontends only; persistence kind and keyspace changes refused; the node-scale lab
still green on the migrated spec.

**Read.** UI/role-scale TRD; backend-schema "v2 (T6): roles"; TRD "T6 extension points"; concepts
"Membership", "Cluster immutables"; AGENTS.md (no compatibility layer).

| Task | Builds | Gap closed |
| --- | --- | --- |
| T6.1 | Spec v2: `roles`, per-node profiles, Cassandra/ES persistence; node-scale spec migrated | N3, N4 |
| T6.2 | `topology.Services` from roles; units with `--service`; peers per role; nginx/`/health` per role; `targets` with `role` | N6, N7 |
| T6.3 | Rings through frontends; per-role gate; bootstrap role stages; `--force` for the last frontend | N2 |
| T6.4 | `datastore/cassandra`: per-host up, quorum, replication, schema version, identity | N5 |
| T6.5 | `datastore/elasticsearch`: `_cluster/health`, index exists, schema order setup→create-index→update | N5 |
| T6.6 | Render Cassandra/ES `temporal.yaml`; persistence identity through new addresses; Fixed checks | N1 |
| T6.7 | Per-role `status`, `audit`, `top`; per-role `node add`/`node remove` | |
| T6.8 | e2e on both labs; v2.0.0 | |

**Proof.** Plan "T6 acceptance" table: split cluster bootstraps; Cassandra + ES + 512 shards (history only);
Cassandra health in status; per-role restart gate; frontend-only profile change moves no shards; profile
larger than the container refused; persistence change refused; keyspace change refused; targets carry roles
and none for the UI host; no peers rule for the UI host; `/health` only on frontends; add a matching host;
node-scale still green on v2.

**Pitfalls.** `DescribeCluster` says "cassandra" for any keyspace: the kind check alone misses N1. `/health`
proxies to the frontend, so on a history host it always fails and an LB would mark a healthy cluster down.
The Cassandra driver discovers peers: the dialer must tunnel every host, not only the first contact point.

---

## 5. Package map (what to create, and when)

Create a package when a phase first needs it; do not stub ahead (plan "Pitfalls").

| Package | First needed | Does |
| --- | --- | --- |
| `cmd/tops` | T1.1 | wiring: root command, signals, error → exit code |
| `internal/cli` | T1.2 | cobra commands (one file per command), flags, confirmation, output mode |
| `internal/term` | T1.2 | tables, colours, progress, prompts, JSON writer, error rendering |
| `internal/spec` | T1.3 | `cluster.yaml` and `secrets.env`: types, load, defaults, validate, version write-back |
| `internal/profile` | T1.3 | embedded size profiles, host fit and shard headroom math |
| `internal/wire` | T1.4 | helper request/response types, shared by remote and helper |
| `internal/helper` | T1.4 | node-side ops behind `tops node <op>` |
| `internal/remote` | T1.5 | SSH clients, known_hosts, helper upload/exec, tunnels |
| `internal/cluster` | T1.6 | Temporal API over tunnels |
| `internal/datastore/postgres` | T1.6 | read-only datastore facts |
| `internal/metrics` | T1.8 | scrape, parse, deltas, quantiles |
| `internal/audit` | T1.8 | layer verdicts and thresholds |
| `internal/journal` | T1.10 | journal entries → readable lines, merge, summary |
| `internal/investigate` | T1.11 | UI URL parsing, workflow report |
| `internal/runlog` | T1.13 | slog JSON run log in the user's state dir |
| `internal/buildinfo` | T1.13 | version, commit, helper checksum |
| `internal/render` | T2.1 | embedded templates, per-node file set, golden tests |
| `internal/plan` | T2.3 | desired vs live per node, diffs, effect class, order |
| `internal/safety` | T2.4 | every refusal as a pure function |
| `internal/gate` | T2.7 | wait for SERVING, ring membership, shard ownership |
| `internal/rollout` | T2.8 | apply, restart, upgrade, rollback, node add/remove, bootstrap |
| `internal/topology` | T2.8 (seam) | node → services; expected ring members (the T6 seam) |
| `internal/release` | T3.1 | release URLs, checksums, supported minors |
| `internal/roleprof` | T5.1 | CPU profile → share per role |
| `internal/datastore/cassandra` | T6.4 | Cassandra reads and schema |
| `internal/datastore/elasticsearch` | T6.5 | ES health and schema |

Dependency direction (TRD): `cli -> rollout -> {plan, safety, gate, render} -> {spec, profile, topology}`;
`rollout -> remote, cluster, datastore`. `helper` imports only `wire`, `buildinfo` and stdlib. `safety`,
`plan`, `render`, `audit`, `journal`, `investigate`, `roleprof` do no I/O — that is what makes the 90%
coverage target reachable.

---

## 6. Running this against your lab (VPSLAB, not temporalScale)

The plan's Verify commands are written for `temporalScale` (`LAB=~/Code/Projects/building/temporalScale`).
You are using VPSLAB. It is the same *contract* with different names, so translate:

| Plan / docs say | Your VPSLAB |
| --- | --- |
| `make up`, `make check`, `make reset` | same targets (VPSLAB `make up/check/down/reset`) |
| `make ssh ARGS='01 <cmd>'` | `make ssh 01` (positional, interactive); for one command: `ssh -F keys/ssh_config vps-01 <cmd>` |
| `make push` (bash-built fixture) | none — until T3, seed the cluster differently (see below) |
| nodes `tscale-01..03` | `vps-01..03` |
| IPs `10.78.0.11-13` | `10.80.0.11-13` |
| `tscale-pg` `10.78.0.20` | `vps-pg` `10.80.0.20` (you added it) |
| keys `lab/keys/` | `keys/` |
| spec `cluster: tscale-lab` | `cluster: vpslab` |

**Lab deltas to decide before T1.6** (the read commands need a real cluster to read):

1. **Size.** The plan's lab is 2 CPU / 4 GiB containers, and the v1 profile `2c4g` assumes that. VPSLAB
   nodes are 1 CPU / 1 GiB, so the profile fit check (`refused.profile_too_large`) will refuse. Either bump
   the `vps-0x` services to `cpus: 2`, `mem_limit: 4g`, or accept that your in-lab spec needs a small
   profile (the size-named lab profiles are only added in T6). Bumping to match the plan is the least
   surprising.
2. **Capabilities.** VPSLAB uses `cap_add: [NET_ADMIN, SYS_ADMIN]` and a private cgroup instead of
   `privileged: true` + `cgroup: host`. That is enough for systemd, iptables and `sudo -n`. If a helper test
   needs the host cgroup namespace, revisit; the TRD's `top` reads `/proc/<MainPID>/cgroup` precisely to work
   without it.
3. **`make push` fixture.** T1.13 and T2's adopt tests build a cluster with bash scripts, then read/adopt it.
   VPSLAB has no bash installer. Until T3 gives you `bootstrap`, you need *something* to deploy a cluster to
   read. Two options: keep a minimal copy of the node-scale scripts/`compose.yaml` in your lab repo, or
   accept that read tests run against a cluster you deploy by hand. Decide at T1.13, not now.
4. **Subnet collision at T6.** VPSLAB is `10.80.0.0/24`; role-scale R0 is *also* `10.80.0.0/24`. When you
   reach T6, one of them moves (the plan's role-scale addresses are 10.80.0.11/21/31/41/51..53/61/70).

Everything else — the spec file, the helper protocol, the exit codes — is lab-agnostic (PRD Q5: tops must
not know it talks to containers).

---

## 7. Working method (do this every task)

1. **Read** the task's "Learn first" links in concepts.md / the official docs. If you cannot answer the
   concept question, stop.
2. **Write the failing table-driven test first** (Go: stdlib `testing` + `testify`). Golden files get an
   `-update` flag and you review every diff by eye (render, plan).
3. **Make it pass** with the least code. Keep command handlers thin (`gocyclo` max 10); logic goes in
   `internal/`.
4. **Refactor** while green.
5. **Verify** with the task's commands; then the milestone's acceptance table.
6. **Break it on purpose** to confirm the guard: e.g. chmod `secrets.env` 644 and watch `validate` refuse;
   hand-edit a rendered file and watch `plan` call it drift; stop a node and watch `status` exit 5.
7. **Record** shape changes in backend-schema.md and output changes in design-brief.md in the same change.

---

## 8. Guardrails and gates

- **AGENTS.md.** No backward compatibility; remove obsolete paths. Simplest implementation that meets the
  requirement. Grow in layers; never trade a working product for unfinished complexity. Modular, separated
  concerns. Prefer established libraries; check the ones you have before adding another. Long-term decisions.
- **Binary size.** PRD limit 40 MB; CI fails above 35 MB. Measured probe: 18.6 MB.
- **Coverage.** >= 80% overall; >= 90% `internal/safety` and `internal/plan`.
- **Lint.** `gofmt -l .` empty; golangci-lint with errcheck, govet, staticcheck, revive, gosec, errorlint,
  gocyclo (10), misspell, bodyclose, noctx, sloglint, contextcheck.
- **Vuln.** `govulncheck ./...` clean.
- **No secrets** in any diff, log, argv, or history record. `temporal.yaml` must be safe to print whole;
  secrets live only in `temporal.env` (0600) and are masked in diffs.
- **Release gate.** No release without an attached green e2e report (PRD R9).

---

## 9. Definition of done

**Per task** (plan "Definition of done"): test written first and table-driven; golden diffs reviewed;
`make lint` and `make test` clean (no `//nolint` without a reason); errors wrapped with context, user-facing
errors with code/what/why/fix; no secret in logs (a test for each new secret field); shapes reflected in
backend-schema.md and output in design-brief.md; user-visible behaviour has an e2e test; you can explain
every concept the task named.

**Per milestone:** acceptance table passes on the lab; CI green, coverage gates met, binary < 40 MB; e2e
report, checksums, SBOM and attestations on the release; README and CHANGELOG updated; the scripts the
milestone covers deleted in temporal-node-scale.

---

## 10. Suggested schedule (from PRD "Milestones")

| Milestone | Scope | Effort |
| --- | --- | --- |
| T1 / v0.1.0 | repo, CI, spec, SSH, helper, read-only commands | 2-3 wk |
| T2 / v0.2.0 | render, plan, apply, snapshots, rollback, restart, lock, ns ensure | 3 wk |
| T3 / v0.3.0 | bootstrap, node add/remove, firewall, nginx, node_exporter, UI | 2 wk |
| T4 / v0.4.0 | upgrade with schema migration | 1-2 wk |
| T5 / v1.0.0 | profile, every refusal tested, docs, README | 1-2 wk |
| T6 / v2.0.0 | roles, Cassandra, Elasticsearch | 4-5 wk |

Start now at **Phase 0 (T1.1)**. Do not look past T1.2 until T1.1's four proof commands pass.
