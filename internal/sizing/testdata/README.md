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

## Open findings (run vps-01-load-1, 2026-11-08, valid)

1. History memory rose about 9 MiB/min for the whole 15 minutes (116 to 271 MiB) and had not
   levelled off. The likely cause is its cache filling. The peak depends on how long the run lasts
   until the cache is bounded; decide that before the remaining runs.
2. vps-pg reached its 512 MiB cap (memory.events max 141, no oom_kill): 278 MiB disk cache,
   146 MiB shared buffers, 70 MiB processes. Size vps-pg from anon + shmem, not memory.current.
3. persistence_sql_in_use sampled every 10 s was almost always 0. Measure conn_r with another
   signal.
