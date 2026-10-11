# tops PRD

Project: tops (Temporal Ops), `github.com/2SSK/tops`. Status: draft. Date: 2026-10-10.
Sibling docs: [TRD](trd.md) · [App flow](app-flow.md) · [Design brief](design-brief.md) ·
[Backend schema](backend-schema.md) · [Implementation plan](implementation-plan.md) · [Concepts](concepts.md)

Related projects: temporal-node-scale (`temporalScale/docs/prd.md`, built second, first user of tops v1) and
temporal-role-scale (`temporal-role-scale/docs/prd.md`, built third, first user of tops v2).

## Summary

`tops` is one Go binary that deploys, controls and investigates a bare-metal Temporal cluster from the
operator's workstation over SSH. One declarative spec file describes the cluster. `tops plan` shows what
would change on every node; `tops apply` changes it node by node, and restarts a node only when every
other node is in the ring.

| Version | Supports                                                              | Used by                      |
| ------- | --------------------------------------------------------------------- | ---------------------------- |
| v1      | All-in-one nodes (all four roles per process), PostgreSQL, Web UI     | temporal-node-scale          |
| v2      | Role topology (frontend, history, matching, worker on separate hosts), Cassandra + Elasticsearch | temporal-role-scale |

v2 is a committed milestone, not a maybe. Build order across the set: tops v1 → temporal-node-scale
M1-M4 → temporal-role-scale R0 (its lab) → tops v2 → temporal-role-scale R1-R5.

## Problem

- The cluster is operated with bash today. temporal-node-scale has ~770 lines of scripts; tadm (the
  inspiration, from the author's work) has ~1,800. Both run on one node at a time after an `rsync`.
  Nothing sees the whole cluster at once: a rolling change is a human walking between terminals.
- No machine-readable output. A drill cannot assert "512 shards owned" without parsing coloured text.
- Testing is `shellcheck`. The safety logic (ring check before restart, profile vs host, shard headroom)
  has no tests.
- Each script set is tied to one topology. A role split means rewriting every script.
- tadm's ideas are good and worth keeping: diff before apply, apply only what changed, restart only
  when the rest of the ring is healthy, config snapshots with an undoable rollback, `top`, `audit` in
  plain words, readable logs, `investigate` from a UI URL, namespace management.

## Users

| User                          | Wants                                                                                   |
| ----------------------------- | --------------------------------------------------------------------------------------- |
| The operator (the author)     | Run either cluster project from one terminal: build, change, upgrade, drill, investigate |
| Drill and test scripts        | `--json` output and exit codes to assert on                                              |
| A reviewer / hiring manager   | A real Go codebase with tests, releases and a clear scope; a 5-minute README             |

User stories:

- As the operator, I run `tops plan` and see, per node, which files change and whether Temporal restarts.
- As the operator, I run `tops apply` once and the whole cluster converges, one node at a time, stopping
  at the first node that does not come back healthy.
- As the operator, I run `tops audit` during an incident and read which layer is choking, in plain words.
- As a drill script, I run `tops status --json` and assert the shard sum and ring size.

## Goals

| #  | Goal                                                                                     | Measured by                                                              |
| -- | ---------------------------------------------------------------------------------------- | ------------------------------------------------------------------------ |
| G1 | Replace every temporal-node-scale script                                                 | `scripts/` deleted in temporal-node-scale; its lab passes on tops alone  |
| G2 | Cluster-wide operations from one workstation                                             | Every command takes the spec, not a node shell; no `rsync` in any runbook |
| G3 | Never make a cluster less available than the operator asked for                         | Each safety refusal has an e2e test that triggers it (list under Safety) |
| G4 | Scriptable                                                                               | Every read command and `plan`/`apply` support `--json`; exit codes documented |
| G5 | Tested like a product                                                                    | Unit coverage >= 80% (safety and diff packages >= 90%); e2e suite green on the node-scale lab for every release |
| G6 | Installable                                                                              | Tagged releases with checksums; `go install` works; binary < 40 MB      |
| G7 | v2 runs temporal-role-scale with the same command set                                    | temporal-role-scale lab built and drilled with tops v2 only             |

## Non-goals

| Not doing                                             | Why                                                                                                   |
| ----------------------------------------------------- | ----------------------------------------------------------------------------------------------------- |
| Provisioning machines, networks, DNS, load balancers  | Terraform or the provider's console. tops starts from "Ubuntu 24.04 host reachable by SSH with sudo". |
| General host configuration (users, SSH hardening, packages beyond Temporal's needs, Docker) | Ansible territory. tops touches only what a Temporal node needs (list under Scope boundary). |
| Installing or operating PostgreSQL, Cassandra or Elasticsearch | External datastores. tops creates Temporal's databases, keyspace, schema and index inside them, and reads their health. PG HA is a separate project. |
| Deploying Prometheus and Grafana                      | Owned by each cluster repo as code (see D8). tops emits scrape targets from the spec.                |
| A daemon, agent or control plane on nodes             | See D1. Nothing runs between tops invocations.                                                        |
| Kubernetes                                            | The official Helm chart exists. These projects are about what the chart hides.                       |
| Multi-cluster replication, archival, mTLS             | Separate topics; mTLS is listed as deferred.                                                          |
| Backups of the datastores                             | The datastore's own tooling. `tops upgrade` asks for confirmation that a backup exists.               |
| A Temporal Cloud-like service                         | One operator, one cluster per spec. No tenants, no self-service.                                      |
| Supporting OSes other than Ubuntu 24.04 on nodes      | One target, tested. Workstation: Linux and macOS.                                                     |

## Operating model (decision D1)

| Option                                                                 | For                                                         | Against                                                                                                         | Verdict |
| ---------------------------------------------------------------------- | ----------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------- | ------- |
| A. Node-local, like tadm (copy the tool, run on each node)              | Simple; no remote execution                                 | No cluster-wide view or rolling apply; the human is the orchestrator; the spec must be copied everywhere        | No      |
| B. Workstation-driven over SSH from one declarative spec                | One place for intent; cluster-wide plan; nothing to run on nodes between calls; works where only port 22 is open | SSH and sudo design matter; a long apply depends on the workstation staying up                       | **Yes** |
| C. Agent on every node + control plane                                  | Continuous reconciliation; no SSH                            | Another service to deploy, secure, upgrade and monitor; bootstrap problem; far beyond one operator's needs       | No      |
| D. Generate Ansible playbooks                                           | Established executor                                        | The Temporal-aware logic (ring gate, shard checks, audit) still has to be written; two tools, two languages      | No      |

How B works:

1. The workstation reads the spec and secrets, renders every node's files in memory, and connects to
   each node over SSH (`golang.org/x/crypto/ssh`, agent or key file, strict `known_hosts`).
2. On each node it runs the **node helper**: the same `tops` binary for linux, uploaded to
   `/usr/local/lib/tops/<version>/tops` when the checksum differs, invoked as `sudo tops node <op> --json`.
   The helper does host work in Go (files, systemd, cgroups, iptables, journal) and returns typed JSON.
   No shell output is parsed.
3. Temporal APIs (frontend gRPC on `127.0.0.1:7233`) and metrics (`:9091`, `:9100`) are reached through
   SSH-forwarded connections to the node's loopback. tops needs port 22 and nothing else, so the cluster
   firewall never has to admit the workstation.
4. `apply` stages in parallel (downloads, binaries, non-restarting files) and restarts serially: one node,
   then the ring gate, then the next.

There is no state file. The truth is the spec (intent) plus what the nodes and Temporal report (reality).
Ring membership is read from Temporal, so a node that runs but is not in the spec is detected.

## Scope boundary: what tops manages on a node

| Managed by tops                                                                                   | Not managed                                                     |
| ------------------------------------------------------------------------------------------------- | --------------------------------------------------------------- |
| `temporal` system user, `/opt/temporal/releases/<v>`, `current` symlink, Temporal CLI, `tdbg`, schema tools | Other users, SSH config, unattended upgrades, kernel tuning      |
| `/etc/temporal/*` (server config incl. pprof on loopback, env file 0600, dynamic config, profile) | Application workers and their hosts                             |
| `temporal.service`, firewall chain `TEMPORAL_INPUT` and its boot unit                               | Any other iptables chain (Docker's included)                    |
| nginx site for `/health` and the UI; node_exporter; journald cap for Temporal                       | Prometheus, Grafana, Alertmanager                               |
| Web UI server (v1: on one node named in the spec)                                                   | Load balancer pool membership (printed as a step to do by hand) |
| Temporal databases / keyspace / index and their schema                                             | The datastore servers themselves                                |

Templates for all managed files are embedded in tops and versioned with it. The cluster repo owns only
the spec, the secrets file and `dynamicconfig.yaml` (operator tuning, hot-reloaded).

## Spec (shape; full schema in backend-schema.md)

```yaml
cluster: tscale-lab                 # immutable after bootstrap
temporal: { version: 1.31.3, cli: 1.9.1 }
shards: 512                         # immutable after bootstrap
profile: 2c4g                       # built-in: 2c4g 2c8g 4c16g 8c32g
persistence:                        # kind is immutable after bootstrap; v1: postgres only
  postgres: { host: 10.78.0.20, port: 5432, user: temporal, tls: false }
secrets: secrets.env                # untracked, must be 0600
ssh: { user: deploy, key: lab/keys/lab_key, knownHosts: lab/keys/known_hosts }
access: { clients: [10.78.0.1], monitors: [10.78.0.1], admins: [10.78.0.1], healthchecks: [] }
namespaces: [ { name: default, retention: 72h } ]
dynamicConfig: dynamicconfig.yaml
ui: { node: tscale-01, version: 2.54.1 }
nodes:
  - { name: tscale-01, address: 10.78.0.11 }
  - { name: tscale-02, address: 10.78.0.12 }
```

v2 adds `roles:` per node (required, no default; one process per node runs its role set, so node-scale
nodes list all four), `persistence.cassandra` + `persistence.elasticsearch`, and lets `ui:` name a host
that runs no Temporal role. No compatibility layer: temporal-node-scale's spec is updated in the same release.

## Command set

| Command                                   | v | Does                                                                                                                                 |
| ----------------------------------------- | - | ------------------------------------------------------------------------------------------------------------------------------------ |
| `tops init`                               | 1 | Writes a commented starter spec and `secrets.env` template                                                                           |
| `tops validate`                           | 1 | Offline: schema, immutables present, shard headroom with one node down vs profile limit, secrets file mode                           |
| `tops bootstrap`                          | 1 | New cluster: preflight every node, refuse if the datastore already holds a Temporal cluster, create databases and schema, apply all nodes, create namespaces, print LB and monitoring steps |
| `tops plan [--node N]`                    | 1 | Per node: changed files (diff, secrets masked), version, firewall per port (+ add, - remove), schema, restart or hot reload; cluster: namespaces, unknown ring members. Exit 0 no change, 2 changes |
| `tops apply [--node N] [-y]`              | 1 | Shows the plan, confirms, snapshots, applies only what changed, restarts with the ring gate, stops at the first failure              |
| `tops status [--json]`                    | 1 | Per node: service, version, frontend SERVING, ring, shards owned, memory vs MemoryHigh, clock, firewall. Cluster: shard sum vs spec, schema version, datastore reachable |
| `tops node add <name>`                    | 1 | Joins a node already in the spec: firewall on existing nodes first, then the new node; reports shard movement and time to ring N of N |
| `tops node remove <name> [-y]`            | 1 | Refuses unless the rest can hold the shards; reminds to leave the LB pool; drains, stops and disables Temporal, waits for full shard ownership elsewhere; then the operator deletes it from the spec and runs `apply` |
| `tops restart <node>\|--all [-y]`         | 1 | Safe restart: refuses unless every other node is in the ring; waits for SERVING, ring N of N and shards; `--all` is rolling          |
| `tops upgrade <version> [-y]`             | 1 | One minor at a time. Confirms a datastore backup exists, stages on all nodes, migrates the schema from one node, rolling restart, writes the version to the spec only after success |
| `tops rollback [list\|<apply-id>] [-y]`   | 1 | Restores the snapshot taken before an apply on every node it changed, one node at a time with the ring gate; saves what it replaces, so it is undoable. Binary rolls back, schema never does. No argument = `list` (assumes Q7) |
| `tops top [<node>\|--all] [-w N]`         | 1 | cgroup CPU, memory vs MemoryHigh/Max, Go heap vs GOMEMLIMIT, pressure, OOM kills, restarts                                            |
| `tops audit [-v] [--window 15s]`          | 1 | Samples every node's `:9091` and `:9100` twice; one line per layer (servers, frontend, history, matching, workers, database, errors), says which is choking and what to do |
| `tops logs [<node>\|--all] [-f -e -q -g -t -s -n]` | 1 | Readable, merged by time across nodes; follow, errors (`--warn` adds warnings), hide task-queue chatter, grep, since, summary per message, last N lines |
| `tops investigate <ui-url>\|<ns> <wf> [run]` | 1 | Workflow report: describe, history summary, pending activities, task queue pollers, related log lines; written to a file and printed |
| `tops ns [list\|ensure]`                  | 1 | Namespaces: retention, running workflows, managed by the spec or not; `ensure` applies the spec under the cluster lock (assumes Q8) |
| `tops profile <node> [--seconds 30]`      | 1 | CPU profile from the node's loopback pprof port; prints CPU share per role (frontend, history, matching, worker) inside the all-in-one process and saves the raw profile |
| `tops targets`                            | 1 | Prometheus file-SD JSON from the spec (temporal :9091, node :9100, labels `cluster`, `node`, and in v2 `role`)                     |
| `tops version`, `tops completion`         | 1 | Version of tops and of the helper on each node; shell completions                                                                    |
| Role-aware forms of the above             | 2 | `--role R` filter on node-scoped commands; ring gate per role; `status` per role; audit per role host; Cassandra and ES bootstrap, schema and health |

`--json` on: `validate`, `plan`, `apply`, `status`, `node add/remove`, `restart`, `upgrade`, `rollback`,
`top`, `audit`, `profile`, `ns`, `targets`, `version`. Not on `logs` (use `--raw` for the journal's JSON) or
`investigate` (the report is a document). JSON shapes are documented in backend-schema.md.

## Safety

| Rule                                                                                                     | Default behaviour               |
| -------------------------------------------------------------------------------------------------------- | ------------------------------- |
| Every mutating command prints the plan and asks `apply to <cluster> (<nodes>)? [y/N]`                    | `-y` skips; no TTY without `-y` fails |
| `plan` is the dry run. There is no separate `--dry-run` flag                                              | Exit 2 means "would change"     |
| Restart only when every other node is in the ring and all shards are owned                               | Refuse                          |
| Stop the rollout at the first node that is not SERVING, ring N of N, shards > 0 within the timeout        | Refuse to continue; print rollback |
| Immutables differ from the live cluster: `cluster`, `shards`, persistence kind; v2 also `cassandra.keyspace`, `.datacenter`, `.replicationFactor` and `elasticsearch.index` | Refuse: "build a new cluster"   |
| A persistence address change (`postgres.host/port`, `cassandra.hosts`, `elasticsearch.url`) points at a store that does not hold the live cluster (`cluster_id` in `cluster_metadata_info`; for ES, the index) | Refuse (`refused.store_identity`) |
| Profile larger than the host; shards per host with one node down above the profile limit                  | Refuse (bash only warned)       |
| Version change through `apply`                                                                           | Refuse: use `tops upgrade`      |
| Upgrade skips a minor version or downgrades past the schema                                              | Refuse                          |
| A ring member is not in the spec                                                                         | Refuse to apply; name it        |
| `node remove` would leave survivors above the profile's shard limit                                      | Refuse                          |
| Another tops run holds the cluster lock (per-node lock taken on all nodes before any change)             | Refuse; show holder and age. A run that lost its lease is fenced: the helper refuses its writes |
| Live file differs from the last applied render (hand edit)                                               | Shown as drift in `plan`; `apply` overwrites after confirmation |
| `--force` overrides the ring gate only, and only after typing the node name                               | Never with `-y`                 |
| PostgreSQL `max_connections` below `Σ nodes × roles per process × (dbMaxConns + dbVisibilityMaxConns) + 10` | Warn in `plan`, `bootstrap`, `node add` (the datastore is not tops') |

Every apply writes a record on each changed node (`/var/lib/tops/history/<apply-id>.json`: who, when,
tops version, spec hash, changes, result) and keeps the newest 10 config snapshots.

## Decisions

| #   | Decision                                                                                       | Why                                                                                                                         |
| --- | ---------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------- |
| D1  | Workstation-driven over SSH from one spec; node helper is the same binary                      | See Operating model                                                                                                         |
| D2  | No state file; reality is read from nodes and Temporal each run                                | Nothing to lose, lock or corrupt. Ring membership and per-node history cover what a state file would hold                  |
| D3  | Templates embedded in tops; cluster repos hold only spec, secrets, dynamic config               | Validation and safety need to know what each file means. A generic file pusher is Ansible                                   |
| D4  | Temporal and metrics reached through SSH forwarding to loopback                                 | Port 22 only. `audit` keeps working after the workstation leaves `MONITOR_IPS` (temporal-node-scale M3)                     |
| D5  | `apply` and `upgrade` are separate                                                             | A version change has a different order (schema first) and a different risk; one confirmation must not cover both           |
| D6  | v1 = what temporal-node-scale needs; v2 = role topology + Cassandra/ES, committed              | Grow in layers. v2 is built against temporal-role-scale's lab, which does not exist before v1 ships                         |
| D7  | v2 changes the spec without a compatibility layer                                              | AGENTS.md. One user (temporal-node-scale) is updated in the same release                                                     |
| D8  | tops does not deploy Prometheus/Grafana; it emits scrape targets and needs neither              | Dashboards and alert thresholds are per-cluster content that the cluster repos iterate on. `audit` reads endpoints directly, so tops works before monitoring exists |
| D9  | SSH user has passwordless sudo; the helper runs as root (assumes Q12)                           | Anyone who can upload and run the helper is root anyway; a restricted sudoers rule would be an audit point, not a boundary. Every run is recorded per node |
| D10 | Cobra for commands, `x/crypto/ssh`, Temporal Go API/SDK, Prometheus `expfmt`; no custom parsers | Established libraries (AGENTS.md). Details in the TRD                                                                        |
| D11 | Releases with GoReleaser: linux amd64/arm64, darwin arm64, checksums, SBOM, GitHub attestations | Versioned, verifiable binaries; the helper for a node's arch is taken from the matching release asset and checksum-verified |
| D12 | Semver: v1.x for node-scale, v2.0 introduces roles                                             | The spec change is breaking; the major says so                                                                              |
| D13 | Each tops release supports two Temporal minors (current and previous); tops 1.x: 1.30, 1.31 (assumes Q11) | Upgrades are tested from previous to current; anything else is untested and refused                                         |
| D14 | Web UI from the `temporalio/ui-server` release binary under systemd (Q2)                        | No Docker on Temporal nodes; one install mechanism for everything tops runs                                                  |
| D15 | tops logs its own runs as JSON (`slog`) under the user's state dir; no Prometheus endpoint     | A CLI has no process to scrape. Per-node apply history is the audit trail                                                   |

## Milestones

Each milestone ends with a release that works end to end on the temporal-node-scale lab
(`temporalScale/lab`, 10.78.0.0/24). The lab itself, and its host list, belong to temporal-node-scale.

| #  | Scope                                                                                                                       | Acceptance (all on the node-scale lab)                                                                                                                       | Effort  |
| -- | --------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------ | ------- |
| T1 | Repo, CI (lint, race tests, coverage), release pipeline; spec + `validate`; SSH + helper upload; read-only: `status`, `top`, `logs`, `audit`, `investigate`, `ns list`, `targets`, `version` | Against the bash-deployed 2-node lab: `tops status --json` shard sum = 512 and ring 2 of 2; `audit` names the busiest layer under omes traffic started by hand; release v0.1.0 installs with `go install` | 2-3 wk |
| T2 | Rendering, `plan`, `apply`, snapshots, `rollback`, `restart`, cluster lock, `ns ensure`                                     | First `apply` adopts the bash-deployed cluster; second `plan` exits 0; a dynamic-config change applies without restart; a static change restarts one node at a time; killing 02 mid-apply stops the rollout; `rollback` and its undo both pass `status` | 3 wk |
| T3 | `bootstrap`, `node add`, `node remove`, firewall, nginx, node_exporter, UI                                                  | `make reset && make up && tops bootstrap` gives a healthy 2-node cluster with UI; `node remove tscale-02` then `node add tscale-02` round-trips; every handover dip below 512 owned recovers within 10 s | 2 wk |
| T4 | `upgrade` with schema migration                                                                                              | Bootstrap at the previous minor, `tops upgrade 1.31.3`: schema migrated once, rolling restart, handover dips below 512 recover within 10 s; refusal of a two-minor jump is tested | 1-2 wk |
| T5 | `profile` (per-role CPU share); v1.0.0: every safety refusal has an e2e test, docs, completions, README                      | `tops profile tscale-01` under load attributes >= 90% of samples to a role or to shared runtime; full e2e suite green; coverage targets met; temporal-node-scale M1 closes on this release | 1-2 wk |
| T6 | v2.0.0: `roles` in the spec (one process per node with its role set), per-node profiles incl. size-named lab profiles, role-ordered bootstrap and per-role ring gate (rings read from frontend hosts), status (Cassandra/ES health), audit and targets (`role` label), Cassandra keyspace/schema, ES index/schema, persistence identity fixed, UI on a `roles: []` host | On temporal-role-scale's lab: bootstrap a split cluster; per-role restart gate; a frontend-only profile change restarts frontends only; persistence kind and keyspace changes refused; temporal-node-scale spec migrated and its lab still green | 4-5 wk |

T6 depends on temporal-role-scale R0 (its lab, 10.80.0.0/24). That lab is role-scale's deliverable, not tops'.
The full list of what role-scale needs is in its PRD ("What this project needs from tops"). Its gaps N1-N7
are designed here:

| #  | Gap (role-scale TRD)                                   | Design                                                                                       | Where                                    |
| -- | ------------------------------------------------------ | -------------------------------------------------------------------------------------------- | ---------------------------------------- |
| N1 | Keyspace, DC, RF, ES index must be fixed               | Fixed fields, checked against the store itself; any address change must reach a store that holds the live `cluster_id` | Safety above; TRD "Persistence identity" |
| N2 | Bootstrap order and first-start gate for roles         | Role stages frontend → history → matching → worker; rings are read from frontend hosts only   | TRD "Ring gate"                          |
| N3 | Profile memory below 1 GB                              | `memoryMiB` (v1 already); fit check against the host's effective limit (cgroup of PID 1)      | backend-schema "Profiles"                |
| N4 | A frontend-only restart-class change                   | Size-named lab profiles (`lab-256m`, `lab-384m`, `lab-768m`); R-D10 moves frontends between two | backend-schema "Profiles"                |
| N5 | v2 `status --json` for Cassandra and ES                 | `datastore.cassandra` (hosts up, quorum, replication) and `datastore.elasticsearch` (status, index) | backend-schema "`--json` outputs"     |
| N6 | `targets` and `/health` on a `roles: []` host           | No targets, no node_exporter, no `/health`; nginx serves the UI only. `/health` only on frontend hosts | TRD "T6 extension points"           |
| N7 | Peers rule and `roles: []` hosts                       | Peers = nodes with roles; a `roles: []` host gets no firewall chain                            | TRD "T6 extension points"                |

## Testing strategy

| Layer        | What                                                                                                                                                       |
| ------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Unit         | Table-driven: spec validation, rendering (golden files per profile and per node), diff and change classification (restart vs hot reload vs firewall only), plan ordering, every safety rule against a fake cluster interface, audit math on recorded metric fixtures, log line formatting, UI URL parsing |
| Helper       | Node operations tested in a systemd container (same image as the lab) through the real SSH path                                                             |
| Rendered config | Each render validated with the target `temporal-server` binary on the node before anything is switched (kept from the scripts)                         |
| End to end   | `make e2e LAB=<path to temporalScale>`: reset lab, bootstrap, status, change, rolling apply, mid-apply failure, rollback, node remove/add, upgrade, each refusal. Required before every release; results attached to the release |
| Static       | `gofmt`, `golangci-lint`, `go test -race`, `govulncheck` in CI                                                                                             |

CI runs unit, helper and static checks. Running the lab in CI is Q4.

## Challenged assumptions

| Assumption                                        | Challenge                                                                                                     | Resolution                                                                    |
| ------------------------------------------------- | ------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------- |
| "Rewrite tadm in Go"                              | A port keeps tadm's main limit: one node at a time                                                            | Keep its ideas, change the model (D1)                                         |
| "Production grade means an agent"                 | An agent adds a service to secure and upgrade on every node for one operator                                  | SSH + helper, no daemon                                                       |
| "Restricted sudo is safer"                        | The helper is uploaded by the same user; root through it is root                                              | D9; say so in the security section of the TRD                                 |
| "tops should own monitoring too"                  | Then tops ships Grafana dashboards, and every dashboard tweak is a tops release                              | D8                                                                            |
| "Support roles from day one"                      | Nothing to test it on until temporal-role-scale has a lab; designing it blind is speculative                  | v2, committed, built against the real lab                                     |
| "Declarative means `apply` handles node removal"  | Removing a node from the spec removes its SSH address; tops could not drain it                                 | `node remove` while it is still in the spec, then edit the spec               |
| "A `--dry-run` flag on everything"                | Two ways to preview drift apart                                                                               | `plan` is the preview; `apply` shows the same plan before confirming          |
| "tops can back up the database"                   | Backups belong to the datastore and its HA setup                                                              | `upgrade` requires a confirmation, not a backup feature                       |

## Risks

| #  | Risk                                                                                             | L      | I      | Mitigation                                                                                       |
| -- | ------------------------------------------------------------------------------------------------ | ------ | ------ | ------------------------------------------------------------------------------------------------ |
| R1 | Scope creep into Ansible/Terraform (users, packages, Docker, provisioning)                       | High   | High   | Scope boundary table is the contract; anything outside it is a non-goal unless the PRD changes  |
| R2 | SSH/sudo design: host-key handling, agent forwarding, sudo prompts on a fresh host              | Medium | High   | Strict `known_hosts`, no agent forwarding, preflight checks `sudo -n true`; refuse otherwise     |
| R3 | Workstation dies mid-apply                                                                        | Medium | Medium | Node-by-node: at most one node mid-change; the lock shows the interrupted apply; `plan` resumes it |
| R4 | Rendered config drifts from what Temporal accepts across versions                               | Medium | High   | Validation with the target binary before switching; two supported minors only (D13)              |
| R5 | Metric names change between Temporal versions and break `audit`                                 | Medium | Medium | Fixtures recorded per supported minor; audit says "metric missing" instead of "fine"             |
| R6 | v2 rework is larger than planned because v1 hard-codes all-in-one                               | Medium | Medium | v1 keeps topology in one package (node → services) so v2 changes one seam, without adding roles early |
| R7 | e2e depends on another repo's lab                                                                 | Certain| Low    | The lab path is a parameter; the harness checks the lab version it expects                      |
| R8 | Plain gRPC on 7233 means any client IP is admin                                                  | Certain| Medium | Unchanged from today; tops never widens `access.clients`; mTLS deferred                          |
| R9 | One maintainer: e2e suite rots                                                                    | Medium | Medium | Release blocked without a green e2e record                                                       |

## Success metrics

| Metric                                                                  | Target                                  |
| ----------------------------------------------------------------------- | --------------------------------------- |
| Bash scripts left in temporal-node-scale                                | 0                                       |
| Fresh lab to a healthy 2-node cluster (`make up` excluded)              | One `tops bootstrap`, under 5 min       |
| No-op `tops plan` on 3 nodes                                            | Under 10 s                              |
| Rolling restart of 3 nodes with no unowned shards after each step       | 100% of e2e runs                        |
| Safety refusals covered by e2e tests                                    | All rules in the Safety table           |
| Unit coverage                                                           | >= 80% overall, >= 90% safety and diff  |
| temporal-role-scale operated with tops v2 only                          | Yes, no scripts in that repo            |

## Deferred

| Item                                       | Why later                                                         |
| ------------------------------------------ | ----------------------------------------------------------------- |
| mTLS on 7233 and for tops' Temporal calls  | Before outside clients; changes the security model of all projects |
| Secrets via sops or a vault                | Q3                                                                |
| Lab e2e in CI                              | Q4                                                                |
| LB pool automation (Cloudflare API)        | Provider-specific; printed as a manual step                       |
| Nodes other than Ubuntu 24.04              | No second target to test on                                       |

## Open questions

| #  | Question                                                                                         | Recommendation                                                                                                          |
| -- | ------------------------------------------------------------------------------------------------ | ----------------------------------------------------------------------------------------------------------------------- |
| Q1 | Is "workstation-driven over SSH, no agent, no state file" the model you want?                    | Yes (D1, D2). Revisit only if a second operator or scheduled runs appear                                               |
| Q2 | Web UI: `ui-server` release binary under systemd, or keep the container?                         | Binary, if the release ships one for linux/amd64 (verify in T3). Removes Docker from Temporal nodes                     |
| Q3 | Secrets: an untracked 0600 `secrets.env`, or sops-encrypted in git?                              | `secrets.env` for v1; sops when a second machine runs tops                                                             |
| Q4 | Run the lab e2e in GitHub Actions (privileged systemd containers on hosted runners)?             | Not for v1; local e2e is a release gate. Try it after v1.0 as a portfolio item                                          |
| Q5 | Should `node add` also create the lab container?                                                 | No. The lab is temporal-node-scale's; tops must not know it talks to containers                                        |
| Q6 | Name: is `tops` free enough (it collides with generic "top"-like tools)?                         | Keep `tops`; module path makes it unique                                                                                |
| Q7 | `tops rollback` with no id: (a) print the list and change nothing, or (b) roll back the newest apply, as tadm did? | (a). A rollback restarts nodes; it must name what it restores. Departs from tadm |
| Q8 | Does `ns ensure` take the cluster lock? (a) yes, like every mutating command; (b) no, the namespace API is idempotent | (a): it cannot interleave with an `apply` that also ensures namespaces |
| Q9 | From T3, does tops own `/etc/temporal` and `/etc/temporal-ui` entirely (unknown files shown as removals), or only the files it renders? | Entirely: a stray file in the config dir is drift; the bash `profile.yaml` is removed this way |
| Q10 | A node stopped by `node remove` but still in the spec: `apply` (a) refuses, (b) starts it again, (c) skips it with a warning? | (a) `refused.node_stopped`: the operator finishes the removal or runs `node add`; (b) would undo a drain silently |
| Q11 | tops 1.x supports Temporal 1.30 and 1.31 although 1.32.1 is released. Keep, or move to 1.31/1.32? | Keep for 1.x: both cluster labs stay on 1.31.3 for comparable runs, and T4 tests 1.30 → 1.31. Move to 1.31/1.32 in v2.0 |
| Q12 | Passwordless sudo for the SSH user (D9), or a sudoers rule limited to the helper path? | Full NOPASSWD: the helper is uploaded by the same user, so a path rule is no boundary; every run leaves a history record |
