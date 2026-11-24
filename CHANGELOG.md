# Changelog

All notable changes to tops are documented here.
The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)
and the project uses [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

- The node helper's `stage` op, which `bootstrap` will run: it creates the `temporal` system user (home `/var/lib/temporal`, 0750, no shell) and installs the server and CLI releases. The workstation reads each archive's sha256 from the release's `checksums.txt` over TLS; the node downloads the archive and refuses one that does not match (`release.checksum`). Only the named binaries are extracted, into a temporary directory renamed into place, so a half-installed release never looks installed. `/opt/temporal/current` is set on the first install only; the newest 3 server releases are kept. A second run changes and downloads nothing.
- Helper ops time out after 60 s, `stage` and `schema` after 10 min. The helper ends its own op then, so a fenced op never holds the node longer, even when the workstation is gone; the workstation waits 15 s more for the answer.
- The node helper's `schema` op: with the staged release's `temporal-sql-tool`, it creates each database the workstation found missing, versions it from 0.0 and migrates it to the schema the tool embeds; a second run changes nothing. The password reaches the tool only as `SQL_PASSWORD`, never in argv; a failed step is `schema.failed` with every error the tool logged, PostgreSQL's reason first, the password masked also as a connection URL escapes it. Helper `schema` times out after 10 min.
- The node helper's `lock` op: a lease in `/var/lib/tops/lock` (TTL by the node's clock) that a run acquires, renews and releases. Another run's live lease is refused with its holder; a free or stale one is taken only after a fenced op still running on the node has ended; a renewal past the TTL is refused; a run never releases another's lease. The lease always names the request's run id.
- Fencing: `stage` and `schema` run only for the run whose lease is the node's and live, and hold the fence until they end; otherwise they change nothing and fail `lock.lost`, saying that no run holds the node, which run does, or how long ago the lease expired. `make helper-test` takes the lease before staging, and checks the refusal through SSH.
- An acquire that would take a free or stale lease while the previous run's fenced op is still writing waits for it, at most the op's 60 s, then fails `lock.busy` naming that run. `make helper-test` races four runs for one node (exactly one wins, the others are told which) and shows a takeover waiting for a stage that outlived its lease.
- `plan` validates every role's files with the spec's `temporal-server` on each node where that release is staged: `render-config` with exactly the role's environment (its output, which holds the password, is discarded) and `validate-dynamic-config`, in `/run/tops/<run-id>/`, removed afterwards. `temporal@.service` and each role's drop-in go through `systemd-analyze verify` as systemd will load them, without the unit files already on the node; a setting systemd would ignore is `refused.render_rejected` naming the file and line. A file the release refuses is `refused.render_rejected` (exit 3) naming the file, the release, the node and Temporal's whole message, with secrets masked, also where YAML quotes a value shortened to its first 7 characters. The header says `validated with temporal-server <v> on <n> nodes` only when every node accepted; a node without the release says `not validated: <v> not staged`.
- `make helper-test` validates every golden render with the real `temporal-server` 1.31.3 and the container's systemd, and a broken config, dynamic config and drop-in per role and a broken unit, in the test container, and migrates both databases on PostgreSQL 17 from the node: created, then a no-op second run, pre-created ones owned by a user without CREATEDB, and a wrong password; the release is checked against `checksums.txt` and cached in `.cache/releases/`.

## [0.2.0] - 2026-11-20

### Added

- `tops -e <env> plan`: shows, per node and role, every file apply would create, update or remove (with a unified diff), which roles would start, stop or restart, and drift: files edited by hand since tops last wrote them. It changes nothing on a node (checked by the e2e suite). Exit 0 nothing to do, 2 changes or drift pending, 3 a refusal, 5 PostgreSQL unreachable. `--json` prints a `Plan` document. `temporal.env` diffs by key: values are `***`, a changed one `*** (changed)`; no secret appears on the page or in JSON.
- `tops -e <env> capacity`: each node's detected memory and cores (from its cgroup), each role's `MemoryMax`, `MemoryHigh`, `GOMEMLIMIT`, `CPUWeight` and database pools, the shard headroom if a history node fails, and the PostgreSQL connection budget. `--json` prints a `Capacity` document. The sizing constants are placeholders until measured on VPSLAB; the page says so.
- `nodes[].pin.<role>` fixes a role's `memoryMax`, `cpuWeight`, `dbPool` or `dbVisibilityPool` on one node instead of the formula.
- Refusals before anything is written: `refused.node_too_small`, `refused.headroom` and `refused.db_budget` (exit 3).
- Rendering per role: `/etc/temporal/<role>.yaml`, `<role>.env` (`GOMEMLIMIT`, `GOMAXPROCS`), `dynamicconfig-<role>.yaml` (the merged settings that role reads), `temporal@<role>.service.d/limits.conf`, the shared `temporal.env` and `temporal@.service`. Ports come from one table; any password stays valid YAML (`{{ env "TOPS_PG_PASSWORD" | quote }}`); roles find frontends through membership (no `publicClient`).
- SSH to the nodes: host keys only from `ssh.knownHosts` (no trust on first use, `ssh.host_key_unknown`), `ssh.key` or the agent, never a password; bounded dial, keepalive, preflight (`uname -m`, `sudo -n`).
- The node helper is uploaded once per build to `/usr/local/lib/tops/<sha16>/tops` and called over SSH with the request on stdin; every command tops sends a node is one of a fixed list. A released tops fetches the helper for another architecture from its release and checks it against `checksums.txt`.
- `tops -e <env> version` reports each node's helper; `--debug` logs every command tops runs and the helper's own log on stderr.
- PostgreSQL's connection limits are read through an SSH tunnel from the first node, never directly from the workstation.
- `make e2e` resets VPSLAB and runs the T2 acceptance against the binary; the reports are attached to this release.

### Fixed

- Built with Go 1.26.11 for the standard library's `net/http` fixes; `make vuln` scans with the toolchain in `go.mod`.

## [0.1.0] - 2026-11-12

### Added

- `tops` root command with help grouped by purpose; a group appears once it has commands.
- `tops version` and `tops completion`.
- Documented exit codes; Ctrl-C and SIGTERM cancel the run instead of killing it.
- Errors print as what happened, why, and how to fix it, on stderr.
- Unknown commands, flags and extra arguments exit 4 with a `usage.invalid` error.
- Coloured status marks on terminals; plain output for pipes, `NO_COLOR` and `TERM=dumb`.
- `--json`: failures print one `{"kind": "Error", ...}` document on stdout.
- `--no-color`: plain output on a terminal.
- `tops validate --json`: one `{"kind": "Validation", ...}` document with every problem's path and message.
- `tops version` prints the build's version, commit and Go version; `--json` prints a `Version` document.
- Nodes list the roles they run (`roles: [frontend, history, matching, worker]`); `validate` checks each role and that every role runs on some node.
- `tops -e <env> validate`: finds the fleet root (`TOPS_ROOT`, else the nearest parent with `environments/`) and checks `environments/<env>/cluster.yaml` offline; lists every problem; exits 4 when invalid. There is no default environment. Success prints ✓ environments/<env> is valid:
  cluster ..., N nodes, M roles; --json names env and cluster.
- `validate` checks `environments/<env>/secrets.env`: a regular file, mode 0600, owned by you (`secrets.mode`); one literal `KEY=VALUE` per line with every key the spec needs (`secrets.invalid`). Errors name the line and key, never the value.
- `tops knobs [text]`: searches the 567 dynamic-config settings of Temporal 1.31 offline, keys first, then descriptions. An exact key (any case) shows its type, precedence, roles and default; `-v` adds its constraints, Go source and a static hot/restart hint. No match suggests the closest keys. `--json` prints a `Knobs` document. Each setting shows `apply`: `hot` or `restart` for the keys the fleet sets (`internal/catalog/class-1.31.yaml`, citing the call site that reads them), else `unknown`, which plan restarts; `-v` shows the evidence and lab status.
- `validate` checks the dynamic-config files: `base/common.yaml`, `base/<role>.yaml` and `environments/<env>/overrides.yaml`, against the catalog of the spec's Temporal minor. Unknown keys (with the closest ones), wrong types, constraints the setting does not take, a key in a role file that role does not read, and stop settings that add up to systemd's `TimeoutStopSec` are errors, each with its file and line. A key in both `common.yaml` and a role file is a `!` warning and does not change the exit code.
- `tops knobs <key> -e <env>` shows where the key gets its value in that environment: the default, `base/common.yaml`, each `base/<role>.yaml`, the overrides, and what each role that reads it runs, from which file. `--json` adds `layers` to each setting. With `-e`, the catalog is that of the spec's Temporal minor.
- `tops diff <from> <to>` compares two environments offline, with defaults applied: `temporal`, `namespaces` and every overrides key decide the exit code (2 when any differs, else 0). `-v` lists the by-design differences such as nodes and persistence, which never affect it. `--json` prints an `EnvDiff` document. Both environments must be valid; `-e` is refused.
- `tops init <env>` creates `environments/<env>/` from a commented template, starting a fleet repo in the working directory when there is none: `cluster.yaml`, an empty `overrides.yaml`, a 0600 `secrets.env` with its keys commented, and any missing `base/` file. It never overwrites a file, and refuses an existing environment (`spec.env_exists`). `--json` prints an `Init` document.
- `validate` warns when git would commit `environments/<env>/secrets.env`, including one that is already tracked. Add `environments/*/secrets.env` to the fleet repo's `.gitignore`. Outside a git repository, or without git, the check is skipped.
- `make generate` rebuilds each settings catalog from Temporal's source; CI fails if the committed catalog is stale.
- `make cover` fails below 80% statement coverage overall, or below 90% for `safety`, `plan` and `sizing`; `make check` and CI run it.
- Releases: linux amd64/arm64 and darwin arm64 archives with shell completions, `checksums.txt`, SBOMs and build attestations. `go install` reports its module version.
- A hidden `tops node <op>` command: the node helper later versions run over SSH (`hello`, `facts`). It is internal, not for direct use, and its protocol may change.

[Unreleased]: https://github.com/2SSK/tops/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/2SSK/tops/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/2SSK/tops/releases/tag/v0.1.0
