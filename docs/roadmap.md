# tops roadmap

Project: tops (Temporal Ops), `github.com/2SSK/tops`. Status: draft. Date: 2026-10-23.
Sibling docs: [PRD](prd.md) · [TRD](trd.md) · [App flow](app-flow.md) · [Design brief](design-brief.md) ·
[Backend schema](backend-schema.md) · [Implementation plan](implementation-plan.md) · [Concepts](concepts.md)

The reading and build path through the other seven documents. The implementation plan is the step list; this
file is the route: what to read, what to build, what proves it, and how each capability sits on the one
before. When this file and a sibling disagree, the sibling wins.

## 0. What you are building

One Go binary, `tops`, run from your workstation, that deploys, changes and investigates bare-metal Temporal
clusters over SSH.

| Idea               | In one line                                                                                       |
| ------------------ | ------------------------------------------------------------------------------------------------- |
| Two faces          | The CLI decides on the workstation; the same binary as **node helper** (`tops node <op>`, root) executes one typed op and exits |
| Fleet repo         | temporalScale holds `base/` (shared settings per role) and `environments/<env>/` (one complete cluster each); `tops -e <env>` picks one |
| Roles per node     | Each node lists the roles it runs; each role is its own `temporal@<role>` unit; all-in-one = a node listing all four |
| Sizing             | Each role's memory, CPU weight and DB pools come from the node's detected size and its roles, by your own measured formula |
| Settings catalog   | Every dynamic-config key, generated from Temporal's source; hot or restart classified by you, with evidence |
| Safe change        | `plan` before anything is copied; `apply` locks first, then one role on one node at a time behind a per-role gate; fencing; rollback; drift |
| v2                 | Cassandra + Elasticsearch persistence with the identity guard; nothing else                        |

## 1. How to read the documents

1. **prd.md**: what and why. Headline goals H1-H8 (what tops does better than its bash predecessor), kept
   ideas G1-G7, safety table, decisions D1-D25, milestones P0 and T1-T10 with user stories.
2. **concepts.md**: read it whole before P0; come back per step.
3. **trd.md**: build and version, fleet root, validation rules, merge order, settings catalog, sizing,
   rendering and ports, helper protocol, gate and orders, lock and fencing, plan/apply, exit codes, tests, CI.
4. **backend-schema.md**: the exact shapes: fleet layout, `cluster.yaml` (with the `Since` column), catalog,
   sizing evidence, helper types, node state, run log, files on a node, `--json`.
5. **design-brief.md**: principles, grammar, flags, TTY and colour rules, error catalogue, example outputs.
6. **app-flow.md**: every command's flow and output shape, the journeys, the refusal index.
7. **implementation-plan.md**: steps with Goal / Learn first / Build / Verify / Pitfalls / Done when.

## 2. Where you are

| Done                                                                  | Next                                            |
| --------------------------------------------------------------------- | ----------------------------------------------- |
| T1.1 repo, CI; T1.2 output, errors, exit codes; T1.3 spec v0 + `validate [-f] [--json]` (27 commits, 2026-10-11 to 10-20) | **Phase 0**: static build, version stamping, `healthchecks`, CHANGELOG, obsolete fields out, `roles` in, `-e` replaces `-f`. Then T1.4 (lines in problems) |

## 3. The capability ladder

Each rung is a release you can demo; each depends only on the rungs below (AGENTS.md: grow in layers).

| Rung | You can…                                                                      | Release | Milestone |
| ---- | ----------------------------------------------------------------------------- | ------- | --------- |
| 0    | **Realign**: the code matches the docs; `tops -e lab validate` on a spec with roles | -  | P0        |
| 1    | **Describe** environments, search settings, compare lab with staging, offline | v0.1.0  | T1        |
| 2    | **Measure and reach**: know what each node can hold; see what a bootstrap would write | v0.2.0 | T2   |
| 3    | **Build** a per-role cluster from bare VPSLAB nodes and see it whole          | v0.3.0  | T3        |
| 4    | **Change** it safely, undo, detect drift, promote between environments        | v0.4.0  | T4        |
| 5    | **Look** inside during an incident: top, logs, audit, pprof, investigate      | v0.5.0  | T5        |
| 6    | **Reshape**: firewall and UI, add or remove nodes, move roles between nodes   | v0.6.0  | T6        |
| 7    | **Upgrade** Temporal one minor at a time                                       | v0.7.0  | T7        |
| 8    | **Trust** it: every refusal tested, sizing proven under load                   | v1.0.0  | T8        |
| 9    | **Schedule** a change; **debug** a role that will not start                    | v1.1.0  | T9        |
| 10   | **Swap persistence** to Cassandra + Elasticsearch                              | v2.0.0  | T10       |

## 4. Phase by phase

### P0: realign (no release)

- **End state.** `make build` is static and stamped; `TOPS_ROOT=internal/fleet/testdata/fleet tops -e lab
  validate` passes on a spec with roles; `-f`, `profile`, `secrets`, `dynamicConfig`, `ui.node` are gone.
- **Read.** Concepts "Building and shipping Go"; TRD "Build and version", "Fleet root and environments";
  backend schema "Fields".
- **Build.** P0.1 hygiene; P0.2 version; P0.3 `healthchecks` + CHANGELOG; P0.4 obsolete fields; P0.5 roles;
  P0.6 `-e` and the fleet root.

### T1: describe (v0.1.0), offline

- **End state.** In temporalScale: `tops -e lab validate`, `tops knobs cacheTTL`, `tops diff lab staging -v`.
- **Read.** Concepts "Temporal", "Environments and promotion", "Code generation"; TRD "Settings catalog";
  backend schema "Settings catalog".
- **Build.** T1.4 lines in problems; T1.5 secrets; T1.6 catalog generator (own module) and `knobs`; T1.7
  classify; T1.8 merge; T1.9 `diff` and the exit-code prefixes; T1.10 `init`, coverage gate, release.
- **Pitfalls.** No default environment. The generator never puts the server module into tops' `go.mod`.
  Unknown class means restart.

### T2: measure and reach (v0.2.0)

- **End state.** Bare VPSLAB: `tops -e lab capacity` per node and role; `tops -e lab plan` lists every file.
- **Read.** Concepts "Linux hosts", "Remote operation and safety"; TRD "Sizing", "Rendering per role",
  "Workstation, SSH and helper".
- **Build.** T2.1a budget, caps, a per-role cluster by hand; T2.1b measurement (omes
  `workflow_with_single_noop_activity`); T2.2 helper `hello`/`facts`; T2.3 SSH and upload; T2.4 sizing, pins,
  `capacity`; T2.5 per-role rendering; T2.6 `plan`, e2e harness.
- **Pitfalls.** Inside a VPSLAB node, `free` and `nproc` lie; read the cgroup. Keep the sum of all caps
  under the workstation's free memory. Fix the load before reading the numbers. This is the longest
  milestone; T2.1 alone may take two weeks.

### T3: build (v0.3.0)

- **End state.** `make lab-reset && tops -e lab bootstrap -y && tops -e lab status`: rings per role, 512 shards.
  temporalScale keeps its bash version until v0.4.0.
- **Read.** App-flow `bootstrap`, journey 2; TRD "Gate per role", "Orders", "Cluster lock", "Snapshots".
- **Build.** T3.1 releases and `stage`; T3.2 validation with the target binary; T3.3 schema; T3.4 lock with
  flock fencing; T3.5 write, snapshots, units, history; T3.6 Temporal reads; T3.7 gate; T3.8 `bootstrap`;
  T3.9 `status`, `ns`.
- **Pitfalls.** Resume is decided by the datastore. Frontends start first: every later gate reads through them.

### T4: change (v0.4.0)

- **End state.** Hot key: no restart. Restart key: each reader once, role-major. Kill mid-apply: stops.
  Rollback and undo. Hand edit: `drift` exit 2. `promote` then `diff` clean.
- **Read.** App-flow `apply`, `rollback`, `drift`, `promote`, journeys 1, 3, 4, 8, 9, 12.
- **Build.** T4.1 safety; T4.2 plan classes; T4.3 `apply`, `restart` (lock first); T4.4 `rollback`;
  T4.5 `drift`; T4.6 `promote`.
- **Pitfalls.** Never execute a plan computed before the lock. A limits change is one restart, not two.

### T5: look (v0.5.0)

- **End state.** Under load, `audit` names the busy layer; `pprof` saves a heap profile; `investigate` writes
  a report.
- **Build.** T5.1 `top`; T5.2 `logs`; T5.3 `audit`; T5.4 `pprof`; T5.5 `investigate`; T5.6 `targets`.

### T6: reshape (v0.6.0)

- **End state.** vps-03 removed and re-added; matching moved off vps-01 and back; the UI on vps-01.
- **Build.** T6.1 firewall, nginx, journald; T6.2 node_exporter and the `ui` role (spec field arrives here);
  T6.3 plan for host files; T6.4 role add/remove; T6.5 `node add`; T6.6 `node remove`.

### T7: upgrade (v0.7.0), T8: trust (v1.0.0)

- T7: the second minor (catalog, classes, constants, fixtures), path rules, rolling upgrade with schema first
  and write-back.
- T8: sizing proven under 30 min of load; one e2e test per refusal; coverage, README, command reference.

### T9: schedule and debug (v1.1.0), T10: Cassandra + ES (v2.0.0)

- T9: `apply --at` refuses a stale plan; `run` streams a stopped role in the foreground.
- T10: the v2 lab on VPSLAB; spec, schema, health, per-role connections, identity guard.

## 5. Package map

Create a package when a step first needs it; do not stub ahead.

| Package                         | First needed | State     |
| ------------------------------- | ------------ | --------- |
| `cmd/tops`, `internal/term`     | T1.1-T1.2    | done      |
| `internal/cli`                  | T1.1         | in use; `-f` replaced in P0.6 |
| `internal/spec`                 | T1.3         | realigned in P0.3-P0.5 |
| `internal/fleet`                | P0.6         |           |
| `internal/secret`               | T1.5         |           |
| `internal/catalog`; `tools/cataloggen` (own module) | T1.6 |   |
| `internal/settings`             | T1.8         |           |
| `internal/wire`, `internal/helper` | T2.2      |           |
| `internal/remote`               | T2.3         |           |
| `internal/sizing`, `internal/datastore/postgres` | T2.4 |   |
| `internal/render`               | T2.5         |           |
| `internal/plan`                 | T2.6         |           |
| `internal/release`              | T3.1         |           |
| `internal/cluster`              | T3.6         |           |
| `internal/gate`                 | T3.7         |           |
| `internal/rollout`, `internal/runlog` | T3.8   |           |
| `internal/safety`               | T4.1         |           |
| `internal/journal`              | T5.2         |           |
| `internal/metrics`, `internal/audit` | T5.3    |           |
| `internal/investigate`          | T5.5         |           |
| `internal/datastore/{cassandra,elasticsearch}` | T10.3-T10.4 | |

## 6. The lab: VPSLAB

`./lab` is the user's VPSLAB repo (git-ignored in tops): compose project `vpslab`, network 10.80.0.0/24,
`vps-01..03` at 10.80.0.11-13 (SSH also on 127.0.0.1:2201-2203), user `deploy`, keys in `lab/keys/`,
`vps-pg` (postgres:17.11, 10.80.0.20, compose profile `pg`; commit it in VPSLAB first, PRD Q19). It is also
temporalScale's `lab` environment: one cluster at a time (PRD D22).

| Need                       | How                                                                                     |
| -------------------------- | --------------------------------------------------------------------------------------- |
| Node and `vps-pg` caps     | `test/e2e/vpslab.override.yaml` copied to `lab/compose.override.yaml` by `make lab-up`; total fits the workstation budget; decided in T2.1b |
| PostgreSQL                 | `docker compose --profile pg up -d --wait vps-pg` (inside `make lab-up`)               |
| Host keys tops can use     | `lab/keys/known_hosts` lines for `10.80.0.11-13`, written by VPSLAB `make up`            |
| Fresh nodes, data kept     | `make lab-renew`; then `tops -e lab bootstrap` resumes onto them                         |
| Fresh nodes and database   | `make lab-reset` (every e2e run starts with it)                                          |
| v2 datastores              | environment `lab-cass`: `test/e2e/vpslab.v2.override.yaml`                               |
| Workstation as nodes see it | 10.80.0.1                                                                              |

## 7. Working method (every step)

1. Read the step's Learn-first links; if you cannot answer the concept's question, stop.
2. Write the failing table-driven test.
3. Make it pass with the least code; keep command handlers thin (`gocyclo` 10).
4. Refactor while green.
5. Run the step's Verify, then check its Done when; at a milestone's end, the acceptance table.
6. Break it on purpose: chmod `secrets.env` 644; hand-edit a rendered file and run `drift`; stop a node and run
   `status`.
7. Record shape changes in backend-schema.md, output in design-brief.md, flows in app-flow.md, and a
   CHANGELOG line, in the same change.

## 8. Guardrails

- AGENTS.md: no backward compatibility, simplest thing that meets the need, grow in layers, established
  libraries, long-term decisions.
- Clean room: ideas from the bash predecessor at feature level only. Its catalog, numbers, names and incidents
  never enter this repo; constants come from your own lab measurements, the catalog from Temporal's source.
- Binary < 40 MB (CI gate 35 MB), static. Coverage >= 80%; `safety`, `plan`, `sizing` >= 90%. `govulncheck`
  clean.
- No secret in any diff, log, argv or history record.
- No release without a green e2e report.

## 9. Schedule

At about 12-15 hours a week (PRD Q18).

| Milestone | Release | Effort   |
| --------- | ------- | -------- |
| P0        | -       | 3-5 days |
| T1 (rest) | v0.1.0  | 4-5 wk   |
| T2        | v0.2.0  | 5-6 wk   |
| T3        | v0.3.0  | 5 wk     |
| T4        | v0.4.0  | 4 wk     |
| T5        | v0.5.0  | 3 wk     |
| T6        | v0.6.0  | 3 wk     |
| T7        | v0.7.0  | 2 wk     |
| T8        | v1.0.0  | 2 wk     |
| T9        | v1.1.0  | 1 wk     |
| T10       | v2.0.0  | 4-5 wk   |

Start at P0.1. Do not look past P0.6 until `tops -e lab validate` passes on the fixture fleet.
