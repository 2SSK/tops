# tops

### Temporal Orchestration Tool for SREs

tops is a Go-based CLI for deploying, operating, and investigating self-hosted Temporal clusters on Linux servers.

The goal is to manage Temporal infrastructure through a declarative configuration instead of a collection of manual commands and deployment scripts.

### Goals

- **Declarative configuration**
- **Plan before applying**
- **Safe operations**
- **Remote management**
- **Operational visibility**
- **Incremental deployments**

### Architecture

- **CLI**: Runs on your workstation, reads configuration, plans changes, coordinates operations, and presents results.
- **Node helper**: Runs on a remote server to execute explicitly requested operations and return results.

## Quickstart

tops 0.1 works offline: it checks a fleet repo and compares its environments. It does not touch a node yet.

Install a [release](https://github.com/2SSK/tops/releases), or with Go 1.26 or newer:

```sh
go install github.com/2SSK/tops/cmd/tops@latest
```

Start a fleet repo with its first environment:

```sh
mkdir fleet && cd fleet && git init
mkdir environments
echo 'environments/*/secrets.env' > .gitignore
tops init lab
```

Edit `environments/lab/cluster.yaml`, set the password in `environments/lab/secrets.env`, then:

```sh
tops -e lab validate                  # every problem, with its file and line
tops knobs cacheTTL                   # search Temporal's dynamic-config settings
tops knobs history.cacheTTL -e lab    # where that key gets its value in lab
tops init staging
tops diff lab staging                 # exit 2 when promotable values differ
```

Every command takes `--json` and has documented exit codes: 0 success, 1 failed, 2 differences to report, 3 refused, 4 invalid input, 5 unhealthy, 130 interrupted.

Temporal 1.31 settings are supported today; 1.30 specs are accepted but `validate` reports that their catalog is not there yet.

## License

MIT — see [LICENSE](LICENSE).
