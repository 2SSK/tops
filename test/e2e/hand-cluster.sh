#!/usr/bin/env bash
# T2.1a: a per-role Temporal cluster on VPSLAB, by hand. T2.1b measures this cluster, and
# T2.5's templates are rendered from what config() writes. Steps, in order, after make lab-up:
#   install  the release and the temporal user on every node, the password on each
#   database the schemas in vps-pg
#   config   one server config and one dynamic-config file per role
#   start    each role as a transient systemd unit, m-<role>
#   cli      the temporal CLI on vps-01
#   stop     every m-<role>
set -euo pipefail
cd "$(dirname "$0")/../.."

VERSION=1.31.3
SHA256=f2c3bf9f1115b506259e5972a62c38257296c043e15798358c3fb758625b96b8
CLI_VERSION=1.10.0
CLI_SHA256=521f461f3d108902763bb71e85cd0fd382dc9357b59604ae03da6ae734d16890
SECRETS=test/e2e/fleet/environments/lab/secrets.env
NODES=(vps-01 vps-02 vps-03)

node() { ssh -F lab/keys/ssh_config "$@"; }

# The roles of a node, in start order: the worker needs a frontend to talk to.
roles() {
	case $1 in
	vps-03) echo matching frontend ;;
	*) echo history matching frontend worker ;;
	esac
}

address() {
	case $1 in
	vps-01) echo 10.80.0.11 ;;
	vps-02) echo 10.80.0.12 ;;
	vps-03) echo 10.80.0.13 ;;
	esac
}

# The last digit of a role's ports: 6933, 7233, 7933 and 9233 for frontend.
digit() {
	case $1 in
	frontend) echo 3 ;;
	history) echo 4 ;;
	matching) echo 5 ;;
	worker) echo 9 ;;
	esac
}

install() {
	for n in "${NODES[@]}"; do
		node "$n" sudo bash -euo pipefail -s "$VERSION" "$SHA256" <<'EOF'
id temporal >/dev/null 2>&1 || useradd --system --create-home --home-dir /var/lib/temporal --shell /usr/sbin/nologin temporal
chmod 750 /var/lib/temporal
install -d /etc/temporal "/opt/temporal/releases/$1"
curl -fsSL -o /tmp/temporal.tgz "https://github.com/temporalio/temporal/releases/download/v$1/temporal_$1_linux_amd64.tar.gz"
echo "$2  /tmp/temporal.tgz" | sha256sum --check --quiet
tar -xzf /tmp/temporal.tgz -C "/opt/temporal/releases/$1" temporal-server temporal-sql-tool tdbg
rm /tmp/temporal.tgz
ln -sfn "/opt/temporal/releases/$1" /opt/temporal/current
EOF
		# Over stdin, so the password is never on a command line.
		node "$n" sudo install -m 600 /dev/stdin /etc/temporal/temporal.env <"$SECRETS"
		echo "$n installed"
	done
}

# The image already created the database "temporal" (POSTGRES_DB defaults to POSTGRES_USER),
# so only temporal_visibility needs create.
database() {
	node vps-01 sudo bash -euo pipefail -s <<'EOF'
set -a; . /etc/temporal/temporal.env; set +a
export SQL_PASSWORD=$TOPS_PG_PASSWORD
sql() { /opt/temporal/current/temporal-sql-tool --ep 10.80.0.20 -p 5432 -u temporal --pl postgres12 "$@"; }
sql --db temporal setup-schema -v 0.0
sql --db temporal update-schema --schema-name postgresql/v12/temporal
sql --db temporal_visibility create-database
sql --db temporal_visibility setup-schema -v 0.0
sql --db temporal_visibility update-schema --schema-name postgresql/v12/visibility
EOF
}

# A server config as backend-schema "Generated files" specifies it. {{ env }} is Temporal's
# own template: # enable-template must stay in the first 1 KB.
server_config() {
	local addr=$1 role=$2 d
	d=$(digit "$role")
	cat <<EOF
# enable-template
log: { stdout: true, level: info }
persistence:
  numHistoryShards: 512
  defaultStore: default
  visibilityStore: visibility
  datastores:
    default:
      sql:
        pluginName: postgres12
        databaseName: temporal
        connectAddr: "10.80.0.20:5432"
        connectProtocol: tcp
        user: "temporal"
        password: "{{ env "TOPS_PG_PASSWORD" }}"
        maxConns: 12
        maxIdleConns: 12
        maxConnLifetime: 1h
    visibility:
      sql:
        pluginName: postgres12
        databaseName: temporal_visibility
        connectAddr: "10.80.0.20:5432"
        connectProtocol: tcp
        user: "temporal"
        password: "{{ env "TOPS_PG_PASSWORD" }}"
        maxConns: 3
        maxIdleConns: 3
        maxConnLifetime: 1h
global:
  membership: { maxJoinDuration: 30s, broadcastAddress: "$addr" }
  pprof: { port: 793$d, host: 127.0.0.1 }
  metrics:
    prometheus: { framework: tally, timerType: histogram, listenAddress: "0.0.0.0:923$d" }
services:
  frontend: { rpc: { grpcPort: 7233, membershipPort: 6933, httpPort: 7243, bindOnIP: 0.0.0.0 } }
  history:  { rpc: { grpcPort: 7234, membershipPort: 6934, bindOnIP: 0.0.0.0 } }
  matching: { rpc: { grpcPort: 7235, membershipPort: 6935, bindOnIP: 0.0.0.0 } }
  worker:   { rpc: { grpcPort: 7239, membershipPort: 6939, bindOnIP: 0.0.0.0 } }
clusterMetadata:
  enableGlobalNamespace: false
  failoverVersionIncrement: 10
  masterClusterName: tops-e2e
  currentClusterName: tops-e2e
  clusterInformation:
    tops-e2e: { enabled: true, initialFailoverVersion: 1, rpcName: frontend, rpcAddress: 127.0.0.1:7233 }
dcRedirectionPolicy: { policy: noop }
dynamicConfigClient:
  filepath: /etc/temporal/dynamicconfig-$role.yaml
  pollInterval: 10s
EOF
}

config() {
	for n in "${NODES[@]}"; do
		for role in $(roles "$n"); do
			server_config "$(address "$n")" "$role" | node "$n" sudo install -m 640 -g temporal /dev/stdin "/etc/temporal/$role.yaml"
			echo '{}' | node "$n" sudo install -m 640 -g temporal /dev/stdin "/etc/temporal/dynamicconfig-$role.yaml"
		done
		echo "$n configured: $(roles "$n")"
	done
}

# vps-01 first: the first history to start writes the cluster's metadata. GOMAXPROCS is
# max(ceil(node CPUs), 2) for every node's cap: Go cannot see the container's cpu.max from here.
start() {
	for n in "${NODES[@]}"; do
		for role in $(roles "$n"); do
			node "$n" sudo systemd-run --unit="m-$role" --uid=temporal --gid=temporal \
				-p MemoryAccounting=yes -p Restart=on-failure -p RestartSec=5 -p LimitNOFILE=65536 \
				-p WorkingDirectory=/var/lib/temporal -p EnvironmentFile=/etc/temporal/temporal.env -p Environment=GOMAXPROCS=2 \
				/opt/temporal/current/temporal-server --config-file "/etc/temporal/$role.yaml" --allow-no-auth start --service "$role"
		done
	done
}

cli() {
	node vps-01 sudo bash -euo pipefail -s "$CLI_VERSION" "$CLI_SHA256" <<'EOF'
install -d "/opt/temporal/cli/$1"
curl -fsSL -o /tmp/cli.tgz "https://github.com/temporalio/cli/releases/download/v$1/temporal_cli_$1_linux_amd64.tar.gz"
echo "$2  /tmp/cli.tgz" | sha256sum --check --quiet
tar -xzf /tmp/cli.tgz -C "/opt/temporal/cli/$1" temporal
rm /tmp/cli.tgz
ln -sfn "/opt/temporal/cli/$1/temporal" /usr/local/bin/temporal
EOF
}

stop() {
	for n in "${NODES[@]}"; do
		for role in $(roles "$n"); do
			node "$n" sudo systemctl stop "m-$role" || true
		done
	done
}

case ${1:-} in
install | database | config | start | cli | stop) "$1" ;;
*)
	echo "usage: $0 install|database|config|start|cli|stop" >&2
	exit 2
	;;
esac
