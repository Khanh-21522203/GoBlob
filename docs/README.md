# GoBlob Architecture Documentation

## High-Level System Overview

GoBlob is a distributed object store designed to store and serve billions of files efficiently. It is written in Go and implements a multi-tier architecture separating metadata management from data storage. The system provides an S3-compatible API.

The codebase lives under `goblob/` and is built as a single binary (`blob`) exposing multiple subcommands, each launching a distinct server role or utility.

## Full System ASCII Architecture Diagram

```
+-----------------------------------------------------------------------------------+
|                              Client Applications                                   |
|                    (S3 SDK / HTTP / gRPC / blob shell)                            |
+--------+--------------------------------+-----------+-----------------------------++
         |  S3 API                        |  HTTP/gRPC |  gRPC admin
         v                                v            v
+--------+-------+             +----------+------+  +-+------------+
|   S3 Gateway   |             |   Filer Server  |  |  Shell (CLI) |
|   :8333        |             |   :8888         |  |  blob shell  |
|   gRPC:18333   |             |   gRPC:18888    |  +-+------------+
+---+--------+---+             +--+----------+---+    |
    |        |                    |          |         |
    | /dir/  | blob read/write    | /dir/    | blob    | gRPC admin
    | assign |  (direct)          | assign   | read/   | commands
    | lookup |                    | lookup   | write   |
    |        |                    |          | (direct)|
    v        |                    v          |         v
+---+--------+--------------------+----------+---------+---------------------------+
|                     Master Server Cluster (Raft)                                   |
|                                                                                    |
|   +------------+      +------------+      +------------+                          |
|   |  Master 1  |<---->|  Master 2  |<---->|  Master 3  |   Raft consensus         |
|   |  (Leader)  |      | (Follower) |      | (Follower) |   log replication        |
|   |  :9333     |      |  :9333     |      |  :9333     |                          |
|   |  gRPC:19333|      |  gRPC:19333|      |  gRPC:19333|                          |
|   +------+-----+      +------------+      +------------+                          |
|          |                                                                         |
|   +---------------------------------------------------------------+               |
|   |  MasterServer internals (on leader)                           |               |
|   |                                                               |               |
|   |  +------------------+        +----------------------------+   |               |
|   |  |    Sequencer     |        |         Topology           |   |               |
|   |  | (global file ID  |        |  DC > Rack > DataNode >    |   |               |
|   |  |  generation)     |        |  Disk > [Volumes]          |   |               |
|   |  | - file-based     |        |                            |   |               |
|   |  | - raft-based     |        |  VolumeLayout              |   |               |
|   |  | - snowflake      |        |  (writable / full /        |   |               |
|   |  +------------------+        |   crowded tracking)        |   |               |
|   |                              |                            |   |               |
|   |                              |  VolumeGrowth              |   |               |
|   |                              |  (allocate new volumes     |   |               |
|   |                              |   when capacity low)       |   |               |
|   |                              +----------------------------+   |               |
|   +---------------------------------------------------------------+               |
+---+-----------------------------------------------------------------------+-------+
    |  /dir/assign (write path)                                     |
    |  /dir/lookup (read path)                                      |
    |  gRPC AllocateVolume (volume growth)                          |
    |  gRPC KeepConnected (heartbeat stream, every 5s)              |
    |                                                               |
    +-----------------------------+---------------------------------+
                                  |
              +-------------------+--------------------+
              |                                        |
              v                                        v
+-------------+-----------+              +-------------+-----------+
|      Volume Server 1    |              |      Volume Server N    |
|      :8080              |   . . .      |      :8080              |
|      gRPC:18080         |              |      gRPC:18080         |
|                         |              |                         |
|  +-------------------+  |              |  +-------------------+  |
|  |    DiskLocation 1 |  |              |  |    DiskLocation 1 |  |
|  |  [vol1] [vol2]    |  |              |  |  [vol5] [vol6]    |  |
|  |  (.dat + .idx)    |  |              |  |  (.dat + .idx)    |  |
|  +-------------------+  |              |  +-------------------+  |
|  +-------------------+  |              |  +-------------------+  |
|  |    DiskLocation 2 |  |              |  |    DiskLocation 2 |  |
|  |  [vol3] [vol4]    |  |              |  |  [vol7] [vol8]    |  |
|  |  (.dat + .idx)    |  |              |  |  (.dat + .idx)    |  |
|  +-------------------+  |              |  +-------------------+  |
+-------------------------+              +-------------------------+

+-----------------------------------------------------------------------------+
|                        Filer Metadata Backends                               |
|   (pluggable — Filer connects to exactly one at runtime)                    |
|                                                                              |
|   LevelDB | Redis | Cassandra | PostgreSQL | MySQL  | MongoDB               |
|   etcd    | TiKV  | YDB       | SQLite     | HBase  | FDB  | ArangoDB | ... |
+-----------------------------------------------------------------------------+
```

### Key Data Flow Paths

```
WRITE PATH:
  Client ──/dir/assign──► Master (Leader)
                              │  pick writable volume
                              │  return {fileId, volumeServerURL}
                              ▼
  Client ──PUT {fileId}──► Volume Server  (direct, bypasses Master)
                              │  write needle (.dat)
                              │  update index (.idx)
                              └─ replicate ──► replica Volume Servers

READ PATH:
  Client ──/dir/lookup──► Master (Leader)
                              │  return [{volumeServer URLs}]
                              ▼
  Client ──GET {fileId}──► Volume Server  (direct, bypasses Master)

S3 / FILER PATH:
  S3 Client ──S3 API──► S3 Gateway ──► Filer Server ──► Master (assign/lookup)
                                           │                      │
                                           │ metadata             │ blob I/O
                                           ▼                      ▼
                                     Metadata Backend       Volume Server

HEARTBEAT (every 5s):
  Volume Server ──gRPC KeepConnected──► Master
                  (volume list, disk status)
                  Master updates Topology tree
```

## Module Decomposition Tree

```
goblob/                        # Main binary entry point (blob.go)
+-- command/                   # CLI subcommand definitions
|   +-- server.go              #   "blob server" - all-in-one launcher
|   +-- master.go              #   "blob master" - standalone master
|   +-- volume.go              #   "blob volume" - standalone volume server
|   +-- filer.go               #   "blob filer"  - standalone filer
|   +-- s3.go                  #   "blob s3"     - standalone S3 gateway
|   +-- shell.go               #   "blob shell"  - admin CLI
|   +-- ...                    #   backup, export, benchmark, etc.
+-- server/                    # HTTP/gRPC server implementations
|   +-- master_server.go       #   MasterServer struct
|   +-- volume_server.go       #   VolumeServer struct
|   +-- filer_server.go        #   FilerServer struct
|   +-- raft_server*.go        #   Raft consensus layer
+-- storage/                   # Core storage engine
|   +-- store.go               #   Store (per volume-server)
|   +-- disk_location.go       #   DiskLocation (per directory)
|   +-- volume.go              #   Volume (.dat + .idx)
|   +-- needle/                #   Needle format (the blob unit)
|   +-- needle_map*.go         #   In-memory / LevelDB index
|   +-- super_block/           #   Volume header (8 bytes)
+-- topology/                  # Cluster topology
|   +-- topology.go            #   Topology > DC > Rack > DataNode
|   +-- volume_growth.go       #   Volume allocation strategy
|   +-- volume_layout.go       #   Writable volume tracking
+-- filer/                     # Metadata layer
|   +-- filer.go               #   Core Filer logic
|   +-- filerstore.go          #   FilerStore interface
|   +-- entry.go               #   Entry (file/dir metadata)
|   +-- leveldb2/ redis3/ ...  #   20+ pluggable backends
+-- s3api/                     # S3 API implementation
|   +-- s3api_server.go        #   S3ApiServer
|   +-- auth_*.go              #   IAM, SigV4
|   +-- s3api_bucket_*.go      #   Bucket operations
|   +-- s3api_object_*.go      #   Object operations
+-- shell/                     # Admin shell commands
+-- pb/                        # Protobuf definitions and helpers
+-- security/                  # JWT, TLS, Guard (whitelist)
+-- wdclient/                  # Master discovery client
+-- operation/                 # Client-side operations (assign, upload)
+-- util/                      # Shared utilities
```

## Runtime Lifecycle Overview

```
                    "blob server" (all-in-one)
                           |
          +----------------+----------------+
          |                |                |
    startMaster()   startVolumeServer()  startFiler()
          |                |                |
  +-------+-------+   +---+---+     +------+------+
  | Raft bootstrap|   | Load  |     | Load filer  |
  | NewTopology() |   | vols  |     | store config|
  | Sequencer init|   | from  |     | Connect to  |
  | gRPC server   |   | disk  |     | master      |
  | HTTP router   |   | gRPC  |     | gRPC server |
  +---------+-----+   | HTTP  |     | HTTP router |
            |          +---+---+     +------+------+
            |              |                |
            |         heartbeat()      MetaAggregator
            |          (periodic)       (peer sync)
            |              |                |
            +--------------+----------------+
                           |
              (optional: startS3Server)
                           |
                     select{} (block forever)
```

### Startup Sequence

1. `main()` in `blob.go` parses CLI args and dispatches to the matching `Command.Run`
2. `runServer()` in `command/server.go` loads security config, resolves peers
3. Master server starts first (Raft cluster bootstrap)
4. Volume server starts after master (with brief coordination delay to allow Raft bootstrap) (loads existing volumes from disk)
5. Filer starts after 1-second delay (connects to master, loads metadata store)
6. Gateway services (S3) start after 2-second delay
7. All servers block on `select{}` or context cancellation

### Graceful Shutdown

- `grace.OnInterrupt()` registers shutdown hooks for each server
- Volume server: stops heartbeats, waits `preStopSeconds`, then shuts down HTTP/gRPC
- Master server: calls `ms.Shutdown()`, stops gRPC
- Filer: calls `f.filer.Shutdown()` to flush metadata

## Deployment Assumptions

- **Single binary**: All server roles are compiled into one `blob` binary
- **Master HA**: Requires odd number of masters (1, 3, 5) for Raft quorum
- **Single-master mode**: Use `-peers=none` for development/standalone
- **Volume servers**: Scale horizontally; each manages local disk directories
- **Filer servers**: Stateless (metadata in external store); scale horizontally
- **S3 Gateway**: Stateless gateway fronting the Filer
- **Default ports**: Master=9333, Volume=8080, Filer=8888, S3=8333, gRPC=HTTP+10000

## Concurrency Model Summary

| Component | Concurrency Mechanism |
|---|---|
| Master Server | Raft consensus (single leader writes), gRPC streaming for heartbeats |
| Volume Server | `sync.RWMutex` per volume for data file access, async needle writer goroutine, concurrent upload/download limits via `sync.Cond` |
| Filer Server | Concurrent HTTP handlers, `sync.Cond` for metadata listener notification, background deletion queue (`UnboundedQueue`) |
| S3 Gateway | Circuit breaker pattern, concurrent upload/download limiting, `singleflight` for dedup |

## Documentation Index

### Core Servers
- [Master Server](core/master-server.md) - Cluster orchestrator, Raft consensus, volume assignment
- [Volume Server](core/volume-server.md) - Blob storage, heartbeat, read/write paths
- [Filer Server](core/filer-server.md) - File metadata, directory tree, pluggable stores

### Storage Engine
- [Storage Engine](storage/storage-engine.md) - Store, DiskLocation, Volume lifecycle
- [Needle Format](storage/needle-format.md) - Binary blob format, SuperBlock, index

### Topology
- [Topology and Replication](topology/topology-and-replication.md) - Cluster topology tree, volume growth, replica placement

### Gateways
- [S3 API Gateway](gateway/s3-api-gateway.md) - S3-compatible API, IAM, SSE, multipart

### Cross-Cutting Concerns
- [Security and Authentication](cross-cutting/security-and-auth.md) - JWT, TLS, IAM, Guard
- [gRPC and Protobuf](cross-cutting/grpc-and-protobuf.md) - Service definitions, streaming
- [Shell and CLI](cli/shell-and-commands.md) - Admin shell, maintenance scripts

### Advanced Features
- [Erasure Coding](advanced/erasure-coding.md) - Reed-Solomon encoding/decoding package and CLI planning
- [Tiered Storage](advanced/tiered-storage.md) - Tier scanner and archival planning command
- [Cross-Region Replication](advanced/cross-region-replication.md) - Async metadata replication and status reporting
- [Caching](advanced/caching.md) - LRU and Redis cache backends
- [Deduplication](advanced/deduplication.md) - Content-hash metadata and refcount management
- [Quota Management](advanced/quota-management.md) - Per-user/per-bucket quotas and S3 enforcement
- [Lifecycle Policies](advanced/lifecycle-policies.md) - S3 lifecycle config and processing command

### Interfaces
- [WebDAV](interfaces/webdav.md) - WebDAV server command and filesystem adapter
