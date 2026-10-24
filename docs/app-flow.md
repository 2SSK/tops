# tops app flow

Project: tops (Temporal Ops), `github.com/2SSK/tops`. Status: draft. Date: 2026-10-23.
Sibling docs: [PRD](prd.md) · [TRD](trd.md) · [Design brief](design-brief.md) · [Backend schema](backend-schema.md) ·
[Implementation plan](implementation-plan.md) · [Concepts](concepts.md) · [Roadmap](roadmap.md)

Every command's flow: what it reads, what it locks, its steps, its failures with code and exit, and the shape
of its output in human and `--json` form. Part A is the per-command reference; part B the operator journeys
that chain commands (numbered 1-12; temporalScale cites them by number); part C the refusal index.

Examples use tops' e2e fixture fleet: environment `lab`, cluster `tops-e2e`, on VPSLAB: `vps-01` 10.80.0.11
(all four roles; `ui` from T6), `vps-02` 10.80.0.12 (all four), `vps-03` 10.80.0.13 (frontend, matching),
PostgreSQL `vps-pg` 10.80.0.20. Output skeletons use `<…>` for values; full rendered examples are in the
[design brief](design-brief.md#example-outputs); full JSON shapes in the
[backend schema](backend-schema.md#--json-outputs).

| Participant | Is                                                                   |
| ----------- | -------------------------------------------------------------------- |
| Op          | The operator at the workstation, in the fleet repo                   |
| tops        | The CLI                                                              |
| H1..H3      | The helper on vps-01..03 (`sudo tops node <op>` over SSH)             |
| F1..F3      | The frontend on each node, reached through a tunnel                  |
| DB          | PostgreSQL, through a tunnel from a node or by the schema tool on one |

## Shared steps

### Load (every command that takes `-e`)

1. Find the fleet root (`$TOPS_ROOT`, else walk up to `environments/`); resolve `environments/<env>/`.
2. Parse and validate `cluster.yaml`; merge and check `base/` + `overrides.yaml` (from T1.8); read
   `secrets.env` when the command connects.

| Failure                      | Code                         | Exit |
| ---------------------------- | ---------------------------- | ---- |
| No `-e` on a command that needs it | `usage.invalid` (lists environments) | 4 |
| `-e` on a command that takes none | `usage.invalid`          | 4    |
| No fleet root                | `spec.no_root`               | 4    |
| Unknown environment          | `spec.no_env` (lists environments) | 4 |
| Spec or settings invalid     | `spec.invalid` (every problem with path and line) | 4 |
| Secrets mode, owner, keys    | `secrets.mode`, `secrets.invalid` | 4 |

### Connect (every command that touches nodes; from T2.3)

1. Per node in parallel: dial `address:port`, strict host key, auth, `sudo -n true`, a test tunnel,
   `uname -m`.
2. `hello` (uploading the helper if `/usr/local/lib/tops/<sha16>/tops` is missing).

| Failure                          | Code                                         | Exit |
| -------------------------------- | -------------------------------------------- | ---- |
| Host key unknown / changed       | `ssh.host_key_unknown` / `ssh.host_key_changed` (fingerprints, line to verify) | 1 |
| Auth refused                     | `ssh.auth`                                   | 1    |
| sudo asks for a password         | `ssh.sudo_password`                          | 1    |
| Forwarding disabled              | `ssh.forwarding_disabled`                    | 1    |
| No helper binary for the arch    | `helper.no_binary`                           | 1    |
| A node unreachable               | Mutating commands: `ssh.unreachable` before any change, exit 1. Read commands: the node marked unreachable, the rest shown, exit 5 |

### Lock and confirm (every mutating command; from T3.4)

1. Acquire the lease on every node in spec order (D21), start renewals.
2. Plan under the lock; print it on stdout.
3. Confirm on the TTY: `<verb> to <env> (<cluster>: <nodes>)? [y/N]`; `-y` skips; no TTY and no `-y`
   refuses; `--json` requires `-y`.
4. Release on success, failure, refusal and interrupt.

| Failure                          | Code                     | Exit |
| -------------------------------- | ------------------------ | ---- |
| Another live run holds the lease | `lock.held` (holder, command, age, renewed) | 3 |
| Stale lease                      | warning, takeover, interrupted apply id printed | - |
| No TTY and no `-y`; `--json` without `-y` | `refused.no_tty` | 3    |
| Operator answers no              | nothing changed, `result: refused` | 3 |
| Lease lost mid-run               | `lock.lost` (from the helper)      | 1 |
| Ctrl-C                           | current op finishes or is abandoned; lock released | 130 |

# Part A: commands

## Spec group (offline)

### `tops -e <env> validate` (done; settings T1.8, secrets T1.5)

Lock: no. Nodes: no.

1. Load (above), all checks, no early exit: every problem is reported.
2. Warnings (key read by no role; key in both `common.yaml` and a role file; secrets file not git-ignored)
   do not change the exit code.

| Result                 | Exit |
| ---------------------- | ---- |
| Valid (warnings allowed) | 0  |
| Invalid                | 4 (`spec.invalid`) |

```
$ tops -e lab validate
✗ environments/lab/cluster.yaml:31  nodes[2].roles[1]  "matchng" is not a role (frontend, history, matching, worker)
✗ environments/lab/overrides.yaml:4  history.cachettl  not a 1.31 setting; did you mean history.cacheTTL?
! base/matching.yaml:2  frontend.namespaceRPS  read by no role on any node of lab
✗ environments/lab is invalid: 2 errors, 1 warning
  fix: edit the files above, then tops -e lab validate
  (code: spec.invalid)
```

Success: `✓ environments/lab is valid: cluster tops-e2e, 3 nodes, 10 roles`.

`--json`: `Validation` `{env, cluster, ok, errors: [{file?, path, line, message}], warnings}`; exit as above;
never an `Error` document for an invalid spec.

### `tops knobs [text] [-e <env>] [-v]` (T1.6; layers T1.8)

Lock: no. Nodes: no. Catalog: the spec's minor with `-e`, else the newest supported.

1. No text: every setting. Text: case-insensitive substring of the key, then of the description.
2. One exact key match: the full entry (description, precedence, constraints, evidence with `-v`).
3. With `-e`: per-layer values for each matched key.

| Failure                     | Code             | Exit |
| --------------------------- | ---------------- | ---- |
| No match                    | prints `no setting matches "<text>"`, closest keys | 0 |
| `-e` environment invalid    | as Load          | 4    |

```
Temporal <minor> · <n> settings
  key                     roles      apply    default  meaning
  <key>                   <roles>    <apply>  <def>    <first sentence of description>
unknown = not classified yet; plan restarts the role for it. details: tops knobs <key> -v
```

Exact key with `-e`: a `layers` block: `default <v> · base/common.yaml - · base/history.yaml 15m ·
overrides.yaml - · effective 15m`.

`--json`: `Knobs`.

### `tops diff <envA> <envB> [-v]` (T1.9)

Lock: no. Nodes: no.

1. Load both environments (each fully validated).
2. Compare resolved promotable values; with `-v` also by-design values.

| Result / failure                    | Code               | Exit |
| ----------------------------------- | ------------------ | ---- |
| No promotable difference            | -                  | 0    |
| Promotable differences              | `pending.differences` (reported) | 2 |
| Same environment twice              | allowed; exit 0    | 0    |
| Either environment invalid          | `spec.invalid` naming the environment | 4 |

```
<A> → <B>  (base/ is shared)

promotable
  ~ <path>        <A value>   <B value>
  + <path>        <A value>   -
  ! temporal.version  <A>  <B>   (promote does not write this: tops -e <B> upgrade <A>)

by design: <n> differences (tops diff <A> <B> -v)
promote with: tops promote <A> <B>
```

`--json`: `EnvDiff`.

### `tops init <env>` (T1.10)

Lock: no. Nodes: no.

1. Find the fleet root (`spec.no_root` outside one).
2. Refuse if `environments/<env>/` exists (`spec.env_exists`, exit 4).
3. Write `cluster.yaml` (commented template), `overrides.yaml` (`{}`), `secrets.env` (0600, keys commented);
   create missing `base/*.yaml` (`{}`). Never overwrite.

```
✓ created environments/qa/cluster.yaml
✓ created environments/qa/overrides.yaml
✓ created environments/qa/secrets.env (0600)
· base/ already complete
next: edit environments/qa/cluster.yaml, then tops -e qa validate
```

`--json`: `Init` `{env, created}`.

### `tops promote <from> <to> [--only versions,namespaces,settings] [--partial] [-y]` (T4.6)

Lock: no (files only). Nodes: no.

1. Load both; compute the promotable diff restricted by `--only`.
2. Drop identity fields; `temporal.version` is shown and never written.
3. Check every override key against `<to>`'s minor; unknown keys refuse unless `--partial`.
4. Show the edits per file; confirm (`write 2 files in environments/staging? [y/N]`).
5. Write through `yaml.Node` (comments and order kept); re-validate `<to>`; restore the originals on
   failure.

| Failure                              | Code                         | Exit |
| ------------------------------------ | ---------------------------- | ---- |
| Nothing to promote                   | prints `nothing to promote`  | 0    |
| A key unknown to `<to>`'s minor      | `refused.unknown_setting` (unless `--partial`) | 3 |
| `<to>` invalid after writing         | `spec.invalid`; files restored | 4  |
| No TTY and no `-y`                   | `refused.no_tty`             | 3    |

```
lab → staging
  environments/staging/cluster.yaml
    ~ ui.version                 2.54.1 → 2.55.0
    + namespaces[1] drills (24h)
  environments/staging/overrides.yaml
    + frontend.namespaceRPS
  ! temporal.version 1.31.3 vs 1.30.7 not written: tops -e staging upgrade 1.31.3
write 2 files? [y/N] y
✓ wrote 2 files. review with: git diff environments/staging
```

`--json`: `Promotion`.

## Integrate group

### `tops version [-e <env>]` (P0.2; nodes T2.3)

Without `-e`: `tops <version> (<commit>, <go version>)`. With `-e`: Load + Connect, then per node
`vps-01  helper <sha16>  uploaded|present`. Exit 0; with `-e`, connect failures as above.

`--json`: `Version`.

### `tops completion bash|zsh|fish` (done)

Prints the script. Dynamic completion reads files only, never SSH (design brief). `--json` is
`usage.invalid`.

### `tops -e <env> targets [--job temporal|node]` (T5.6)

Lock: no. Nodes: no (the spec is enough). One target per role instance on its metrics port; the `node` job
(`:9100` on every node) from T6. Output: the file-SD JSON array, with or without `--json` (it is already
JSON), so `tops -e lab targets > targets/lab.json` works.

## Change group

### `tops -e <env> capacity` (T2.4)

Lock: no. Nodes: Connect (read-only).

1. `hello` on every node: effective memory and cores with sources.
2. PostgreSQL limits through a tunnel from the first reachable node.
3. Sizing per node and role; headroom; DB budget.

| Result / failure                     | Code                         | Exit |
| ------------------------------------ | ---------------------------- | ---- |
| Fits                                 | -                            | 0    |
| A refusal would apply                | shown as `✗` rows with the formula line; `refused.node_too_small`, `refused.headroom`, `refused.db_budget` | 3 |
| Datastore unreachable                | database row `✗ unreachable`; sizing still shown | 5 |

```
<env> (<cluster>)  <n> nodes · constants measured <date> on VPSLAB (Temporal <minor>)

<node>  <cores> cores (<source>) · <mem> (<source>) · reserve <r>
  role       MemoryMax  MemoryHigh  GOMEMLIMIT  CPUWeight  pool  vis
  <role>     <v>        <v>         <v>         <w>        <p>   <p>   [pin]

cluster
✓ headroom   <node> down: <survivor> history owns <n> shards, predicted <x> of <y> GOMEMLIMIT
✓ database   pools <n> of <budget> available (<host> max_connections <m> · reserved <r> · other sessions <o>)
```

`--json`: `Capacity`.

### `tops -e <env> plan [--node N]` (T2.6; classes T4.2; host files T6.3)

Lock: no. Nodes: Connect; writes only the helper binary and `/run/tops/<run-id>` (removed).

1. Per node in parallel: `facts`, sizing, render, `validate` (with the staged target binary, if any).
2. Cluster reads (from T3.6): rings and identity through every reachable frontend; namespaces.
3. Classify per file and per key; detect drift; run the safety rules.

| Result / failure                     | Code                         | Exit |
| ------------------------------------ | ---------------------------- | ---- |
| Nothing to change, no drift          | -                            | 0    |
| Changes or drift pending             | `pending.changes` (reported) | 2    |
| A refusal                            | the refusal's code; plan still printed with the refused lines marked | 3 |
| Target binary rejects a render       | `refused.render_rejected` (file, role, Temporal's message) | 3 |
| Release not staged on a node         | `! not validated: <v> not staged` on that node; not a failure | 0/2 |

```
<env> (<cluster>)  plan for <n> nodes · validated with temporal-server <v> on <k> nodes

<node>
  <role>
    + <path>                                   create
    ~ <path>                                   <hot-reload|restart>
        <unified diff, secrets ***>
    → restarts <role> (<key> is <restart|unknown>)
    + starts <role> | - stops <role>
  ! drift: <path> edited by hand since <apply-id>; apply overwrites it

cluster
  namespaces  <in sync | + <name> | ~ <name> retention>
  rings       no unknown members

<n> nodes change · <k> restarts (<role> on <nodes>, one at a time) · <d> drift
apply with: tops -e <env> apply
```

`--json`: `Plan`.

### `tops -e <env> bootstrap [-y]` (T3.8)

Lock: yes. Nodes: Connect. Journey 2 has the full sequence.

1. Lock all nodes.
2. Preflight in parallel: Ubuntu 24.04; sizing fits; ports free (6933-6939, 7233-7243, 9233-9239,
   7933-7939; from T6 also 9100, 80, and 8080 on `ui` nodes); no `temporal@*` unit (unless resuming);
   clock synced (warning).
3. Datastore preflight through a tunnel: reachable; `cluster_metadata_info` empty or naming this cluster
   (resume); DB budget fits.
4. Plan (every file per node and role, releases, databases, namespaces); confirm.
5. Stage in parallel; schema from the first node; validate every role's files with the staged binary.
6. Start in role stages (frontend, history, matching, worker, ui), one node at a time within a stage, each
   gated.
7. Namespaces (`ns ensure`); history records; unlock; print the steps tops does not do (load balancer pool,
   `tops -e <env> targets`, a first datastore backup).

| Failure                           | Code                         | Exit | State left                      |
| --------------------------------- | ---------------------------- | ---- | ------------------------------- |
| Node too small                    | `refused.node_too_small`     | 3    | Nothing changed                 |
| Port in use                       | `refused.ports_in_use` (port, process) | 3 | Nothing changed           |
| Not Ubuntu 24.04                  | `refused.unsupported_os`     | 3    | Nothing changed                 |
| A `temporal@*` unit exists, store empty | `refused.node_in_use`  | 3    | Nothing changed                 |
| Datastore holds another cluster   | `refused.datastore_in_use`   | 3    | Nothing changed                 |
| DB budget                         | `refused.db_budget`          | 3    | Nothing changed                 |
| Download or checksum              | `release.download`, `release.checksum` | 1 | Partly staged; re-run continues |
| Schema tool fails                 | `schema.failed` (stderr, no secrets) | 1 | Re-run continues          |
| Target binary rejects a render    | `refused.render_rejected`    | 3    | Staged only                     |
| A role not healthy in time        | `rollout.role_unhealthy` (condition, journal tail) | 1 | Partial cluster; fix, re-run |

Human output: the plan (as `plan`), the confirmation, then one line per step as in `apply`, then:

```
✓ bootstrap <apply-id> done in <m>m<s>s · <n> roles on <k> nodes · 512 of 512 shards owned
not done by tops:
  · add frontend nodes (vps-01, vps-02, vps-03) to the load balancer pool
  · tops -e lab targets > <prometheus targets file>
  · take a first datastore backup
```

`--json`: `Rollout` (`command: "bootstrap"`).

### `tops -e <env> status [--role R]` (T3.9)

Lock: no. Nodes: Connect (read-only).

1. `facts` per node in parallel; Temporal reads through every reachable frontend; shards per history host;
   datastore schema versions.
2. Healthy = every listed role active, in its ring in every view, shards sum = `shards`, no unknown member,
   datastore reachable.

| Situation                    | Behaviour                                                                        | Exit |
| ---------------------------- | -------------------------------------------------------------------------------- | ---- |
| Healthy                      | `healthy`                                                                         | 0    |
| A check fails                | Failed rows `✗`, last line names the next command                                | 5 (`unhealthy.cluster`) |
| A node unreachable           | Row marked unreachable, the rest shown                                           | 5    |
| No frontend reachable        | Unit facts shown; Temporal rows `-`                                              | 5    |

```
<env> (<cluster>)  Temporal <v> · <n> nodes · <shards> shards · <store>        <HH:MM:SS> UTC

  node    role      unit    version  ring  shards  memory
✓ <node>  <role>    active  <v>      3/3   <n|->   <cur> of <high>  <pct>%  [SERVING]

cluster
✓ shards     <owned> of <spec> owned · created with <live>
✓ rings      frontend 3/3 · history 2/2 · matching 3/3 · worker 2/2 · no unknown members
✓ database   <host:port> via <node> · temporal <v> · temporal_visibility <v>

healthy | unhealthy: <first failed check>. next: <command>
```

`--json`: `Status`.

### `tops -e <env> ns [list]` and `ns ensure [-y]` (T3.9)

`ns list`: no lock; lists namespaces with state, retention, running workflows, and `spec`, `system` or
`unmanaged`. Exit 0 (5 if no frontend reachable).

`ns ensure`: lock; `+` create (`RegisterNamespace`), `~` retention change (`UpdateNamespace`) through one
frontend; confirm; wait until visible on every frontend (poll 1 s, 30 s timeout). Unmanaged namespaces are
never touched, never deleted. Nothing to do: exit 0 without asking.

```
  namespace  state       retention  running  managed
+ drills     -           24h        -        spec      create
~ default    registered  72h → 48h  12       spec      update
· perf       registered  24h        0        unmanaged
```

`--json`: `Namespaces` (list), `Rollout` (ensure).

### `tops -e <env> apply [--node N] [--force] [--timeout D] [-y]` (T4.3)

Lock: yes. Nodes: Connect. Journey 3 has the full sequence.

1. Lock; plan under the lock (as `plan`); refusals release and exit 3; nothing to do releases and exits 0.
2. Print the plan; confirm.
3. Stage what is missing; firewall additions (T6); hot changes and non-Temporal classes in parallel.
4. Restart, start and stop changes one role on one node at a time, role-major; each: gate before, `write`
   (snapshot first), `daemon-reload` if a unit changed, service action, gate after.
5. Namespaces; firewall removals (T6); history records; unlock; summary with the rollback command.

| Failure                              | Code                                        | Exit |
| ------------------------------------ | ------------------------------------------- | ---- |
| Any plan refusal                     | its `refused.*` code                        | 3    |
| Gate before fails at the first step  | `refused.ring_incomplete` / `refused.last_frontend` | 3 |
| Gate before fails after earlier steps | `rollout.gate_failed`; nothing further touched | 1 |
| Role not back in time                | `rollout.role_unhealthy`; journal tail; rollback hint | 1 |
| `--force` with `-y`                  | `refused.force_with_yes`                    | 3    |
| Lease lost                           | `lock.lost`                                 | 1    |

```
apply to lab (tops-e2e: vps-01, vps-02, vps-03)? [y/N] y

apply <apply-id>
 <m:ss>  ✓ lock        <nodes>
 <m:ss>  ✓ hot         <file> on <n> nodes (reloads within 10s)
 <m:ss>  ✓ <node>  <role>  gate     <conditions>
 <m:ss>  ✓ <node>  <role>  write    <n> files · snapshot <apply-id>
 <m:ss>  ✓ <node>  <role>  restart  <after conditions> (<s>s)
 <m:ss>  ✓ unlock

done in <d> · <n> nodes changed · <k> restarts
undo with: tops -e <env> rollback <apply-id>
```

On a TTY the running step is one live line on stderr; finished steps go to stdout. `--json`: `Rollout`.

### `tops -e <env> restart <role>|--all [--node N] [--force] [-y]` (T4.3)

Lock: yes.

1. Steps: `<role>` on every node that runs it (or `--node`), or `--all` every role of every node,
   role-major.
2. Confirm names the steps and the cost: `restart history on vps-01, vps-02, one at a time? shards move for
   ~30 s each [y/N]`.
3. Per step: gate before, restart, gate after; history record.

| Failure                          | Code                         | Exit |
| -------------------------------- | ---------------------------- | ---- |
| Role not run by `--node`         | `usage.invalid` naming the node's roles | 4 |
| Ring incomplete                  | `refused.ring_incomplete` (e.g. `history ring has 1 of 2 members: vps-02 is missing`) | 3 |
| Role not back                    | `rollout.role_unhealthy`     | 1    |

Output and `--json` as `apply`.

### `tops -e <env> rollback [list]` and `rollback <apply-id> [-y]` (T4.4)

`list` (also bare `rollback`): no lock; history records from every node merged by apply id, newest first;
rollbackable when every node the apply changed still has its snapshot. Changes nothing.

```
  apply id                  command   by            started         nodes  result  rollback
  20261023T153012Z-7f3a     apply     ssk@thinkpad  10-23 15:30     2      ok      ✓
  20261023T140155Z-3b7d     bootstrap ssk@thinkpad  10-23 14:01     3      ok      · snapshot rotated
```

`<apply-id>` (unique prefix accepted): lock; per node with the snapshot, diff snapshot vs live; plan per role
(hot restores in parallel, restart-class restores rolling with gates, role starts/stops reversed); confirm;
each `restore` snapshots the live state under the new apply id first; then:

```
✓ rolled back <id> as <new-id>
  environments/lab still describes the rolled-back change; tops -e lab plan shows it as pending. Fix the spec too.
  undo with: tops -e lab rollback <new-id>
```

| Failure                               | Code                           | Exit |
| ------------------------------------- | ------------------------------ | ---- |
| Snapshot rotated out on a changed node | `refused.not_rollbackable`    | 3    |
| Prefix ambiguous / unknown            | `usage.invalid` listing matches | 4   |
| A role not back                       | `rollout.role_unhealthy`; the partial rollback is itself undoable | 1 |

`--json`: `ApplyHistory` (list), `Rollout` (rollback).

### `tops -e <env> drift` (T4.5)

Lock: no. Nodes: Connect. No rendering: compares live facts with `applied.json` and the rings with the spec.

| Result                        | Code                     | Exit |
| ----------------------------- | ------------------------ | ---- |
| No drift                      | -                        | 0    |
| Drift                         | `pending.drift`          | 2    |
| A node unreachable            | `unhealthy.unreachable`  | 5    |

```
<env> (<cluster>)  <n> nodes

✓ <node>  no drift
✗ <node>  ~ <path>  edited since <apply-id>
          - temporal@<role>  stopped outside tops (was active)
✓ rings   no unknown members

drift on <k> node(s). restore with: tops -e <env> apply
```

`--json`: `Drift`.

### `tops -e <env> node add <node> [-y]` (T6.5)

Lock: yes. Precondition: `<node>` is in `cluster.yaml` with its roles, reachable, host key known.

1. Checks: `<node>` runs no `temporal@*`; the others pass their gates; it fits its roles; headroom and DB
   budget with it counted.
2. Plan: its files; firewall `peers +<address>` on the others; confirm.
3. Firewall on existing nodes first; then stage, validate, write on `<node>`; start its roles in bootstrap
   stage order, each gated; report shards each history node gave up.
4. History records; unlock; print the load balancer step if it runs a frontend, and `targets`.

| Failure                          | Code                         | Exit |
| -------------------------------- | ---------------------------- | ---- |
| Not in the spec                  | `refused.not_in_spec`        | 3    |
| Already runs Temporal            | `refused.node_in_use`        | 3    |
| A role not in its ring in time   | `rollout.role_unhealthy`; roles left running for diagnosis; `node remove` cleans up | 1 |

### `tops -e <env> node remove <node> [-y]` (T6.6)

Lock: yes.

1. Checks: no role becomes instance-less; with `<node>` gone, headroom holds; the others pass their gates.
2. Confirm: `take vps-03 out of the load balancer first. continue? [y/N]`.
3. Stop its roles in reverse stage order (ui, worker, matching, history, frontend): `disable --now`, wait
   until it leaves its ring in every view and, for history, until all shards are owned elsewhere.
4. History record (`node remove`, read by `drift` and `plan`); unlock; print: delete the node from
   `cluster.yaml`, then `apply` (removes it from the others' firewall).

| Failure                          | Code                         | Exit |
| -------------------------------- | ---------------------------- | ---- |
| Last instance of a role          | `refused.last_role_instance` | 3    |
| Survivors fail headroom          | `refused.headroom`           | 3    |

### `tops -e <env> upgrade <version> [--backup-confirmed] [-y]` (T7.3)

Lock: yes. Journey 7 has the full sequence.

| Failure                          | Code                         | Exit |
| -------------------------------- | ---------------------------- | ---- |
| Skips a minor, downgrades, unsupported, > 2 versions live | `refused.upgrade_path` | 3 |
| Backup not confirmed             | `refused.no_backup`          | 3    |
| Validation with the target fails | `refused.render_rejected`; nothing restarted; schema untouched | 3 |
| Schema tool fails                | `schema.failed`; nothing restarted | 1 |
| A role not back                  | `rollout.role_unhealthy`; mixed versions (supported); re-run resumes, or rollback (binary back, schema stays) | 1 |

Output: steps as `apply`, plus `schema temporal 1.18 → 1.19 · temporal_visibility 1.13 → 1.14` and
`wrote temporal.version: 1.31.3 to environments/lab/cluster.yaml (commit it)`. `--json`: `Rollout`.

## Look group (read-only, no lock; usable while an apply runs)

### `tops -e <env> top [--node N] [--role R] [-w N]` (T5.1)

Helper `top` per unit (cgroup files sampled over 1 s) plus `memory_heap` and `gomaxprocs` from each role's
metrics port. `-w` refreshes in place on a TTY (plain repeated blocks otherwise). Exit 0; 5 if a node is
unreachable.

```
  node    role      cpu   weight  memory              high    max     heap / GOMEMLIMIT  procs  psi mem  oom  restarts
  <node>  <role>    <%>   <w>     <cur> (<pct>% high) <high>  <max>   <heap> / <lim>     <n>    <avg10>  <n>  <n>
```

`--json`: `Top`.

### `tops -e <env> logs [--node N] [--role R] [-f] [-E|--warn] [-q] [-g text] [-t since] [-s] [-n N] [--raw]` (T5.2)

Helper `journal` streamed from every selected unit; merged by timestamp (each node's own clock, noted in the
header). `-s` prints a summary (count per level, top messages) instead of lines. `--raw` prints
`JournalEntry` NDJSON; `--json` is `usage.invalid`. Ctrl-C ends `-f` cleanly with exit 0.

```
<MM-DD HH:MM:SS.mmm>  <node>  <role>  <LEVEL>  <msg>  <key=value …>
```

### `tops -e <env> audit [-v] [--window 15s]` (T5.3)

Every role's metrics port and node_exporter (T6) scraped twice, `--window` apart, in parallel; rates and
quantiles from the deltas; one line per layer; the first marked line is where to look.

| Result                     | Exit |
| -------------------------- | ---- |
| All layers ok              | 0    |
| A layer warns or fails     | 0 (it is a report); `verdict` says which |
| Scrape failed on a node    | 5    |

```
<env> (<cluster>) audit  <n> nodes  <window> window

  <! n layers need attention  start with the first marked line | ✓ every layer healthy>

  <mark> <layer>                <figures>                       <plain-words verdict>

  details: tops -e <env> audit -v    longer window: --window 60s
```

`--json`: `Audit`.

### `tops -e <env> pprof <role> --node N [cpu|heap] [--seconds 30]` (T5.4)

Tunnel to `127.0.0.1:79xx` on the node; CPU blocks for `--seconds` (said before it starts). Writes
`./pprof-<node>-<role>-<kind>-<YYYYMMDDTHHMMSSZ>.pb.gz`.

| Failure                          | Code                         | Exit |
| -------------------------------- | ---------------------------- | ---- |
| Node does not run the role       | `usage.invalid` naming the node's roles | 4 |
| Endpoint unreachable             | `ssh.tunnel_failed`          | 1    |

```
· cpu profile of history on vps-01 for 30s …
✓ wrote pprof-vps-01-history-cpu-20261023T153012Z.pb.gz (412 KiB)
  open with: go tool pprof -http=: pprof-vps-01-history-cpu-20261023T153012Z.pb.gz
```

`--json`: `Profile`.

### `tops -e <env> investigate '<ui-url>'` (T5.5)

1. Parse `/namespaces/<ns>/workflows/<id>[/<run>]` (URL-decoded; ids may contain `/`).
2. Describe the execution; owning shard and history host; history summary (event counts, last events,
   failures); pending activities; task queue pollers and backlog; visibility row; PostgreSQL sessions of
   the Temporal role (`pg_stat_activity`: state, wait events); log lines from every role in the workflow's
   time window that mention its id.
3. Write `./investigate-<workflow>-<time>.md` and print it.

| Failure                    | Code / behaviour                                                         | Exit |
| -------------------------- | ------------------------------------------------------------------------ | ---- |
| Not a workflow URL         | `usage.invalid` with the expected shape                                   | 4    |
| Workflow not found         | `investigate.not_found` (namespace and id as parsed)                      | 1    |
| Datastore unreachable      | section says so; the report continues                                     | 0    |

`--json` is `usage.invalid` (the report is the document).

## Hidden

### `tops node <op>`

The helper. Refuses unless root (`helper.not_root`, exit 2) and a request decodes on stdin
(`helper.bad_request`, exit 2). Never listed in help or completion. Shapes in the backend schema.

## v1.1

### `tops -e <env> apply --at HH:MM [-y]` (T9.1)

1. Plan without the lock; print; confirm now (`apply at 02:00 local (20:30 UTC)? [y/N]`).
2. Wait in the foreground (a line every minute on a TTY); Ctrl-C before the time cancels with exit 130.
3. At the time: lock, re-plan, compare input hashes; differ: `refused.plan_stale` (exit 3); else execute
   without asking.

A time already passed today: `usage.invalid`.

### `tops -e <env> run <role> --node N` (T9.2)

Refused while `temporal@<role>` is active (`refused.role_running`). Streams the role's stdout/stderr until
Ctrl-C or exit; prints `exited <code>`; exit 0 on Ctrl-C, 1 if the process failed. The unit stays stopped and
disabled as it was.

# Part B: journeys

## 1. Settings: find a knob and change it

1. `tops knobs cache` lists every key containing `cache`: key, roles, `hot`/`restart`/`unknown`, default.
2. `tops knobs history.cacheTTL -e lab` shows the full entry, the evidence of its class, and the value at
   each layer.
3. The operator sets it where it belongs: every environment -> `base/history.yaml`; lab only ->
   `environments/lab/overrides.yaml`.
4. `tops -e lab validate`: the key exists in the 1.31 catalog, its value decodes, history reads it.
5. `tops -e lab plan`: `dynamicconfig-history.yaml` changes on vps-01 and vps-02; the effect follows the key's
   class (`hot-reload`, or `→ restarts history` for `restart` and `unknown`).

| Fails at                                  | tops does                                                       |
| ----------------------------------------- | --------------------------------------------------------------- |
| 4 key not in the catalog                  | `validate` error naming the closest keys (exit 4)               |
| 4 key in `base/matching.yaml` that matching does not read | Error: move it to the role that reads it or to `common.yaml` |
| 5 key class `unknown`                     | Planned as a restart; `plan` notes `history.cacheTTL: unclassified, restarting to be safe` |

## 2. First-time bootstrap

Precondition: `make lab-reset` then `make lab-up` (VPSLAB with the e2e node sizes and `vps-pg`); host keys
in the lab's `known_hosts`; an empty PostgreSQL that admits every node.

1. `tops init lab` creates `environments/lab/` and any missing `base/` files; the operator edits them.
2. `tops -e lab validate`, then `tops -e lab capacity`: per node and role, limits and pools; the DB budget
   against `vps-pg`'s `max_connections`; shard headroom.
3. `tops -e lab plan`: every file, exit 2; nothing changes on the nodes.
4. `tops -e lab bootstrap`: lock, preflight, plan, confirm, stage, schema, validate, role stages, namespaces.

```mermaid
sequenceDiagram
    participant Op
    participant tops
    participant H1
    participant H2
    participant H3
    participant DB
    participant F1
    Op->>tops: tops -e lab bootstrap
    tops->>H1: lock
    tops->>H2: lock
    tops->>H3: lock
    tops->>DB: via tunnel: cluster_metadata_info empty? max_connections
    tops-->>Op: plan, confirm?
    Op->>tops: y
    par stage
        tops->>H1: stage
        tops->>H2: stage
        tops->>H3: stage
    end
    tops->>H1: schema ensure
    H1->>DB: temporal-sql-tool create/setup/update
    par validate
        tops->>H1: validate per role
        tops->>H2: validate per role
        tops->>H3: validate per role
    end
    Note over tops,F1: stage 1: frontends
    tops->>H1: write, enable-now temporal@frontend
    tops->>F1: SERVING, frontend ring
    tops->>H2: write, enable-now temporal@frontend
    tops->>H3: write, enable-now temporal@frontend
    Note over tops,F1: stage 2: history
    tops->>H1: enable-now temporal@history
    tops->>F1: DescribeCluster, DescribeHistoryHost: 512 owned by vps-01
    tops->>H2: enable-now temporal@history
    tops->>F1: history ring 2/2, sum 512
    Note over tops,F1: stages 3-5: matching, worker, ui (T6)
    tops->>F1: RegisterNamespace
    tops-->>Op: healthy; LB and monitoring steps
```

Resume: the datastore's `cluster_metadata_info` names this spec's `cluster`; roles already healthy are
skipped, the rest start in stage order. Refused when it names another cluster, or rings have members outside
the spec.

Resume onto recreated nodes is the same command. After `make lab-renew` every node is new (no `temporal@*`
units, no `/var/lib/tops`) while `vps-pg` still holds the cluster: preflight accepts the nodes because the store
names this cluster; schema steps are no-ops; every node is staged and every role started in stage order; the
history stage gates on all 512 shards owned again. Namespaces and workflows survive in the store. The new
host keys come from `make lab-renew`, which runs VPSLAB's `make up` and rewrites `lab/keys/known_hosts` from
the containers; tops still refuses any key not in that file. Same addresses are required: a node with a new
address is a `node add`, not a resume.

## 3. Plan and apply, one role on one node at a time

1. Operator edits `base/`, `overrides.yaml` or `cluster.yaml`, commits.
2. `tops -e lab plan`: per node and role, file diffs, effects, drift; exit 2.
3. `tops -e lab apply`: lock, re-plan under the lock, confirm, execute role-major with gates.

```mermaid
sequenceDiagram
    participant Op
    participant tops
    participant H1
    participant H2
    participant F1
    participant F2
    Op->>tops: tops -e lab apply
    tops->>H1: lock
    tops->>H2: lock
    par facts, validate
        tops->>H1: facts, validate
        tops->>H2: facts, validate
    end
    tops-->>Op: plan: history restarts on vps-01, vps-02; frontend hot-reload. confirm?
    Op->>tops: y
    par hot
        tops->>H1: write dynamicconfig-frontend.yaml
        tops->>H2: write dynamicconfig-frontend.yaml
    end
    Note over tops,F2: history on vps-01
    tops->>F2: gate before: history ring 2/2, 512 owned
    tops->>H1: write history files (snapshot), restart temporal@history
    loop every 2 s until stable twice or 3 min
        tops->>F1: DescribeCluster, DescribeHistoryHost
        tops->>F2: DescribeCluster
    end
    Note over tops,F2: history on vps-02
    tops->>F1: gate before
    tops->>H2: write (snapshot), restart temporal@history
    loop until stable
        tops->>F1: DescribeCluster, DescribeHistoryHost
    end
    tops->>H1: history record, unlock
    tops->>H2: history record, unlock
    tops-->>Op: done; undo with tops -e lab rollback <id>
```

Failures: see `apply` in part A. `--node N` limits writes to one node; the lock and the gate still cover the
whole cluster.

## 4. Restart a role

`tops -e lab restart history` (every node that runs it), `--node vps-02` (one), or `--all` (every role,
role-major). Refusal drill: `docker stop vps-02`, then `tops -e lab restart history --node vps-01` ->
`refused.ring_incomplete: history ring has 1 of 2 members: vps-02 is missing`, exit 3.

## 5. Change a node's roles

Move matching off vps-01 and back (T6.4):

1. Remove `matching` from vps-01's `roles`. `tops -e lab plan`: vps-01 `- stops matching`; vps-01's other
   roles get new limits (`→ restarts frontend, history, worker` because their `MemoryMax` grows); no other
   node changes (matching peers are learned through membership).
2. `tops -e lab apply`: role-major. vps-01 history restarts with its new limit (gate), then matching on vps-01
   stops (`disable --now`, Temporal drains; wait until it leaves the matching ring in every view), then its
   files are removed, then frontend and worker restart with new limits.
3. Add `matching` back: `+ starts matching` on vps-01, others shrink and restart; the new instance is gated
   into the ring.

| Fails at                                        | tops does                                     |
| ----------------------------------------------- | --------------------------------------------- |
| Removing the last instance of a role            | Refuse (`refused.last_role_instance`)         |
| Adding a role makes the node too small          | Refuse (`refused.node_too_small`) in `plan`   |

## 6. Node add and remove

`tops -e lab node remove vps-03`, delete it from `cluster.yaml`, `tops -e lab apply` (firewall on the
others); restore the spec entry; `tops -e lab node add vps-03`. Flows and refusals: part A. A node stopped by
`node remove` but still in the spec makes `apply` refuse (`refused.node_stopped`): `node add` it or delete
it from the spec.

## 7. Upgrade (`tops -e lab upgrade 1.31.3`)

Precondition: every node on 1.30.x.

1. Lock; live versions and schema versions. Path checks: supported, same or next minor, not a downgrade,
   at most two versions live.
2. `a backup of the datastore exists? [y/N]` (`-y` needs `--backup-confirmed`).
3. Stage 1.31.3 everywhere; render with the 1.31 catalog and sizing constants; validate with the 1.31.3
   binary on every node.
4. Schema from vps-01 with the 1.31.3 tool: temporal 1.18 -> 1.19, visibility 1.13 -> 1.14. Old binaries keep
   running on the newer schema.
5. Rolling, role-major: gate, switch `current` and files, restart, gate.
6. Rewrite `temporal.version: 1.31.3` in `environments/lab/cluster.yaml` (comments kept). Print: commit it,
   then `tops diff lab staging` shows the version as promotable and `tops -e staging upgrade 1.31.3` as next.

## 8. Environments: diff, promote, apply

1. `tops diff lab staging`: promotable differences first, exit 2. `-v` also lists by-design differences.
2. `tops promote lab staging --only settings,versions`: shows the edits, asks, writes (comments kept).
3. `git diff environments/staging`, commit.
4. `tops diff lab staging` exits 0 for the promoted groups.
5. `tops -e staging plan`, then `tops -e staging apply` (journey 3, against staging's own nodes) when
   staging has servers; while staging is files only, `validate` is the last step.

```mermaid
sequenceDiagram
    participant Op
    participant tops
    participant Git as fleet repo
    Op->>tops: tops diff lab staging
    tops->>Git: read environments/lab, environments/staging, base/
    tops-->>Op: promotable: ui.version, 1 override, 1 namespace (exit 2)
    Op->>tops: tops promote lab staging --only settings,versions
    tops-->>Op: writes 2 files. confirm?
    Op->>tops: y
    tops->>Git: write environments/staging/cluster.yaml, overrides.yaml
    Op->>Git: git diff; git commit
    Op->>tops: tops -e staging validate (plan when staging is live)
```

## 9. Drift detection

1. Someone edits `/etc/temporal/dynamicconfig-history.yaml` on vps-02 by hand.
2. `tops -e lab drift`: `vps-02 ~ /etc/temporal/dynamicconfig-history.yaml edited since 20261023T140155Z-3b7d`,
   exit 2.
3. `tops -e lab plan` shows the same line marked `drift` and the change back to the spec; `apply` restores it.
   temporalScale runs `drift --json` on a schedule and alerts on `drift: true`.

## 10. Incident: status, audit, top, logs, pprof, investigate

`status` (what is down) → `audit` (which layer chokes) → `top --role <r>` (is it memory or CPU) →
`logs --role <r> -E -t 30m` (what it says) → `pprof <r> --node <n> heap|cpu` (where it spends) →
`investigate '<url>'` (one workflow). All read-only, no lock, usable during an apply. Flows: part A.

## 11. Namespace ensure

`tops -e lab ns` shows managed and unmanaged namespaces; `tops -e lab ns ensure` creates and updates the
spec's; `apply` runs the same step after its rollout.

## 12. Rollback

1. `tops -e lab rollback` lists; nothing changes.
2. `tops -e lab rollback 20261023T153012Z-7f3a` restores, rolling with gates, and prints the new apply id.
3. The rollback is itself undoable. The spec still describes the rolled-back change: fix the spec too.
   Binary rollback switches `current`; the schema never goes back.

# Part C

## What tops refuses (index)

Every row has an e2e test by T8 (`TestRefuse<Name>`); v2 rows by T10.

| Refusal                                                          | Commands                     | Code                         | Since |
| ---------------------------------------------------------------- | ---------------------------- | ---------------------------- | ----- |
| No TTY and no `-y`; `--json` without `-y` on a mutating command  | all mutating, promote        | `refused.no_tty`             | T3.8  |
| Touch a role while its ring is incomplete or shards unowned      | apply, restart, node, upgrade, rollback | `refused.ring_incomplete` | T3.7 |
| Restart with no other frontend serving                           | apply, restart, upgrade      | `refused.last_frontend`      | T3.7  |
| Stop or remove the last instance of a role                       | apply, node remove           | `refused.last_role_instance` | T4.1  |
| Immutable differs from the live cluster                          | plan, apply                  | `refused.immutable`          | T4.1  |
| Persistence address reaches a store without the live cluster     | plan, apply                  | `refused.store_identity`     | T4.1  |
| Node too small for its roles and pins                            | capacity, plan, bootstrap, apply, node add | `refused.node_too_small` | T2.4 |
| Shard headroom with one history node down                        | capacity, plan, bootstrap, apply, node | `refused.headroom` | T2.4  |
| Pools above the PostgreSQL connection budget                     | capacity, plan, bootstrap, apply, node add | `refused.db_budget` | T2.4 |
| Rendered config rejected by the target binary                    | plan, bootstrap, apply, upgrade, node add | `refused.render_rejected` | T3.2 |
| Version change through `apply`                                   | plan, apply                  | `refused.version_change`     | T4.1  |
| Upgrade skipping a minor, downgrading, unsupported               | upgrade                      | `refused.upgrade_path`       | T7.2  |
| Upgrade without backup confirmation                              | upgrade                      | `refused.no_backup`          | T7.2  |
| Ring member not in the spec                                      | plan, apply                  | `refused.unknown_member`     | T4.1  |
| Lock held by a live run                                          | all mutating                 | `lock.held`                  | T3.4  |
| Bootstrap into a datastore holding another cluster               | bootstrap                    | `refused.datastore_in_use`   | T3.8  |
| Bootstrap on a node running Temporal; `node add` of a member     | bootstrap, node add          | `refused.node_in_use`        | T3.8  |
| Ports tops needs are in use                                      | bootstrap, node add          | `refused.ports_in_use`       | T3.8  |
| Node OS is not Ubuntu 24.04                                      | bootstrap, node add          | `refused.unsupported_os`     | T3.8  |
| `node add` of a node not in the spec                             | node add                     | `refused.not_in_spec`        | T6.5  |
| `apply` to a node stopped by `node remove`                       | plan, apply                  | `refused.node_stopped`       | T6.6  |
| `--force` with `-y`                                              | apply, restart               | `refused.force_with_yes`     | T4.3  |
| Rollback without a complete snapshot                             | rollback                     | `refused.not_rollbackable`   | T4.4  |
| Promotion of a key unknown to the target's minor (without `--partial`) | promote                | `refused.unknown_setting`    | T4.6  |
| Scheduled apply whose inputs changed (v1.1)                      | apply --at                   | `refused.plan_stale`         | T9.1  |
| `run` on a role whose unit is active (v1.1)                      | run                          | `refused.role_running`       | T9.2  |
