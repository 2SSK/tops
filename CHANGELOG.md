# Changelog

All notable changes to tops are documented here.
The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)
and the project uses [Semantic Versioning](https://semver.org/).

## [Unreleased]

## Added

- `tops` root command with help grouped into Spec, Change, Look and Integrate.
- `tops version` and `tops completion`.
- Documented exit codes; Ctrl-C and SIGTERM cancel the run instead of killing it.
- Errors print as what happened, why, and how to fix it, on stderr.
- unknown commands, flags and extra arguments exit 4 with a `usage.invalid` error.
- Coloured status marks on terminals; plain output for pipes, `NO_COLOR` and `TERM=dumb`.
- `--json`: failures print one `{"kind": "Error", ...}` document on stdout.
- `--no-color`: plain output on a terminal.
- `tops validate [-f cluster.yaml]`: checks the spec offline and lists every problem; exits 4 when invalid.
- `tops validate --json`: one `{"kind": "Validation", ...}` document with every problem's path and message.
