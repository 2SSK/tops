# tops

### Temporal Orchestration Tool for SREs

tops is a Go-based CLI for deploying, operating, and investigating self-hosted Temporal clusters on Linux servers.

The goal is to manage Temporal infrastructure through a declarative configuration instead of a collection of manual commands and deployment scripts.

### Goals

- **Decalarative configuration**
- **Plan before applying**
- **Safe operations**
- **Remote management**
- **Operational visibility**
- **Incremental deployments**

### Architecture

- **CLI**: Runs on your workstation, reads configuration, plans changes, coordinates operations, and presents results.
- **Node helper**: Runs on a remote server to execute explicitly requested operations and return results.

## License

MIT — see [LICENSE](LICENSE).
