# tops PRD

Project: tops (Temporal Ops), `github.com/2SSK/tops`. Status: draft. Date: 2026-10-23.
Sibling docs: [TRD](trd.md) · [App flow](app-flow.md) · [Design brief](design-brief.md) ·
[Backend schema](backend-schema.md) · [Implementation plan](implementation-plan.md) · [Concepts](concepts.md) ·
[Roadmap](roadmap.md)

Related project: temporalScale (`temporalScale/docs/prd.md`), the fleet repo tops manages: its environments,
monitoring, drills and measured evidence. tops is the tool (Terraform + Helm for bare-metal Temporal);
temporalScale is the infra repo it runs against. tops owns every spec field, command, flag, `--json` path,
port and milestone id; temporalScale uses them as defined here.

## Summary

`tops` is one Go binary that deploys, changes and investigates bare-metal Temporal clusters from the
operator's workstation over SSH. A fleet repo holds one directory per environment; each describes one
complete cluster. Every node lists the Temporal roles it runs, and each role runs as its own systemd unit,
sized from the node's detected CPU and memory. `tops -e <env> plan` shows what would change before anything
is copied; `tops -e <env> apply` changes it one role on one node at a time, behind a per-role health and ring
gate, under a cluster lock.

| Version | Supports                                                                                                   |
| ------- | ---------------------------------------------------------------------------------------------------------- |
| v1      | Roles per node (any subset of frontend, history, matching, worker, plus the Web UI), one unit per role, per-node sizing, environments with promotion and drift, PostgreSQL |
| v2      | Cassandra + Elasticsearch persistence, with the persistence-identity guard. Nothing else                   |

## Problem

| Problem                                                                                                   | Consequence today                                                          |
| --------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------- |
| Self-hosted Temporal on plain servers is run with shell scripts. A bash tool the author operated proved the operating ideas below, and also their limits | Each limit below is a goal in "Headline"                                    |
| The scripts must copy their checkout to every node before they can show a diff                            | You learn what a change does only after it is on the node                  |
| New host keys are accepted on first contact; there is no lock between two operators                       | A spoofed node or a second operator can corrupt a rollout                  |
| The cluster is described by a space-separated file plus `.env` files                                     | A typo is found on the node, at run time, without a line number            |
| Output is coloured text only                                                                              | A drill cannot assert "512 shards owned" without scraping                  |
| Settings knowledge lives in people's heads: which of ~570 dynamic-config keys a role reads, whether it reloads live, what its default is | Restarts nobody planned, or changes that silently never apply          |
| Memory and connection limits are set by hand per host                                                     | A host resize or a role moved to another host silently invalidates them    |

## Personas

| Persona                         | Context                                                                                     | Goals                                                                         | Pain points tops removes                                             |
| ------------------------------- | ------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------- | -------------------------------------------------------------------- |
| **Operator** (primary; the author) | Runs lab, staging and production Temporal from one fleet checkout on a Linux or macOS workstation; on call for it | Build a cluster, change it safely, promote proven changes, upgrade, find the busy layer in an incident | Diff-after-copy, hand-sized limits, unknown hot/restart behaviour, no lock |
| **Drill and test script** (machine user) | temporalScale's drills, `make drift-check`, CI jobs, tops' own e2e suite             | Stable `--json` documents and exit codes to assert on                         | Parsing coloured text                                                |
| **Portfolio reviewer** (secondary; hiring manager, senior engineer) | Reads the README and the code for 5-30 minutes, may run the lab quickstart | See a real Go codebase: clear scope, tests that mean something, releases, safety thinking | Toy projects with no tests, no failure handling, no measured claims  |

## Goals

### Headline: what tops does better than its bash predecessor

These are the portfolio's claims. Each has a test that proves it.

| #  | Predecessor                                    | tops                                                                                 | Measured by                                                                                     |
| -- | ---------------------------------------------- | ------------------------------------------------------------------------------------ | ----------------------------------------------------------------------------------------------- |
| H1 | Pushes its checkout, then diffs on the node    | `plan` computes the diff from the spec and live reads before any Temporal file is copied | e2e `TestPlanTouchesNothing`: sha256 of `/etc/temporal`, `/opt/temporal`, `/etc/systemd/system/temporal*` and `/var/lib/tops` identical before and after `plan`; `/run/tops` empty after. The helper binary is the only thing `plan` may install |
| H2 | rsync'd bash on every node                     | One static binary; typed helper ops over SSH; no checkout or shell scripts on nodes  | `CGO_ENABLED=0` release binary; a unit test pins the only shell string tops sends (the helper upload); e2e: nothing of tops on a node outside `/usr/local/lib/tops` and `/var/lib/tops` |
| H3 | Accepts new host keys on first contact         | Strict host keys, never trust on first use                                            | e2e: a changed host key exits 1 with `ssh.host_key_changed`; a test fails the build if `InsecureIgnoreHostKey` appears |
| H4 | No lock                                        | Cluster lock with leases and fencing: two operators cannot change one environment at once | e2e: two concurrent `apply`, one exits 3 `lock.held`; a run past its lease gets `lock.lost` on its next write |
| H5 | Space-separated file + `.env`                  | Typed `cluster.yaml`, strict schema, every error names the field and line           | Every validation problem carries `path` and `line`; golden test over the bad-spec fixtures       |
| H6 | Text only, linter only                         | `--json` everywhere; unit, golden and e2e tests; CI; versioned, verifiable releases   | JSON golden per command; coverage >= 80% (`safety`, `plan`, `sizing` >= 90%); tagged releases with checksums, SBOM, provenance and an attached e2e report |
| H7 | One environment at a time, by hand             | Environment diff, promotion and drift detection                                       | e2e: `tops diff lab staging` exits 2; `tops promote lab staging -y` then `diff` exits 0; a hand edit on a node makes `tops drift` exit 2 |
| H8 | PostgreSQL only                                | v2: Cassandra + Elasticsearch with the persistence-identity guard                      | v2.0.0 acceptance: keyspace, DC, RF and index changes refused against the store's metadata      |

### Kept ideas, re-derived independently

| #  | Goal                                                                                 | Measured by                                                                                     |
| -- | ------------------------------------------------------------------------------------ | ----------------------------------------------------------------------------------------------- |
| G1 | Per-node sizing from detected CPU/memory (cgroup-aware) and the roles a node runs; constants measured on VPSLAB by the author | `tops capacity` matches the formula on two lab node sizes; no OOM kill under the reference load (T8) |
| G2 | Settings catalog generated from Temporal's own source, with search; hot vs restart classified with evidence | `tops knobs` lists every key of each supported minor; generator re-run is byte-stable          |
| G3 | Never make a cluster less available than the operator asked for                       | Each refusal in the app-flow index has an e2e test                                              |
| G4 | Rolling change one role on one node at a time, with per-role gates; rollback          | e2e: a restart-class change restarts each affected role once, in order, shards owned throughout |
| G5 | Incident tools: status, top, logs, audit per layer, investigate from a UI URL, pprof per role, namespace ensure, Prometheus targets | Acceptance rows of T3 and T5                                              |
| G6 | Installable and small                                                                 | `go install` works; binary < 40 MB (CI gate 35 MB)                                              |
| G7 | Scheduled apply and foreground run of a role (v1.1)                                   | Acceptance rows of T9                                                                           |

## Non-goals

| Not doing                                                         | Why                                                                                         |
| ----------------------------------------------------------------- | ------------------------------------------------------------------------------------------- |
| Provisioning machines, networks, DNS, load balancers              | Terraform or the provider. tops starts from "Ubuntu 24.04 host reachable by SSH with sudo"  |
| General host configuration (users, SSH hardening, Docker)         | Ansible territory. tops touches only what a Temporal node needs ("Scope boundary")          |
| Installing or operating PostgreSQL, Cassandra or Elasticsearch    | External datastores. tops creates Temporal's databases, keyspace, schema and index in them and reads their health |
| Deploying Prometheus and Grafana                                  | The fleet repo owns monitoring as code; tops emits scrape targets                           |
| A control plane, API server, or a stateful agent with its own API | Nothing runs between tops invocations (D1)                                                  |
| A plugin system or a templating language                          | Base + overrides is a plain YAML merge; templates are embedded and versioned with tops      |
| Multi-tenancy, a Temporal Cloud-like service                      | One operator, one cluster per environment                                                   |
| Kubernetes                                                        | The official Helm chart exists; this project is about what the chart hides                  |
| Multi-cluster replication, archival, mTLS                         | Separate topics; mTLS deferred                                                              |
| Datastore backups                                                 | The datastore's tooling. `tops upgrade` asks for confirmation that one exists               |
| Node OSes other than Ubuntu 24.04; Windows workstations           | One target, tested. Workstation: Linux and macOS                                            |

## Operating model (D1)

| Option                                                      | For                                          | Against                                                                 | Verdict |
| ----------------------------------------------------------- | -------------------------------------------- | ----------------------------------------------------------------------- | ------- |
| A. Copy the tool and config to every node, run it there      | No remote execution                          | Diff only after copying; the human orchestrates; config copied everywhere | No      |
| B. Workstation-driven over SSH from one declarative spec     | Cluster-wide plan before any copy; nothing runs between calls; port 22 only | SSH and sudo design matter; a long apply needs the workstation up | **Yes** |
| C. Agent on every node + control plane                       | Continuous reconciliation                    | Another service to secure, upgrade and monitor; far beyond one operator | No      |
| D. Generate Ansible playbooks                                | Established executor                         | The Temporal-aware logic still has to be written; two tools              | No      |

How B works:

1. The workstation finds the fleet root, loads `environments/<env>/` plus the shared `base/`, and renders
   every node's files in memory, per role.
2. On each node it runs the **node helper**: the same `tops` binary for linux, uploaded to
   `/usr/local/lib/tops/<sha16>/tops` when missing, invoked as `sudo -n <path> node <op>`. The helper does
   host work in Go and returns typed JSON. No shell output is parsed.
3. Temporal APIs, metrics and pprof are reached through SSH-forwarded connections to the node's loopback.
4. Mutating commands lock the environment first, then plan, confirm and execute: stage in parallel, write
   hot changes in parallel, restart serially (one role on one node, gate, next).

There is no state file. Truth is the spec (intent) plus what nodes and Temporal report (reality). Each node
keeps a small record of what tops last wrote there (`/var/lib/tops`), so a hand edit can be told from a spec
change.

## Environments (D16)

| Rule                                                                                                   |
| ------------------------------------------------------------------------------------------------------ |
| A fleet repo has `base/` (dynamic config shared by every environment: one file per role plus `common.yaml`) and `environments/<env>/` |
| `environments/<env>/cluster.yaml` is a complete, self-contained cluster: name, nodes and roles, versions, persistence, access, namespaces |
| `environments/<env>/overrides.yaml` holds only the dynamic-config keys that differ in that environment   |
| `environments/<env>/secrets.env` is untracked, mode 0600                                               |
| Plain YAML merge for settings only: base, then the environment's overrides. No templating               |
| Environments are separate clusters: own cluster name, nodes, persistence and secrets; nothing shared at runtime |
| `tops -e <env> <command>` selects one; there is no default environment and no environment variable for it |

Full layout and merge order: [backend-schema.md](backend-schema.md#fleet-repo-layout).

## Scope boundary: what tops manages on a node

| Managed by tops                                                                                     | Not managed                                     |
| --------------------------------------------------------------------------------------------------- | ----------------------------------------------- |
| `temporal` system user, `/opt/temporal/releases/<v>`, `current`, Temporal CLI, schema tools         | Other users, SSH config, kernel tuning          |
| `/etc/temporal/*`: per-role server config, env, dynamic config; shared secrets env                   | Application workers and their hosts             |
| `temporal@.service` template, one enabled instance per role, per-role limits drop-in                | Other systemd units                             |
| Firewall chain `TEMPORAL_INPUT` and its boot unit (T6)                                               | Any other iptables chain                        |
| nginx site for `/health` and the UI; node_exporter; journald cap (T6)                                | Prometheus, Grafana, Alertmanager               |
| Web UI (`ui-server` under systemd) on nodes that list `ui` (T6)                                      | Load balancer pool membership (printed step)    |
| Temporal databases and their schema                                                                  | The datastore servers                           |
| `/usr/local/lib/tops`, `/var/lib/tops`, `/run/tops`                                                  | Anything else on the host                       |

## Spec (shape; full schema in backend-schema.md)

The example is tops' own e2e fixture (`test/e2e/fleet/environments/lab/cluster.yaml`), whose mixed shape
exercises role subsets and pins. temporalScale's `lab` environment (`tscale-lab`, three all-in-one nodes)
runs on the same VPSLAB containers; the two never run at once (D22).

```yaml
cluster: tops-e2e                   # Temporal cluster name; fixed after bootstrap
temporal: { version: 1.31.3, cli: 1.9.1 }
shards: 512                         # fixed after bootstrap
persistence:
  postgres: { host: 10.80.0.20, port: 5432, user: temporal }
ssh: { user: deploy, key: ~/Code/Projects/building/tops/lab/keys/id_ed25519,
       knownHosts: ~/Code/Projects/building/tops/lab/keys/known_hosts }
access: { clients: [10.80.0.1], monitors: [10.80.0.1], admins: [10.80.0.1] }
namespaces: [ { name: default, retention: 72h } ]
ui: { version: 2.54.1 }             # from T6
nodes:
  - { name: vps-01, address: 10.80.0.11, roles: [frontend, history, matching, worker, ui] }  # ui from T6
  - { name: vps-02, address: 10.80.0.12, roles: [frontend, history, matching, worker] }
  - { name: vps-03, address: 10.80.0.13, roles: [frontend, matching],
      pin: { matching: { memoryMax: 512M } } }                                               # pin from T2
```

Fields enter the schema with the milestone that implements them (D24): `roles` in Phase 0, `pin` in T2.4,
the `ui` role and `ui.*` in T6.2.

## Command set

| Command                                          | Rel.  | Does                                                                                                   |
| ------------------------------------------------ | ----- | ------------------------------------------------------------------------------------------------------ |
| `tops -e <env> validate`                         | 0.1   | Offline: schema, roles, every dynamic-config key against the catalog, secrets mode and keys           |
| `tops knobs [text] [-e env] [-v]`                | 0.1   | Search the settings catalog: key, roles, hot/restart/unknown, default, meaning; with `-e`, the value per layer |
| `tops diff <envA> <envB> [-v]`                   | 0.1   | Offline: promotable differences (CLI/UI versions, log level, namespaces, overrides) and, with `-v`, by-design ones. Exit 0 none, 2 some |
| `tops init <env>`                                | 0.1   | Creates `environments/<env>/` (commented `cluster.yaml`, empty `overrides.yaml`, 0600 `secrets.env`) and missing `base/` files |
| `tops -e <env> capacity`                         | 0.2   | Per node: detected cores and memory with their source, per-role limits and pools, shard headroom, DB connection budget |
| `tops -e <env> plan [--node N]`                  | 0.2   | Per node and role: file diffs, hot or restart, role starts/stops; cluster: namespaces, unknown members. Exit 0 / 2 / 3 |
| `tops -e <env> bootstrap`                        | 0.3   | New cluster: preflight, schema, stage, write, start roles in stages with gates, namespaces; resumable  |
| `tops -e <env> status [--role R]`                | 0.3   | Per node and role: unit, version, SERVING, ring, shards, memory vs limit; cluster: shard sum, rings, schema. Exit 0 / 5 |
| `tops -e <env> ns [list\|ensure]`                | 0.3   | Namespaces vs the spec; `ensure` creates and updates, never deletes                                     |
| `tops -e <env> apply [--node N]`                 | 0.4   | Lock, plan, confirm, snapshot; hot changes in parallel; restarts one role on one node at a time        |
| `tops -e <env> restart <role>\|--all [--node N]` | 0.4   | Safe restart of a role, rolling, behind its gate                                                        |
| `tops -e <env> rollback [list\|<apply-id>]`      | 0.4   | Restores a snapshot, rolling with gates; undoable                                                      |
| `tops -e <env> drift`                            | 0.4   | Changes made behind tops' back: hand-edited files, units in the wrong state, unknown ring members. Exit 0 / 2 / 5 |
| `tops promote <from> <to> [--only G] [--partial]` | 0.4  | Writes the promotable values of `<from>` into `<to>`'s files; never identity fields; never contacts nodes |
| `tops -e <env> top [--node N] [--role R] [-w N]` | 0.5   | Per role: cgroup CPU, memory vs MemoryHigh/Max, Go heap vs GOMEMLIMIT, GOMAXPROCS, pressure, OOM kills |
| `tops -e <env> logs [...]`                       | 0.5   | Per-role journals merged by time; errors, grep, since, summary, follow                                 |
| `tops -e <env> audit [-v] [--window D]`          | 0.5   | Two scrapes of every role's metrics; one line per layer; names the first one choking                    |
| `tops -e <env> pprof <role> --node N [cpu\|heap]` | 0.5  | Saves a profile from that role's loopback pprof port                                                    |
| `tops -e <env> investigate <ui-url>`             | 0.5   | Workflow report: describe, history summary, pending activities, pollers, related log lines             |
| `tops -e <env> targets [--job J]`                | 0.5   | Prometheus file-SD from the spec, one target per role with a `role` label                              |
| `tops -e <env> node add\|remove <node>`          | 0.6   | Join a node in the spec / drain and stop a node before it leaves the spec                              |
| `tops -e <env> upgrade <version>`                | 0.7   | One minor at a time: stage, migrate schema once, rolling restart, write the version back               |
| `tops version [-e env]`, `tops completion <shell>` | 0.1 | Version (with `-e`: the helper on each node, from 0.2); shell completions                               |
| `tops -e <env> apply --at <HH:MM>`               | 1.1   | Scheduled apply of a confirmed plan; refused at run time if the cluster changed since                  |
| `tops -e <env> run <role> --node N`              | 1.1   | Runs a stopped role in the foreground on its node, output streamed, for a role that will not start     |
| Cassandra + Elasticsearch                        | 2.0   | Keyspace, schema, index, health in `status`, identity guard                                            |

Adding or removing a role on a node is an edit of `nodes[].roles` followed by `apply`. `--json` on every
command except `logs` (`--raw` gives NDJSON instead), `investigate` (the report is a document) and
`completion`; there it is a usage error.

## Safety

| Rule                                                                                                   | Behaviour                         |
| ------------------------------------------------------------------------------------------------------ | --------------------------------- |
| Every mutating command takes the lock, prints the plan and asks `apply to lab (tops-e2e: vps-01, vps-02, vps-03)? [y/N]` | `-y` skips; no TTY without `-y` refuses |
| `plan` is the dry run                                                                                   | Exit 2 means "would change"       |
| Restart role R on node X only when every other member of R's ring is in it (in every frontend's view) and, for history, all shards are owned | Refuse                |
| Stop the rollout at the first role not back (SERVING for frontend, in its ring, owns shards for history) within the timeout | Stop; print rollback |
| Stopping or removing the last running instance of a role                                                | Refuse                            |
| Immutables differ from the live cluster: `cluster`, `shards`, persistence kind, a node's address        | Refuse: "build a new cluster"     |
| A persistence address change reaches a store that does not hold the live cluster (`cluster_metadata_info`) | Refuse (`refused.store_identity`) |
| A node too small for its roles (formula leaves no room above the role floors)                           | Refuse                            |
| Shard headroom: with one history node down, a survivor's predicted history memory above its GOMEMLIMIT  | Refuse                            |
| DB budget: sum of every role's pools above what PostgreSQL can give                                     | Refuse (`refused.db_budget`)      |
| Version change through `apply` or `promote`                                                             | Refuse: `tops upgrade`            |
| Upgrade skips a minor or downgrades                                                                     | Refuse                            |
| A ring member not in the spec                                                                           | Refuse to apply; name it          |
| Another run holds the cluster lock; a run that lost its lease writes                                   | Refuse; the helper fences writes  |
| A dynamic-config key unknown to the catalog of the spec's Temporal minor, or of the wrong type          | `validate` error                  |
| Rendered config rejected by the target `temporal-server`                                                | Refuse before any write (`refused.render_rejected`) |
| Live file differs from the last applied render (hand edit)                                              | `drift` and `plan` show it; `apply` overwrites after confirmation |
| `--force` overrides the before-gate only, after typing the node name                                    | Never with `-y`                   |

Every mutating run writes a history record on each node it touched and keeps the newest 10 snapshots.

## Decisions

| #   | Decision                                                                                     | Why                                                                                       |
| --- | -------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------- |
| D1  | Workstation-driven over SSH from one spec; the node helper is the same binary                | Operating model                                                                           |
| D2  | No state file; reality is read from nodes and Temporal each run                              | Nothing to lose, lock or corrupt                                                          |
| D3  | Templates embedded in tops; the fleet repo holds specs, settings and secrets only            | Validation and safety need to know what each file means                                   |
| D4  | Temporal, metrics and pprof reached through SSH forwarding to loopback                       | Port 22 only from the workstation                                                         |
| D5  | `apply` and `upgrade` are separate                                                           | A version change has a different order (schema first) and risk                           |
| D6  | v1 = roles per node, environments, sizing, PostgreSQL; v2 = Cassandra + Elasticsearch only    | Roles are the core of the operating model; datastores are a separate, larger topic       |
| D7  | No compatibility layers: spec changes land in place, with the fleet repo updated in the same change | AGENTS.md                                                                           |
| D8  | tops does not deploy monitoring; it emits scrape targets                                     | Dashboards are fleet-repo content                                                         |
| D9  | SSH user has passwordless sudo; the helper runs as root                                     | Whoever can upload and run the helper is root anyway; every run is recorded per node     |
| D10 | Cobra, `x/crypto/ssh`, Temporal API, `expfmt`, go-systemd; no custom parsers except `secrets.env` | Established libraries (TRD "Library choices")                                       |
| D11 | Releases with GoReleaser: linux amd64/arm64, darwin arm64, checksums, SBOM, attestations     | Verifiable binaries; the helper comes from the matching release asset                     |
| D12 | Semver; `--json` fields only added within a major                                           | Scripts can rely on the shape                                                             |
| D13 | Each release supports two Temporal minors; tops 1.x: 1.30 and 1.31                          | Upgrades are tested previous → current                                                    |
| D14 | Web UI from the official ui-server release binary under systemd                              | No Docker on Temporal nodes                                                               |
| D15 | tops logs its own runs as JSON (`slog`); no Prometheus endpoint                              | A CLI has no process to scrape                                                            |
| D16 | Environments as directories plus a shared `base/`; `-e` required                            | "Environments"                                                                            |
| D17 | One unit per role per node: the `temporal@.service` template, instance = role                | Per-role limits, restarts, logs, metrics and pprof; all-in-one is a node listing all four |
| D18 | Sizing formula and constants are the author's own, measured on the lab and recorded with their samples | No borrowed numbers; reproducible (TRD "Sizing")                                  |
| D19 | The settings catalog is generated from Temporal's source; hot vs restart is classified by hand with evidence, unknown = restart | The source does not encode hot vs restart (TRD "Settings catalog") |
| D20 | Promotion edits files in the fleet repo; it never touches a cluster                          | GitOps: the change is reviewed and committed, then planned and applied per environment    |
| D21 | Mutating commands take the lock before they plan; the confirmed plan is the executed plan    | No gap between "what you confirmed" and "what runs" (Terraform's model)                  |
| D22 | tops' e2e fleet fixture (`test/e2e/fleet`, cluster `tops-e2e`) and temporalScale's `lab` (`tscale-lab`) share the VPSLAB containers and `vps-pg`; one at a time; every e2e run starts with `make lab-reset` | The workstation fits one cluster; the store of one would otherwise refuse the other (`refused.datastore_in_use`) |
| D23 | The catalog generator is its own Go module (`tools/cataloggen`) run against tagged Temporal source checkouts, one per supported minor | One `go.mod` cannot require two minors of `go.temporal.io/server`; generator dependencies stay out of tops |
| D24 | A spec field enters the schema with the milestone that implements it (`pin` T2.4, `ui` T6.2) | Grow in layers: `validate` never accepts what tops cannot yet do                         |
| D25 | Phase 0 realigns the existing code with these docs before T1.4                              | The code was written against the Oct 10 design; drift is fixed first, not carried         |

## Milestones

Each ends with a release that works end to end on VPSLAB. Effort is for one person building every line by
hand, at about 12-15 hours a week (Q18). Measured pace so far: T1.1-T1.3, about 1,600 lines including tests,
in 10 days (2026-10-11 to 2026-10-20).

| #   | Release | Scope                                                                                                        | Demo                                                                                         | Effort |
| --- | ------- | ------------------------------------------------------------------------------------------------------------ | -------------------------------------------------------------------------------------------- | ------ |
| P0  | -       | Cleanup and realign: repo hygiene, version via ldflags, `healthchecks`, obsolete spec fields out, `roles` in, `-e` replaces `-f` | In temporalScale: `tops -e lab validate` passes on a spec with roles                          | 3-5 days |
| T1  | v0.1.0  | Lines in problems; secrets; catalog generator and `knobs`; classification; base + overrides merge; `diff`; `init`; release pipeline | In temporalScale: `tops knobs cacheTTL`, `tops diff lab staging -v`; `go install …@v0.1.0`    | 4-5 wk |
| T2  | v0.2.0  | Lab budget and measurement by hand; helper, SSH, host keys; cgroup-aware detection; sizing, pins and `capacity`; per-role rendering; `plan` | On a bare VPSLAB: `tops -e lab capacity` per node and role; `tops -e lab plan` lists every file, exit 2 | 5-6 wk |
| T3  | v0.3.0  | Releases, validation with the target binary, schema, lock, write and snapshots, template units, Temporal reads, per-role gate, `bootstrap`, `status`, `ns` | `make lab-reset && tops -e lab bootstrap -y && tops -e lab status`: rings per role, 512 shards | 5 wk |
| T4  | v0.4.0  | Safety rules, hot vs restart per role, rolling `apply`, `restart`, `rollback`, `drift`, `promote`           | Hot key: no restart; restart key: each reader once; kill mid-apply stops; rollback + undo; hand edit → `drift` exit 2; `promote` then `diff` exit 0 | 4 wk |
| T5  | v0.5.0  | `top`, `logs`, `audit`, `pprof`, `investigate`, `targets`                                                   | Under load `audit` names the busy layer; `pprof history --node vps-01 heap` saves a profile    | 3 wk   |
| T6  | v0.6.0  | Firewall, nginx `/health`, node_exporter, the `ui` role; `node add/remove`; role add/remove via `apply`     | Remove/add vps-03; move matching off vps-01 and back with shards owned throughout              | 3 wk   |
| T7  | v0.7.0  | `upgrade` with schema migration; catalog, classes and constants for the second minor                        | Bootstrap 1.30.x, upgrade to 1.31.3 rolling per role; skip and downgrade refused               | 2 wk   |
| T8  | v1.0.0  | Every refusal has an e2e test; sizing re-validated under load; coverage, README, command reference         | Full e2e suite green on the release binary                                                    | 2 wk   |
| T9  | v1.1.0  | `apply --at`, `run`                                                                                          | A scheduled apply runs unattended; one refused as stale after a hand change                    | 1 wk   |
| T10 | v2.0.0  | Cassandra + Elasticsearch persistence, identity guard, datastore health in `status`                         | VPSLAB + 3 Cassandra + 1 ES: bootstrap, drills from temporalScale v2, keyspace/RF/index changes refused | 4-5 wk |

Total to v1.0.0: about 33-38 weeks at the stated pace. temporalScale deletes its bash lab and scripts at
v0.4.0, once tops can change and roll back its `lab` environment (v0.3.0 only builds and reads it).

## User stories and acceptance criteria

Acceptance commands are in the [implementation plan](implementation-plan.md); every story below maps to at
least one row there.

### P0: cleanup and realign

| Story                                                                                          | Accepted when                                                                                |
| ---------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------- |
| As the operator, I select an environment with `-e` and the spec lives in the fleet repo, so I cannot validate the wrong file | `tops -e lab validate` exits 0 in temporalScale; `tops validate -f x` exits 4 (`usage.invalid`); no `-f` in `--help` |
| As the operator, every node in my spec says which roles it runs                                 | A node without `roles`, an unknown role, a duplicate, or a role on no node is a validation error |
| As a reviewer, `tops version` on a built binary prints its version                             | `make build && bin/tops version` prints `git describe` output, not `dev`                     |

### T1: describe, offline (v0.1.0)

| Story                                                                                          | Accepted when                                                                                |
| ---------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------- |
| As the operator, a broken spec tells me the field and the line                                  | Every problem in `validate --json` has `path` and `line > 0`; golden over the bad fixtures   |
| As the operator, a secrets file readable by others is refused before it is used                 | `chmod 644 secrets.env` → `validate` exits 4 `secrets.mode`                                  |
| As the operator, I find every cache setting, which role reads it, whether it is hot, its default | `tops knobs cache` lists them; `history.cacheTTL` shows roles `[history]`, apply `restart`, default `1h` |
| As the operator, a misspelled key in `overrides.yaml` is caught offline with a suggestion       | `validate` exits 4, message names the closest key                                            |
| As the operator, I see which value each layer gives a key                                       | `tops knobs <key> -e lab` shows default, base, override, effective                            |
| As the operator, I see what lab has that staging lacks                                          | `tops diff lab staging` exits 2 listing only promotable paths; `-v` adds by-design ones       |
| As the operator, a new environment starts from a commented template                             | `tops init qa` creates three files, `secrets.env` 0600; a second `init qa` exits 4            |
| As a reviewer, I install a tagged release and verify it                                         | `go install …@v0.1.0`; release has checksums, SBOM and provenance                            |

### T2: measure, reach, size, plan (v0.2.0)

| Story                                                                                          | Accepted when                                                                                |
| ---------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------- |
| As the operator, tops refuses a node whose host key I have not verified                         | Unknown key → exit 1 `ssh.host_key_unknown` with the fingerprint; nothing uploaded            |
| As the operator, I see what each node can hold before I deploy                                  | `tops -e lab capacity` shows per node the detected size with its source (cgroup) and per role MemoryMax/High, GOMEMLIMIT, CPUWeight, pools |
| As the operator, a role set too big for a node is refused at plan time                          | A node whose roles and pins leave `free < 0` → `plan` exits 3 `refused.node_too_small` with the formula line |
| As the operator, I see every file a bootstrap would create, and nothing is copied                | `plan` exits 2 on bare nodes; `TestPlanTouchesNothing` passes                                |
| As a reviewer, the sizing constants come with their evidence                                    | `internal/sizing/testdata/measure-*.json` exists; a test recomputes every constant from it   |

### T3: build and see (v0.3.0)

| Story                                                                                          | Accepted when                                                                                |
| ---------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------- |
| As the operator, one command builds a per-role cluster on bare nodes                            | Fresh VPSLAB → `bootstrap -y` → `status` healthy, 512 shards owned, < 5 min                  |
| As the operator, an interrupted bootstrap continues where it stopped                            | Kill tops after the history stage; re-run exits 0 and starts only what is missing            |
| As the operator, bootstrap never takes over a datastore that holds another cluster              | Exit 3 `refused.datastore_in_use`; nothing changed                                           |
| As a drill script, I assert shards and ring sizes                                               | `status --json` has `.temporal.shards.owned` and `.temporal.rings.<role>.members`; exit 5 when a node is down |
| As the operator, the spec's namespaces exist with their retention                               | `ns ensure` twice: the second reports no change                                              |

### T4: change safely (v0.4.0)

| Story                                                                                          | Accepted when                                                                                |
| ---------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------- |
| As the operator, a hot key changes without a restart                                            | `apply --json` has no `restart` step                                                         |
| As the operator, a restart key restarts each reader once, in order, shards owned throughout     | Steps are `history@vps-01`, `history@vps-02`; ownership dips recover within 10 s             |
| As the operator, a node dying mid-apply stops the rollout                                       | Exit 1 `rollout.role_unhealthy`; later steps untouched                                       |
| As the operator, I undo any apply, and undo the undo                                            | `rollback <id>` then `rollback <new id>`; status healthy after each                           |
| As two operators, we cannot change the lab at once                                              | Second `apply` exits 3 `lock.held` naming the holder                                         |
| As the operator, a hand edit is found and removed                                               | `drift` exits 2 naming the file; `apply` restores; `drift` exits 0                           |
| As the operator, lab's proven values reach staging as a reviewed file change                    | `promote lab staging -y`; `diff` exits 0; `temporal.version` never written                   |

### T5: look (v0.5.0)

| Story                                                                                          | Accepted when                                                                                |
| ---------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------- |
| As the operator in an incident, one command names the layer that is choking                     | At the saturated rate `audit --json` has `.closest` set; at the reference rate `.verdict == "ok"` |
| As the operator, I see per-role memory against its limits and the Go heap against GOMEMLIMIT    | `top --json`: every role has `memHighBytes > 0` and `goMaxProcs > 0`                         |
| As the operator, I read errors of one role across nodes, merged by time                         | `logs --role history -E` shows lines from both history nodes                                 |
| As the operator, I take a heap profile of one role on one node                                  | `pprof history --node vps-01 heap` writes a file `go tool pprof` reads                       |
| As the operator, I paste a UI URL and get a report                                              | `investigate <url>` writes `investigate-<wf>-<time>.md` with every section                   |
| As temporalScale, my Prometheus targets come from the spec                                      | `targets --job temporal` has one entry per role instance with a `role` label                 |

### T6: reshape (v0.6.0)

| Story                                                                                          | Accepted when                                                                                |
| ---------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------- |
| As the operator, nodes accept Temporal traffic only from the sources in `access`                | A source not listed cannot reach 7233; peers can reach each other                            |
| As the operator, a load balancer can health-check frontends                                     | `curl http://<frontend node>/health` returns 200 from an admin or healthcheck source          |
| As the operator, the Web UI runs on the node that lists `ui`                                    | `curl` of `http://10.80.0.11/` from an admin source returns 200                              |
| As the operator, I move a role between nodes by editing `roles`                                 | Matching off vps-01 and back; ring 2 then 3; shards owned throughout                         |
| As the operator, I remove and re-add a node safely                                              | `node remove vps-03`, delete, `apply`; restore; `node add vps-03`; status healthy             |

### T7-T10

| Story                                                                                          | Accepted when                                                                                |
| ---------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------- |
| T7: As the operator, I upgrade one minor with the schema migrated once and roles rolled         | 1.30.x → 1.31.3: schemas 1.19/1.14, every role on 1.31.3, `cluster.yaml` rewritten with comments kept |
| T7: As the operator, an unsafe upgrade path is refused                                          | Skip a minor or downgrade → exit 3 `refused.upgrade_path`                                    |
| T8: As a reviewer, every refusal is proven, not claimed                                         | One `TestRefuse…` per v1 row of the refusal index; sizing holds under 30 min of load         |
| T9: As the operator, I schedule a change for a quiet hour and it refuses if the cluster moved   | `--at` in 2 min runs; a hand edit during the wait → `refused.plan_stale`                     |
| T9: As the operator, I watch a role that will not start in the foreground                       | `run matching --node vps-03` streams its startup; Ctrl-C leaves the unit stopped             |
| T10: As the operator, I run Temporal on Cassandra + ES and cannot break its identity            | Bootstrap on `lab-cass`; keyspace and RF changes → `refused.immutable`                        |

## Testing strategy (summary)

Full strategy in [TRD "Test strategy"](trd.md#test-strategy).

| Layer           | What                                                                                                        |
| --------------- | ----------------------------------------------------------------------------------------------------------- |
| Unit            | Table-driven: spec, fleet, merge, catalog, sizing, plan, safety, gate, audit math, URL parsing             |
| Golden          | Rendered files per node and role; `--json` documents; human output of every command; catalog              |
| Helper          | Node ops in a systemd container through the real SSH path                                                   |
| End to end      | `make e2e LAB=./lab` on VPSLAB: every journey and every refusal. Required for every release                 |
| Static          | `golangci-lint` (gofmt), `go test -race`, `govulncheck` in CI                                               |

## Challenged assumptions

| Assumption                                          | Challenge                                                                    | Resolution                                                               |
| --------------------------------------------------- | ---------------------------------------------------------------------------- | ------------------------------------------------------------------------ |
| "Roles are an advanced v2 feature"                  | One process per node hides per-role memory, restarts, logs and pprof; splitting later rewrites rendering, gate and sizing | Roles per node from v1 (D6, D17)              |
| "Fixed size profiles are simpler"                   | A profile is wrong the moment a node is resized or gains a role              | Per-node sizing from detected size and roles, with pins (D18)            |
| "Adopt the cluster the bash scripts built"          | That cluster runs one unit per node on a lab being deleted; adopting it means a one-off migration | Dropped: bootstrap a fresh cluster; no migration code |
| "Hot vs restart can be read from Temporal's source" | Settings declare key, type, default and description; whether a consumer re-reads a value is decided at each call site | Generated catalog + hand classification with evidence; unknown = restart (D19) |
| "The generator can import the server module"        | Two supported minors need two versions of one module; one `go.mod` holds one | Separate module, tagged source checkouts (D23)                           |
| "Promotion means copying a running config"          | Environments differ by design (nodes, addresses, sizes)                      | Promote only promotable fields, as file edits (D20)                      |
| "Plan, confirm, then lock is fine"                  | Another run can change the cluster between the confirm and the lock          | Lock first (D21)                                                         |
| "A scheduled apply needs node-side timers"          | Node timers move decisions onto nodes and need state between runs (D1)       | v1.1: the workstation runs a confirmed plan at a time and refuses it if the cluster changed |
| "Production grade means an agent"                   | An agent is a service to secure and upgrade on every node for one operator   | SSH + helper, no daemon                                                  |
| "Restricted sudo is safer"                          | The helper is uploaded by the same user                                      | D9                                                                       |
| "Declarative means `apply` handles node removal"    | Deleting a node from the spec removes its SSH address before it can be drained | `node remove` first, then edit the spec                                |
| "Validate accepts the whole v1 schema from day one" | `validate` would pass a `ui` node that nothing renders until T6              | Fields enter with their implementation (D24)                             |
| "The docs and code agree"                           | Code has `-f`, `profile`, `ui.node`, no `roles`; `healthchecks` unchecked; version is a constant | Phase 0 (D25)                                         |

## Risks

| #   | Risk                                                                                     | L      | I      | Mitigation                                                                                      |
| --- | ---------------------------------------------------------------------------------------- | ------ | ------ | ----------------------------------------------------------------------------------------------- |
| R1  | Scope creep into Ansible/Terraform                                                       | High   | High   | Scope boundary is the contract                                                                  |
| R2  | A key classified hot actually needs a restart: a change silently does not take effect    | Medium | High   | Unknown = restart; every hot classification cites a call-site read and a lab check; `knobs -v` shows the evidence |
| R3  | Sizing constants measured on small lab nodes do not hold on large servers                | Medium | Medium | Measure at two node sizes; pins as the escape; T8 re-validates under load; re-measured per Temporal minor |
| R4  | Lab caps exceed the workstation's free memory; every measurement is silently wrong       | Medium | High   | T2.1 measures the workstation first; the override's total caps fit a stated budget; e2e checks `free -m` before each run |
| R5  | Applying to the wrong environment                                                        | Low    | High   | `-e` required, no env var default; every prompt names the environment and cluster              |
| R6  | Workstation dies mid-apply                                                               | Medium | Medium | At most one role mid-change; lease expiry; re-run plans what remains                            |
| R7  | Rendered config or metric names change between Temporal versions                        | Medium | High   | Validation with the target binary; catalog and audit fixtures per supported minor              |
| R8  | Plain gRPC on 7233: any client IP is admin                                               | Certain| Medium | tops never widens `access.clients`; mTLS deferred                                               |
| R9  | One maintainer: e2e suite rots                                                           | Medium | Medium | No release without a green e2e report                                                           |
| R10 | Schedule: v1.0 is 8-9 months of evenings; motivation and the portfolio window run out first | High | High   | Every milestone is a demoable release; the README's headline table grows with each             |
| R11 | T2.1 measurement is the largest unknown: a hand-built per-role cluster, a load generator and six runs before any SSH code | High | Medium | Split into T2.1a (budget, caps, hand cluster) and T2.1b (runs); constants re-validated in T8, so a first set need not be perfect |
| R12 | Catalog generator needs all of Temporal's dependencies to type-check (`go/packages`)     | Medium | Medium | Source checkouts cached under `.cache/`; CI caches the module cache; output committed            |
| R13 | VPSLAB is shared by tops e2e and temporalScale's `lab`; `vps-pg` exists only as an uncommitted change in VPSLAB | High | Medium | D22; Phase 0 commits `vps-pg` in VPSLAB (Q19); the e2e harness checks the lab contract first |

## Success metrics

| Metric                                                          | Target                         |
| --------------------------------------------------------------- | ------------------------------ |
| Fresh VPSLAB to a healthy 3-node cluster (`make lab-up` excluded) | One `tops bootstrap`, < 5 min |
| No-op `tops plan` on 3 nodes                                    | < 10 s                         |
| `validate`, `knobs`, `diff`                                     | < 300 ms                       |
| Rolling restart of every role, no unowned shard after each step | 100% of e2e runs               |
| Safety refusals covered by e2e tests                            | All v1 rows of the refusal index |
| Unit coverage                                                   | >= 80%; `safety`, `plan`, `sizing` >= 90% |
| Releases with a green e2e report attached                       | 100%                           |
| temporalScale environments operated with tops only              | From v0.4.0; no scripts left   |
| Reviewer path: README to a running lab cluster                  | < 15 min on a machine with Docker |

## Deferred

| Item                                       | Why later                                    |
| ------------------------------------------ | -------------------------------------------- |
| mTLS on 7233 and for tops' Temporal calls  | Changes the security model of every project  |
| Secrets via sops or a vault                | Q3                                           |
| Lab e2e in CI                              | Q4                                           |
| LB pool automation                         | Provider-specific; printed as a manual step  |
| Per-node dynamic-config values             | No need found; per-node differences come from sizing |

## Open questions

| #   | Question                                                                                                       | Recommendation                                                                                         |
| --- | -------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------ |
| Q3  | Secrets: untracked 0600 `secrets.env` per environment, or sops in git?                                         | `secrets.env` for v1; sops when a second machine runs tops                                             |
| Q4  | Run the lab e2e in GitHub Actions?                                                                              | Not for v1; local e2e gates releases                                                                   |
| Q11 | Supported minors for 1.x: 1.30/1.31 (1.32 exists)?                                                              | Keep for 1.x; move to 1.31/1.32 in v2.0                                                               |
| Q14 | VPSLAB caps for e2e (default 1 CPU / 1 GiB)?                                                                    | Fit a 4.5 GiB budget: vps-01/02 1.5 CPU / 1.25 GiB, vps-03 1 CPU / 768 MiB, vps-pg 1 CPU / 512 MiB (sized neutrally from measurement), monitoring 512 MiB for 4 containers (4.25 GiB). v2 (`lab-cass`): 6 GiB. Final values from T2.1b |
| Q18 | How many hours a week go into tops? The schedule above assumes 12-15                                           | Confirm, or give the number; the milestone table is rescaled, scope is not                             |
| Q19 | `vps-pg` exists only as an uncommitted change to VPSLAB's `compose.yaml`. Commit it in VPSLAB, or define it in tops' `test/e2e/vpslab.override.yaml`? | Commit it in VPSLAB (profile `pg`): temporalScale's docs already name VPSLAB as its owner, and both projects use it |
| Q20 | temporalScale's shape S3 runs all four roles on vps-03, proposed at 768 MiB. What if T2.1b's fit check says they do not fit? | Give vps-03 vps-01's size (1.25 GiB). That makes the total 4.75 GiB, over the 4.5 GiB budget, so take the 0.5 GiB from the measured slack of `vps-pg` or monitoring. Decide this with the measured numbers, not before |

Resolved since the previous draft (ids kept because temporalScale cites them):

| #   | Question                                                                                   | Resolution                                                                           |
| --- | ------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------ |
| Q1  | Workstation-driven, no agent, no state file?                                               | Yes (D1, D2)                                                                         |
| Q7  | `rollback` with no id: list only, or roll back the newest?                                 | List only: a rollback restarts roles and must name what it restores                  |
| Q10 | A node stopped by `node remove` but still in the spec?                                     | `apply` refuses (`refused.node_stopped`)                                             |
| Q13 | Where does the VPSLAB checkout live?                                                        | `tops/lab` (git-ignored); specs name its keys by `~` path                            |
| Q15 | What is the second environment before real servers exist?                                  | `environments/staging` as files only; validated, diffed, promoted into, never applied |
| Q16 | Rolling order within an apply?                                                             | Role-major: history, matching, frontend, worker, ui                                  |
| Q17 | Do `knobs` classifications ship in tops?                                                   | Yes: facts about Temporal's code, versioned with the catalog                         |
