#!/usr/bin/env bash
# T2.1b: one measured run, as internal/sizing/testdata/README.md defines it. Restarts the cluster
# so that memory.peak belongs to this run, samples it, and prints whether the run counts.
#   usage: test/e2e/measure-run.sh <idle|load|double> <run number> <vps-01|vps-02|vps-03>
set -euo pipefail
cd "$(dirname "$0")/../.."

USAGE="usage: $0 <idle|load|double> <run number> <vps-01|vps-02|vps-03>"
PHASE=${1:?$USAGE}
RUN=${2:?$USAGE}
NODE=${3:?$USAGE}
NAME=$NODE-$PHASE-$RUN
ID=$NAME-$(date +%s)
DIR=test/e2e/samples
NODES=(vps-01 vps-02 vps-03)

case $NODE in
    vps-01) ADDR=10.80.0.11 ;;
    vps-02) ADDR=10.80.0.12 ;;
    vps-03) ADDR=10.80.0.13 ;;
    *) echo "$USAGE" >&2 && exit 2 ;;
esac

case $PHASE in
    idle) RATE=0 ;;
    load) RATE=5 ;;
    double) RATE=10 ;;
    *) echo "$USAGE" >&2 exit 2 ;;
esac

node() { ssh -F lab/keys/ssh_config "$@"; }

# oom_kill never goes back down, so a run compares its own before and after.
ooms() {
    for n in "${NODES[@]}"; do
        node "$n" awk "'\$1==\"oom_kill\" {print \$2}'" /sys/fs/cgroup/memory.events
    done | paste -sd+ | bc
}

restarts() {
    for n in "${NODES[@]}"; do
        node "$n" 'for u in frontend history matching worker; do systemctl show -p NRestarts --value m-$u; done'
    done | paste -sd+ | bc
}

mkdir -p "$DIR"
oom_before=$(ooms)
test/e2e/hand-cluster.sh stop
test/e2e/hand-cluster.sh start
sleep 60

failed=0
if [ "$RATE" = 0 ]; then
    # Idle, 10 minutes after start: the 60 s above and these 540 s.
    test/e2e/sample.sh 540 "$DIR/$NAME.jsonl"
else
    # 30 s of quiet before the load and after it, so a chart shows where it starts and stops.
    test/e2e/sample.sh 1000 "$DIR/$NAME.jsonl" &
    sampler=$!
    # Ctrl-C does not reach a background job, so stop the sampler when this script exits.
    trap 'kill "$sampler" 2>/dev/null || true' EXIT
    echo "$NAME: load until $(date -d +16min +%H:%M), then the summary"
    sleep 30
    go -C .cache/omes run ./cmd/omes run-scenario-with-worker \
        --scenario workflow_with_single_noop_activity --language go \
        --run-id "$ID" --server-address "$ADDR:7233" --namespace default \
        --duration 15m --max-iterations-per-second "$RATE" --max-concurrent 200 \
        >"$DIR/$NAME.log" 2>&1 || failed=1
    wait "$sampler"
fi

echo "$NAME"
[ "$failed" = 0 ] || echo "  INVALID: omes failed, see $DIR/$NAME.log"
if [ "$RATE" != 0 ]; then
    target=$((RATE * 900))
    completed=$(node vps-01 temporal workflow count --namespace default --address "$ADDR:7233" \
        --query "'TaskQueue=\"omes-$ID\" AND ExecutionStatus=\"Completed\"'" | awk '{print $2}')
    echo "  completed $completed, needs at least $((target * 99 / 100)) of $target"
    [ "$completed" -ge $((target * 99 / 100)) ] || echo "  INVALID: too few completed"
fi
r=$(restarts)
echo "  restarts $r"
[ "$r" = 0 ] || echo "  INVALID: a role restarted"
o=$(($(ooms) - oom_before))
echo "  oom kills $o"
[ "$o" = 0 ] || echo "  INVALID: oom_kill"
min_available=$(jq -s '[.[].memAvailableBytes // emtpy] | min' "$DIR/$NAME.jsonl")
echo "  lowest workstation memory $((min_available / 1048576)) MiB"
[ "$min_available" -gt 1073741824 ] || echo "  INVALID: workstation under 1 GiB"
