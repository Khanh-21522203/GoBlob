# GoBlob - Design Index

GoBlob is a distributed blob storage system implemented in Go, shipped as a single `blob` CLI binary that can run multiple server roles (master, volume, filer, S3, WebDAV) plus admin/maintenance commands. Runtime behavior is split between HTTP/gRPC control-plane services, volume data-plane I/O, metadata persistence backends, and background loops (Raft leadership, heartbeats, replication, lifecycle, maintenance).

## Mental Map

```
┌─ CLI Command Runtime ──────────────────────────────┐  ┌─ Master Control Plane ─────────────────────────────┐
│ Owns: entrypoint, dispatch, SIGHUP reload          │  │ Owns: assign, lookup, topology coordination        │
│ Entry: goblob/blob.go                              │  │ Entry: goblob/server/master_server.go              │
│ Key:   goblob/command/command.go,                  │  │ Key:   goblob/server/master_server_handlers.go,    │
│        goblob/command/runtime.go                   │  │        goblob/server/master_grpc_server.go         │
│ Uses:  Security, Observability                     │  │ Uses:  Topology, Raft, Observability, Security     │
└────────────────────────────────────────────────────┘  └────────────────────────────────────────────────────┘

┌─ Volume Data Plane ────────────────────────────────┐  ┌─ Filer Metadata Service ───────────────────────────┐
│ Owns: blob write/read/delete, heartbeat, cache     │  │ Owns: namespace, HTTP/gRPC file APIs, log buffer   │
│ Entry: goblob/server/volume_server.go              │  │ Entry: goblob/server/filer_server.go               │
│ Key:   goblob/server/volume_grpc_server.go         │  │ Key:   goblob/server/filer_grpc_server.go,         │
│ Uses:  Needle Storage, Replication, Security, Obs  │  │        goblob/filer/filer.go, goblob/log_buffer/   │
└────────────────────────────────────────────────────┘  │ Uses:  Filer Backends, Operation, Security, Obs    │
                                                        └────────────────────────────────────────────────────┘

┌─ Needle Storage Engine ────────────────────────────┐  ┌─ Topology and Placement ───────────────────────────┐
│ Owns: .dat/.idx volume files, needle R/W/compact   │  │ Owns: in-memory tree, heartbeat joins, auto-grow   │
│ Entry: goblob/storage/store.go                     │  │ Entry: goblob/topology/topology.go                 │
│ Key:   goblob/storage/volume/volume.go,            │  │ Key:   goblob/topology/volume_layout.go,           │
│        goblob/storage/needle/needle.go             │  │        goblob/topology/volume_growth.go            │
└────────────────────────────────────────────────────┘  │ Uses:  (none internal)                             │
                                                        └────────────────────────────────────────────────────┘

┌─ Raft and ID Sequencing ───────────────────────────┐  ┌─ S3 Gateway and IAM ───────────────────────────────┐
│ Owns: consensus, max IDs, topology ID              │  │ Owns: S3 HTTP surface, SigV4, IAM, lifecycle       │
│ Entry: goblob/raft/raft_server.go                  │  │ Entry: goblob/s3api/server.go                      │
│ Key:   goblob/raft/fsm.go, goblob/raft/events.go,  │  │ Key:   goblob/s3api/auth/auth.go,                  │
│        goblob/sequence/raft_sequencer.go           │  │        goblob/s3api/iam/iam.go,                    │
└────────────────────────────────────────────────────┘  │        goblob/s3api/filer_client.go                │
                                                        │ Uses:  Filer, Quota, Observability                 │
┌─ Filer Store Backends ─────────────────────────────┐  └────────────────────────────────────────────────────┘
│ Owns: pluggable metadata persistence               │
│ Key:   goblob/filer/leveldb2/, goblob/filer/redis3/│  ┌─ Replication Pipelines ────────────────────────────┐
│        goblob/filer/postgres2/, mysql2/, cassandra/│  │ Owns: sync replica fan-out, async metadata repl    │
│        goblob/filer/storeloader/                   │  │ Key:   goblob/replication/replicator.go,           │
└────────────────────────────────────────────────────┘  │        goblob/replication/async/replicator.go      │
                                                        │ Uses:  (none internal)                             │
┌─ WebDAV Gateway ───────────────────────────────────┐  └────────────────────────────────────────────────────┘
│ Owns: WebDAV HTTP surface, filer FS adapter        │
│ Entry: goblob/webdav/webdav_server.go              │  ┌─ Admin Shell and Maintenance ──────────────────────┐
│ Uses:  Filer                                       │  │ Owns: interactive REPL, scheduled scripts          │
└────────────────────────────────────────────────────┘  │ Entry: goblob/shell/shell.go                       │
                                                        │ Key:   goblob/shell/command_*.go,                  │
┌─ Shared ───────────────────────────────────────────┐  │        goblob/command/maintenance.go               │
│ Owns: core types, security guard/JWT, metrics,     │  │ Uses:  Master, Filer, Volume (via gRPC)            │
│       config loading, Go client API, cluster reg,  │  └────────────────────────────────────────────────────┘
│       quota, cache, log buffer, wdclient           │
│ Key: goblob/core/types/, goblob/security/,         │
│      goblob/obs/, goblob/config/, goblob/operation/│
│      goblob/cluster/, goblob/quota/, goblob/cache/ │
│      goblob/log_buffer/, goblob/wdclient/          │
└────────────────────────────────────────────────────┘
```

## Feature Matrix

| Feature | Description | File | Status |
|---------|-------------|------|--------|
| CLI Command Runtime | Process entrypoint, command registry, signal reload hook, and command dispatch contracts. | [cli-command-runtime.md](cli-command-runtime.md) | Stable |
| Master Control Plane | Master HTTP/gRPC APIs for assign/lookup/topology and Raft peer administration. | [master-control-plane.md](master-control-plane.md) | Stable |
| Volume Data Plane | Volume HTTP/gRPC APIs for blob writes/reads/deletes, compaction, and heartbeat integration. | [volume-data-plane.md](volume-data-plane.md) | Stable |
| Filer Metadata Service | Filer HTTP/gRPC metadata namespace APIs, chunked upload/download, and distributed locks. | [filer-metadata-service.md](filer-metadata-service.md) | Stable |
| S3 Gateway and IAM | S3-compatible HTTP surface, SigV4 verification, IAM policy checks, lifecycle and multipart logic. | [s3-gateway-and-iam.md](s3-gateway-and-iam.md) | In Progress |
| WebDAV Gateway | WebDAV server and filer-backed filesystem adapter over filer gRPC. | [webdav-gateway.md](webdav-gateway.md) | Stable |
| Admin Shell and Maintenance | Interactive shell commands, maintenance actions, and scheduled script execution. | [admin-shell-and-maintenance.md](admin-shell-and-maintenance.md) | In Progress |
| Replication Pipelines | Synchronous volume replica write fan-out and async filer metadata replication pipeline. | [replication-pipelines.md](replication-pipelines.md) | In Progress |
| Topology and Volume Placement | In-memory topology tree, heartbeat joins, writable volume selection, and auto-grow allocation. | [topology-and-placement.md](topology-and-placement.md) | Stable |
| Raft and ID Sequencing | Raft state machine plus max-file-id and max-volume-id consistency guarantees. | [raft-and-id-sequencing.md](raft-and-id-sequencing.md) | Stable |
| Needle Storage Engine | On-disk `.dat`/`.idx` volume format, needle encoding, read/write/delete, and compaction swap. | [needle-storage-engine.md](needle-storage-engine.md) | Stable |
| Filer Store Backends | Pluggable metadata persistence for LevelDB, Redis, Postgres, MySQL, and Cassandra. | [filer-store-backends.md](filer-store-backends.md) | Stable |
| Go Client Operations API | Exported Go helpers for assign/upload/lookup/delete/chunk-upload client flows. | [go-client-operations.md](go-client-operations.md) | Stable |
| Security Hardening | Guard/JWT access checks and shared HTTP hardening middleware chain. | [security-hardening.md](security-hardening.md) | Stable |
| Observability and Metrics | Prometheus metrics registration, metrics/debug server, and pushgateway loop. | [observability-and-metrics.md](observability-and-metrics.md) | Stable |

## Cross-Cutting Concerns

Security is applied at multiple layers: route-level authorization (`goblob/security/guard.go`), S3 SigV4+IAM checks (`goblob/s3api/auth/auth.go`, `goblob/s3api/iam/iam.go`), and transport middleware (`goblob/security/hardening.go`). Data and metadata are separated by design: blob payloads live in volume files (`goblob/storage/volume/*.go`), while namespace and policy state live in filer stores (`goblob/filer/*`, `goblob/filer/*/*_store.go`). Leadership-sensitive paths (master assign/grow/peer changes) rely on Raft state (`goblob/raft/*.go`) and follower-to-leader proxying (`goblob/server/master_server_handlers.go`). Observability is shared through Prometheus collectors and per-service runtime wrappers (`goblob/obs/metrics.go`, `goblob/command/metrics_runtime.go`).

## Notes

None.
