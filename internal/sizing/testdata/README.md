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

## The quick set (2026-11-21): `measure-2026-11-21.json`

Run on request as a short first set instead of the full matrix, with the conditions above except:
the `quiet` power profile (the laptop ran hot on `balanced`, with a browser using a core and a
half), one idle run of 3 minutes after 60 s, and one reference-load run of **5 minutes** (not 15)
at 5 workflows/s against vps-01's frontend. omes at the pinned commit; Temporal 1.31.3; caps
1280M/1.5 CPU (vps-01, vps-02), 768M/1 CPU (vps-03), vps-pg 512M.

| Rule                        | Result                                                        |
| --------------------------- | ------------------------------------------------------------- |
| Completed                   | 1499 of 1500 (needs 1485): valid                              |
| Restarts, `oom_kill`        | 0 on every node                                               |
| Lowest workstation memory   | 9367 MiB                                                      |
| CPU temperature             | 71 °C at most                                                 |

How each constant is read from the samples (`TestConstantsFromMeasurements` does exactly this):

| Constant   | Rule                                                                                       | Value                                    |
| ---------- | ------------------------------------------------------------------------------------------ | ---------------------------------------- |
| `osBase`   | idle: node `memory.current` minus its Temporal units, worst second of any node, up to a MiB | 444 MiB (vps-01; vps-02/03 about 275)    |
| `floor_r`  | idle: the role's highest `memory.current` on any node, up to a MiB                          | 37 / 130 / 42 / 40 MiB                   |
| `share_r`  | load: peak on an all-roles node minus the floor, normalised                                 | 0.224 / 0.657 / 0.065 / 0.054            |
| `burst_r`  | load: largest rise within 10 s on an all-roles node, of the role's peak                     | 0.054 / 0.046 / 0.065 / 0.028            |
| `nonHeap_r`| load: median of (`memory.current` − `memory_heapinuse`) / `memory.current`, worst node      | 0.536 / 0.45 / 0.478 / 0.386             |
| `cpu_r`    | load: CPU seconds over the run, worst node                                                  | 27.1 / 49.1 / 14.6 / 3.1                 |
| `conn_r`   | load: peak `persistence_sql_open_conn` / node CPUs × 1.5, worst node                        | 3 / **20** / **20** / 2                  |
| `visConn_r`| the same for the visibility store                                                           | 1.5 / 2 / 1.5 / 1                        |

(frontend / history / matching / worker.)

Not yet measured, kept as first guesses and to be measured before T8 relies on them:

- **`conn_r` of history and matching are lower bounds**: both filled their 20-connection pools on
  vps-01, so the rule above says they do not count. Re-measure with pools of 40.
- **`maxPool`** (50) needs the double-load run; **`perShard`** (256 KiB) needs the 128/256/512-shard
  clusters (`measure-run.sh shards-<n>`); `perShard` is `[]` in the file.
- **Floors were read with all roles together**, not each role alone (`measure-run.sh alone-<role>`).
- `osBase` includes the page cache of the Temporal binaries (about 170 MiB on vps-01, which also ran
  the schema tool and the CLI). Worst-kept makes it the safe high side.
- vps-pg: 270 MiB anon + shmem at load while `memory.current` sat at its 512 MiB cap (finding 2).

**S3 fit** (all four roles on vps-03's 768 MiB, plan T2.1b step 5): 768 − 444 − 249 = **75 MiB free:
fits**. **Caps** (PRD Q14): the lab contract's caps stay. With the measured constants every node
fits its roles (vps-03 has 245 MiB free above its floors), headroom for 512 shards holds (258 of
270 MiB), and vps-pg's 270 MiB at the reference rate leaves 242 MiB in its 512 MiB cap.
