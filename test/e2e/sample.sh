#!/usr/bin/env bash
# T2.1b: samples the running lab as JSON lines. Every second: each unit's and each node's
# cgroup files. Every 10 s: each role's metrics, vps-pg's cgroup, the workstation's memory.
#   usage: test/e2e/sample.sh <seconds> <out.jsonl>
set -euo pipefail
cd "$(dirname "$0")/../.."

SECONDS_TO_RUN=${1:?usage: $0 <seconds> <out.jsonl>}
OUT=${2:?usage: $0 <seconds> <out.jsonl>}
mkdir -p "$(dirname "$OUT")"
: >"$OUT"

# Runs on each node over one SSH session. read and printf are bash builtins, so a sample starts
# no process: the sampler must not change what it measures.
cgroup_loop() {
    cat <<'REMOTE'
sample() {
	local u=$1 d=$2 mem peak cpu
	[ -d "$d" ] || return 0
	read -r mem <"$d/memory.current"
  read -r peak <"$d/memory.peak"
  read -r _ cpu <"$d/cpu.stat"
	printf '{"t":"%(%FT%TZ)T","node":"%s","unit":"%s","memoryCurrentBytes":%s,"memoryPeakBytes":%s,"cpuUsageUsec":%s}\n' -1 "$NODE" "$u" "$mem" "$peak" "$cpu"
}
NODE=$(hostname)
export TZ=UTC
while :; do
	sample node /sys/fs/cgroup
	for r in frontend history matching worker; do
		sample "m-$r" "/sys/fs/cgroup/system.slice/m-$r.service"
	done
	sleep 1
done
REMOTE
}

# Prints one JSON line of a role's metrics, or nothing if its port does not answer.
metrics() {
    local node=$1 addr=$2 role=$3 port=$4
    curl -s --max-time 2 "$addr:$port/metrics" | awk -v t="$(date -u +%FT%TZ)" -v node="$node" -v unit="m-$role" '
		/^memory_heapinuse\{/ { heap = $2 }
		/^gomaxprocs\{/ { procs = $2 }
		/^persistence_sql_in_use\{db_kind="main"/ { sql = $2 }
		/^persistence_sql_in_use\{db_kind="visibility"/ { vis = $2 }
		END { if (procs != "") printf "{\"t\":\"%s\",\"node\":\"%s\",\"unit\":\"%s\",\"memHeapInuseBytes\":%.0f,\"sqlInUse\":%.0f,\"sqlVisInUse\":%.0f,\"gomaxprocs\":%.0f}\n", t, node, unit, heap, sql, vis, procs }
	' || true
}

slow_loop() {
    local pg
    pg=/sys/fs/cgroup/system.slice/docker-$(docker inspect -f '{{.Id}}' vps-pg).scope
    while :; do
        metrics vps-01 10.80.0.11 frontend 9233
        metrics vps-01 10.80.0.11 history 9234
        metrics vps-01 10.80.0.11 matching 9235
        metrics vps-01 10.80.0.11 worker 9239
        metrics vps-02 10.80.0.12 frontend 9233
        metrics vps-02 10.80.0.12 history 9234
        metrics vps-02 10.80.0.12 matching 9235
        metrics vps-02 10.80.0.12 worker 9239
        metrics vps-03 10.80.0.13 frontend 9233
        metrics vps-03 10.80.0.13 matching 9235
        printf '{"t":"%s","node":"vps-pg","unit":"vps-pg","memoryCurrentBytes":%s,"memoryPeakBytes":%s,"cpuUsageUsec":%s}\n' \
            "$(date -u +%FT%TZ)" "$(cat "$pg/memory.current")" "$(cat "$pg/memory.peak")" "$(awk '/^usage_usec/ { print $2 }' "$pg/cpu.stat")"
        printf '{"t":"%s","node":"workstation","memAvailableBytes":%s}\n' \
            "$(date -u +%FT%TZ)" "$(awk '/^MemAvailable:/ { print $2 * 1024 }' /proc/meminfo)"
        sleep 10
    done
}

# Whole lines only: each child appends one short line at a time.
trap 'kill $(jobs -p) 2>/dev/null || true' EXIT
for n in vps-01 vps-02 vps-03; do
    ssh -F lab/keys/ssh_config "$n" bash -s < <(cgroup_loop) >>"$OUT" &
done
slow_loop >>"$OUT" &
sleep "$SECONDS_TO_RUN"
