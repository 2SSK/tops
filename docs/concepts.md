# tops concepts

Project: tops (Temporal Ops), `github.com/2SSK/tops`. Status: draft. Date: 2026-10-10.
Sibling docs: [PRD](prd.md) · [TRD](trd.md) · [App flow](app-flow.md) · [Design brief](design-brief.md) ·
[Backend schema](backend-schema.md) · [Implementation plan](implementation-plan.md)

What you must be able to explain, without notes, to own tops in an interview. This is the "learn first"
spine of the [implementation plan](implementation-plan.md): read the link before the task that uses it.
Test yourself with the question in the last column.

## Temporal

| Concept | Why it matters here | Read | Can you answer? |
| ------- | ------------------- | ---- | --------------- |
| The four roles (frontend, history, matching, worker) | tops renders one process per node with its role set and gates restarts per role | https://docs.temporal.io/temporal-service/temporal-server | Which role owns a workflow's state? Which one only runs system workflows? |
| Membership (ringpop gossip, seeds from the `cluster_membership` table) | The ring gate reads each node's view of every ring; views can disagree in a partition | https://docs.temporal.io/temporal-service/temporal-server | Why does tops read every node's `DescribeCluster` instead of one? |
| History shards and ownership | "512 of 512 owned" is the health invariant behind every gate; the count is fixed at creation | https://docs.temporal.io/temporal-service/temporal-server | Why can the shard count never change, and what moves when a node leaves? |
| Cluster immutables (cluster name, shard count, store identity) | tops refuses changes that would make Temporal create a second, empty cluster (N1) | TRD "Persistence identity"; https://docs.temporal.io/references/configuration | What happens if a running cluster is pointed at a new keyspace? |
| Schema versions and upgrades (one minor at a time, schema first) | `tops upgrade` migrates once, then rolls binaries; rollback never touches the schema | https://docs.temporal.io/self-hosted-guide/upgrade-server | Why can a 1.30 binary run on the 1.31 schema, but not the reverse? |
| Graceful shutdown and drain (`shutdownDrainDuration`, failed health first) | A restart moves shards and pollers; the drain settings make it clean | https://docs.temporal.io/references/dynamic-configuration | What does a load balancer see during a frontend restart, second by second? |
| Static vs dynamic config | Decides the plan's effect class: restart vs hot reload | https://docs.temporal.io/references/dynamic-configuration | Which change in a plan restarts nodes, and which is picked up in 10 s? |
| Namespaces and retention | `ns ensure` owns spec namespaces and never deletes others | https://docs.temporal.io/namespaces | Why does a new namespace appear on other frontends only after ~10 s? |

## Linux hosts

| Concept | Why it matters here | Read | Can you answer? |
| ------- | ------------------- | ---- | --------------- |
| systemd units, drop-ins, `daemon-reload`, job results | Every service tops manages is a unit; a restart is a job that can fail | https://www.freedesktop.org/software/systemd/man/latest/systemd.service.html | Why must a unit change be followed by `daemon-reload` before the restart? |
| cgroup v2 memory (`memory.high`, `memory.max`, events) and PSI | `top` reads them; the fit check needs the effective limit, not `/proc/meminfo` | https://docs.kernel.org/admin-guide/cgroup-v2.html | What happens at `MemoryHigh`, at `MemoryMax`, and at a container's `mem_limit`, in that order? |
| `GOMEMLIMIT` and `GOMAXPROCS` | Profiles set them below the cgroup limits so the Go GC works before the kernel kills | https://go.dev/doc/gc-guide | Why is `GOMEMLIMIT` a soft limit and `MemoryMax` a hard one? |
| iptables chains, atomic `iptables-restore`, conntrack | tops owns one chain and replaces it atomically without dropping established connections | https://man7.org/linux/man-pages/man8/iptables-restore.8.html | Why does the `ESTABLISHED,RELATED` rule come before `DROP`? |
| Atomic file replacement (`rename(2)`, `fsync`) | No partial config on a node, even if the helper is killed | https://man7.org/linux/man-pages/man2/rename.2.html | Why write to a temp file in the same directory, not in `/tmp`? |

## Remote operation and safety

| Concept | Why it matters here | Read | Can you answer? |
| ------- | ------------------- | ---- | --------------- |
| SSH host keys and `known_hosts` (no trust on first use) | Every connection; a changed key is refused | https://man.openbsd.org/sshd#SSH_KNOWN_HOSTS_FILE_FORMAT | What attack does strict host-key checking stop, and how does the lab get keys safely? |
| SSH port forwarding (`direct-tcpip`) | All Temporal, metrics and database reads go through tunnels; only port 22 is open | https://www.rfc-editor.org/rfc/rfc4254#section-7.2 | Why does `audit` keep working after the workstation leaves `access.monitors`? |
| sudo and the root boundary | The helper runs as root; a restricted sudoers rule would not be a boundary (D9, Q12) | https://www.sudo.ws/docs/man/sudoers.man/ | Why is "only allow `tops node *`" not a security control here? |
| Declarative desired state, plan, apply, drift | The whole operating model: spec = intent, nodes = reality, plan = the difference | https://developer.hashicorp.com/terraform/cli/commands/plan | How does tops tell drift (hand edit) from change (spec edit) without a state file? |
| Idempotency | Every helper op and every re-run of `apply`, `bootstrap`, `upgrade` converges | TRD "Invocation" | Why is "re-run the same command" the recovery for an interrupted apply? |
| Leases and fencing tokens | The cluster lock; a run that lost its lease must not write | https://martin.kleppmann.com/2016/02/08/how-to-do-distributed-locking.html | A laptop sleeps past its lease and wakes up mid-apply. What stops it? |
| Rolling change with a health gate | One node at a time, only when the rest of the ring is whole; stop at the first failure | https://sre.google/sre-book/release-engineering/ | Why "stable twice, 5 s apart" and not a single successful check? |
| gRPC health checking protocol | SERVING is per service name; it is the frontend's readiness signal | https://github.com/grpc/grpc/blob/master/doc/health-checking.md | Why does tops ask for `temporal.api.workflowservice.v1.WorkflowService` and not ""? |

## Metrics and delivery

| Concept | Why it matters here | Read | Can you answer? |
| ------- | ------------------- | ---- | --------------- |
| Prometheus counters, histograms and quantiles from two samples | `audit` computes rates and p99 itself from two scrapes | https://prometheus.io/docs/practices/histograms/ | How do you get a p99 from two scrapes of `_bucket` counters, and what does a counter reset look like? |
| File-based service discovery | `targets` makes scrape targets a function of the spec | https://prometheus.io/docs/prometheus/latest/configuration/configuration/#file_sd_config | Why can a label not replace the constant `512` in a recording rule? |
| pprof CPU profiles | `profile` attributes samples to roles by package | https://go.dev/doc/diagnostics#profiling | What does a CPU sample contain, and why is "persistence" its own bucket? |
| Supply chain: checksums, SBOM, provenance | The helper and every release tops downloads is verified; tops' own releases are attested | https://slsa.dev/spec/v1.0/levels | Who computes the checksum the node compares against, and why not the node? |
