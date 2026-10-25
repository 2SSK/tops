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
- `tops validate [-f cluster.yaml]`: checks the spec offline and lists every problem; exits 4 when invalid.
- `tops validate --json`: one `{"kind": "Validation", ...}` document with every problem's path and message.
- `tops version` prints the build's version, commit and Go version; `--json` prints a `Version` document.
- Nodes list the roles they run (`roles: [frontend, history, matching, worker]`); `validate` checks each role and that every role runs on some node.
- `tops -e <env> validate`: finds the fleet root (`TOPS_ROOT`, else the nearest parent with `environments/`) and checks `environments/<env>/cluster.yaml` offline; lists every problem; exits 4 when invalid. There is no default environment.

[Unreleased]: https://github.com/2SSK/tops/commits/main
