# Changelog

All notable changes to tops are documented here.
The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)
and the project uses [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

- `tops` root command with help grouped into Spec, Change, Look and Integrate.
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
- `make generate` rebuilds each settings catalog from Temporal's source; CI fails if the committed catalog is stale.

[Unreleased]: https://github.com/2SSK/tops/commits/main
