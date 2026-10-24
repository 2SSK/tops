# tops concepts

Project: tops (Temporal Ops), `github.com/2SSK/tops`. Status: draft. Date: 2026-10-23.
Sibling docs: [PRD](prd.md) · [TRD](trd.md) · [App flow](app-flow.md) · [Design brief](design-brief.md) ·
[Backend schema](backend-schema.md) · [Implementation plan](implementation-plan.md) · [Roadmap](roadmap.md)

What you must be able to explain, without notes, to own tops in an interview. This is the "learn first" spine
of the [implementation plan](implementation-plan.md): read the link before the task that uses it, and test
yourself with the last column.

## Temporal

| Concept | Why it matters here | Read | Can you answer? |
| ------- | ------------------- | ---- | --------------- |
| The four roles | Each runs as its own unit; sizing, gates, restarts and metrics are per role | https://docs.temporal.io/temporal-service/temporal-server | Which role owns a workflow's state? Which only runs system workflows? |
| Membership (ringpop, seeded from `cluster_membership`) | The gate reads every frontend's view of each role's ring; views can disagree | https://docs.temporal.io/temporal-service/temporal-server | Why does tops read every frontend's `DescribeCluster` instead of one? |
| History shards and ownership | "512 of 512 owned" is the invariant behind the history gate; the count is fixed at creation | https://docs.temporal.io/temporal-service/temporal-server | What moves when a history node leaves, and why can the count never change? |
| Cluster immutables and store identity | tops refuses a change that would make Temporal create a second, empty cluster | TRD "Persistence identity" | What happens if a running cluster is pointed at a new database with the schema in place? |
| Schema versions and upgrades | Schema first, then binaries, one minor at a time; rollback never touches the schema | https://docs.temporal.io/self-hosted-guide/upgrade-server | Why can a 1.30 binary run on the 1.31 schema, but not the reverse? |
| Graceful shutdown and drain | A role restart moves shards or partitions; drain settings make it clean; `TimeoutStopSec` must exceed them | https://docs.temporal.io/references/dynamic-configuration | What does a client see, second by second, while a frontend restarts? |
| Static vs dynamic config, and property functions | Decides hot vs restart. A setting hands out a function; whether the consumer calls it per use or once is decided at the call site | TRD "Settings catalog"; `common/dynamicconfig/setting.go` | Why is `history.cacheTTL` a restart although it is a dynamic-config key? |
| Per-role scaling signals | Which role to add where: frontend with request rate and latency, history with shard load and persistence latency, matching with backlog and sync-match rate, worker with system workflow backlog | https://docs.temporal.io/references/cluster-metrics | Schedule-to-start latency rises while history latency is flat. Which role do you add, and on which node? |
| Namespaces and retention | `ns ensure` owns the spec's namespaces and never deletes others | https://docs.temporal.io/namespaces | Why does a new namespace appear on other frontends only after a few seconds? |

## Linux hosts

| Concept | Why it matters here | Read | Can you answer? |
| ------- | ------------------- | ---- | --------------- |
| systemd units, drop-ins, `daemon-reload`, jobs | Every service tops manages is a unit; a restart is a job that can fail | https://www.freedesktop.org/software/systemd/man/latest/systemd.service.html | Why must a unit change be followed by `daemon-reload` before the restart? |
| systemd template units (`temporal@.service`, `%i`, instance drop-ins) | One file, one instance per role; `temporal@history.service.d/limits.conf` sizes only history | https://www.freedesktop.org/software/systemd/man/latest/systemd.unit.html | How does `systemctl enable temporal@history` work without a `temporal@history.service` file? |
| cgroup v2 limits: `MemoryHigh`, `MemoryMax`, `CPUWeight` | Per-role limits; weight is priority under contention, never a cap | https://www.freedesktop.org/software/systemd/man/latest/systemd.resource-control.html | Two roles at CPUWeight 200 and 100 on a busy 2-core node: who gets what, and what changes when the node is idle? |
| cgroup v2 accounting: `memory.current/peak/events`, `cpu.stat`, PSI | `top` and the sizing measurements read them | https://docs.kernel.org/admin-guide/cgroup-v2.html | What happens at `MemoryHigh`, at `MemoryMax`, and at a container's `mem_limit`, in that order? |
| Effective size: cgroup-aware detection | A VPSLAB node's `/proc/meminfo` and `nproc` are the workstation's; the limits are in its cgroup | TRD "Detection" | Why take the minimum over PID 1's cgroup and its ancestors? |
| `GOMEMLIMIT`, GC, container-aware `GOMAXPROCS` | GOMEMLIMIT below MemoryHigh lets the Go GC work before the kernel reclaims; Go >= 1.25 sizes GOMAXPROCS from `cpu.max` | https://go.dev/doc/gc-guide , https://go.dev/doc/go1.25 | Why is `GOMEMLIMIT` soft and `MemoryMax` hard, and what is a GC storm? |
| iptables chains, atomic `iptables-restore`, conntrack | tops owns one chain and replaces it atomically | https://man7.org/linux/man-pages/man8/iptables-restore.8.html | Why does `ESTABLISHED,RELATED` come before `DROP`? |
| Atomic file replacement (`rename(2)`, `fsync`) | No partial config on a node | https://man7.org/linux/man-pages/man2/rename.2.html | Why write the temp file in the same directory? |

## Remote operation and safety

| Concept | Why it matters here | Read | Can you answer? |
| ------- | ------------------- | ---- | --------------- |
| SSH host keys, no trust on first use | Every connection; a changed key is refused (H3) | https://man.openbsd.org/sshd#SSH_KNOWN_HOSTS_FILE_FORMAT | What attack does strict checking stop, and how does VPSLAB give you keys safely? |
| SSH port forwarding (`direct-tcpip`) | Temporal, metrics, pprof and database reads go through tunnels | https://www.rfc-editor.org/rfc/rfc4254#section-7.2 | Why does `audit` work although the workstation is not in `access.monitors`? |
| sudo and the root boundary | The helper runs as root; a sudoers path rule is no boundary | https://www.sudo.ws/docs/man/sudoers.man/ | Why is "only allow `tops node *`" not a control here? |
| Declarative state, plan, apply, drift | Spec = intent, nodes = reality; drift = reality changed behind tops' back | https://developer.hashicorp.com/terraform/tutorials/state/resource-drift | How does tops tell a hand edit from a spec edit without a state file? |
| Leases and fencing | The cluster lock; a run that lost its lease must not write (H4). On each node, `flock` makes the fencing check and the write one step | https://martin.kleppmann.com/2016/02/08/how-to-do-distributed-locking.html , https://man7.org/linux/man-pages/man2/flock.2.html | A laptop sleeps past its lease and wakes mid-apply. What stops it? Why is checking the lease, then writing, not enough without the flock? |
| Lock before plan | The plan you confirm is the plan that runs (D21) | https://developer.hashicorp.com/terraform/language/state/locking | What can change between "plan, confirm" and "lock" if the lock comes last? |
| Rolling change with a health gate | One role on one node at a time, only when its ring is otherwise whole | https://sre.google/sre-book/release-engineering/ | Why "stable twice, 5 s apart", and why history before worker? |
| gRPC health checking | SERVING is per service name | https://github.com/grpc/grpc/blob/master/doc/health-checking.md | Why ask for `temporal.api.workflowservice.v1.WorkflowService` and not ""? |
| Idempotency | Every helper op and every re-run converges | TRD "Helper upload and invocation" | Why is "run the same command again" the recovery for an interrupted apply? |

## Environments and promotion

| Concept | Why it matters here | Read | Can you answer? |
| ------- | ------------------- | ---- | --------------- |
| Base and overlays | `base/` is shared, `overrides.yaml` holds only what differs; a plain merge, no templating | https://kubectl.docs.kubernetes.io/references/kustomize/glossary/#overlay | Why is "only the differences" in the overlay the property that makes `diff` meaningful? |
| Environment promotion (GitOps) | A change proven in lab moves to staging as a reviewed commit, then a plan and an apply there | https://opengitops.dev/ | Why does `promote` edit files instead of copying a running cluster's config? |
| Promotable vs by-design differences | Versions and settings should converge; names, nodes and addresses never do | TRD "Diff and promote" | Why does `promote` refuse `temporal.version`? |
| Separate clusters per environment | Nothing shared at runtime; one bad apply reaches one environment | PRD "Environments" | What would sharing one PostgreSQL between lab and staging break? |

## Building and shipping Go

| Concept | Why it matters here | Read | Can you answer? |
| ------- | ------------------- | ---- | --------------- |
| Static binaries and cgo | The helper built on your workstation must run on Ubuntu 24.04 (P0.1) | https://pkg.go.dev/cmd/cgo | Why does a `net`-importing binary built with cgo on Arch fail on Ubuntu, and what does `CGO_ENABLED=0` change? |
| Build stamping | `tops version` and every `--json` document report the real version (P0.2) | https://pkg.go.dev/cmd/link , https://pkg.go.dev/runtime/debug#BuildInfo | Why can `-X` not set a `const`, and where does `vcs.revision` come from? |
| Content-addressed artefacts | The helper lives at `/usr/local/lib/tops/<sha16>/`: no version comparison, no collisions between dev builds | TRD "Build and version" | Why is a hash a better directory name than a version string for the helper? |
| Multi-module repositories | The catalog generator has its own `go.mod` (D23) | https://go.dev/doc/modules/managing-source#multiple-module-source | Why can one `go.mod` not load Temporal 1.30 and 1.31 at once, and what does `./...` skip? |

## Code generation

| Concept | Why it matters here | Read | Can you answer? |
| ------- | ------------------- | ---- | --------------- |
| `go/ast`, `go/parser` | The catalog is read from Temporal's source, not its runtime | https://pkg.go.dev/go/ast | Why can tops not list the settings by importing `dynamicconfig`? |
| `go/types` and `go/packages` | Constant defaults and "which role references this variable" need type information | https://github.com/golang/example/tree/master/gotypes | How do you find every use of a package-level variable across a module? |
| `go generate` and reproducible output | The catalog is regenerated and diffed in CI | https://go.dev/blog/generate | Why must the generator's output be sorted and timestamp-free? |

## Metrics, profiles, delivery

| Concept | Why it matters here | Read | Can you answer? |
| ------- | ------------------- | ---- | --------------- |
| Counters, histograms, quantiles from two samples | `audit` computes rates and p99 itself | https://prometheus.io/docs/practices/histograms/ | How do you get a p99 from two scrapes, and what does a counter reset look like? |
| File-based service discovery | `targets` makes scrape targets a function of the spec | https://prometheus.io/docs/prometheus/latest/configuration/configuration/#file_sd_config | Why can a label not replace the constant `512` in a recording rule? |
| pprof (CPU and heap) | `pprof` reads one role's loopback port | https://pkg.go.dev/net/http/pprof | What is the difference between `inuse_space` and `alloc_space`? |
| Measurement discipline | Sizing constants and audit thresholds come from recorded lab runs | TRD "Constants and how they are measured" | Why fix the load before looking at the numbers? |
| Supply chain: checksums, SBOM, provenance | The helper and every release are verified | https://slsa.dev/spec/v1.0/levels | Who computes the checksum the node compares against, and why not the node? |

## v2: Cassandra and Elasticsearch

| Concept | Why it matters here | Read | Can you answer? |
| ------- | ------------------- | ---- | --------------- |
| Replication factor, `NetworkTopologyStrategy`, snitch and DC | The keyspace names a DC; the snitch must report it | https://cassandra.apache.org/doc/5.0/cassandra/architecture/dynamo.html | What happens to a `{dc1: 3}` keyspace on nodes that report `datacenter1`? |
| Quorum and LWT (`LOCAL_QUORUM`, `LOCAL_SERIAL`) | Shard ownership is a conditional write; RF 3 survives one node, not two | https://cassandra.apache.org/doc/5.0/cassandra/architecture/guarantees.html | Why does losing two of three Cassandra nodes stop every persistence call? |
| Joining nodes one at a time; repair | The lab's ordered start; production's repair duty | https://cassandra.apache.org/doc/5.0/cassandra/managing/operating/topo_changes.html | Why must a new node not list itself as a seed? |
| ES templates before indices; health colours | Schema order is a contract; yellow is not data loss | https://www.elastic.co/guide/en/elasticsearch/reference/8.19/indices-templates-v1.html | What does an index created before its template get? |
| Visibility is asynchronous | Executions continue without ES; visibility catches up | https://docs.temporal.io/visibility | A workflow completed 200 ms ago is not in the list. Name two reasons. |
