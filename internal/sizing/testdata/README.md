# Sizing measurements

Written before the first measured run, so the load cannot be tuned to the numbers.

## Reference load

| Field          | Value                                           |
| -------------- | ----------------------------------------------- |
| Temporal       | 1.31.3                                          |
| Tool           | omes `b82187e4a82d9c30e82bd7c1c29d7e7bfabbf611` |
| Scenario       | `workflow_with_single_noop_activity`            |
| Worker         | Go, on the workstation                          |
| Reference rate | 5 workflows/s for 15 minutes                    |
| Double rate    | 10 workflows/s for 15 minutes                   |
| Max concurrent | 200, so that only the rate limits the load      |
| Server address | a frontend on the node under test               |

## When a run counts

- omes finishes without a failed iteration, and started at least 99% of the target
  (4455 of 4500 at the reference rate, 8910 of 9000 at double).
- No role restarted, and the node's `memory.events` shows no `oom_kill`.
- The sampler's lowest workstation `memAvailableBytes` stayed above 1 GiB.
- The workstation is on AC power with the `balanced` power profile, the same for every run.

A run that fails a rule is kept and marked invalid. If the reference rate fails on both node
sizes, halve it once, here, with the date and the reason.

## Open findings of run vps-01-load-1 (2026-11-08, valid), decided 2026-11-20

1. **History memory rose about 9 MiB/min and never levelled off** (116 to 271 MiB in 15 minutes).
   Cause, from Temporal 1.31.3's `common/dynamicconfig/constants.go`: the mutable-state cache is
   bounded only by entry count (`history.hostLevelCacheMaxSize`, 128000) and the events cache by
   256 MiB (`history.eventsHostLevelCacheMaxSizeBytes`), both with a 1 h TTL. At 5 workflows/s a
   15-minute run reaches neither bound, so the peak measured only how long the run lasted.
   **Decision:** every measured cluster bounds both caches by bytes, so history's peak plateaus:
   `history.cacheSizeBasedLimit: true`, `history.hostLevelCacheMaxSizeBytes: 67108864` (64 MiB),
   `history.eventsHostLevelCacheMaxSizeBytes: 33554432` (32 MiB). The constants hold for a cluster
   with these bounds; `test/e2e/fleet/base/history.yaml` sets the same, and a fleet that raises
   them must raise history's pin with them.
2. **vps-pg reached its 512 MiB cap** (`memory.events max` 141, no `oom_kill`): 278 MiB disk cache,
   146 MiB shared buffers, 70 MiB processes. The page cache fills any cap and is reclaimed under
   pressure, so `memory.current` says nothing about what PostgreSQL needs.
   **Decision:** the sampler records vps-pg's `anon` and `shmem` from `memory.stat`; vps-pg is
   sized from their peak, with the nodes' headroom rule.
3. **`persistence_sql_in_use` was almost always 0** (41 of 990 samples above 0, at most 2). It is
   an instant gauge Temporal reports once a minute (`sqlplugin/db_metrics.go`), so it misses
   bursts. **Decision:** `conn_r` and `visConn_r` come from the peak of
   `persistence_sql_open_conn`. Temporal sets `maxIdleConns` to the pool and no idle timeout, and
   connections live an hour, so the open count only grows: its peak is the most connections the
   role ever needed at once. A run whose open count reaches `persistence_sql_max_open_conn` was
   capped by its pool and does not count for `conn_r`. The measured pools are 20 (default store)
   and 5 (visibility), 250 for the 10 roles, inside vps-pg's 295.
