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
