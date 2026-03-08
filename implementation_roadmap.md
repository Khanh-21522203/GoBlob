# Implementation Roadmap

This roadmap provides a step-by-step guide to building GoBlob, a distributed blob storage system with S3 compatibility. The implementation is organized into logical phases that respect component dependencies and enable incremental system construction.

## Phase 0: Project Bootstrap

**Purpose**: Initialize the repository, module system, and tooling before any code is written.

### Task: Initialize Go Module and Repository

**Goal**
Create the Go module, establish the directory skeleton, configure code quality tooling, and set up a minimal CI pipeline so every subsequent task lands in a clean, lintable, testable repo.

**Implementation Steps**

1. Run `go mod init github.com/yourusername/goblob` to create `go.mod`
2. Create top-level directory skeleton: `goblob/`, `proto/`, `plans/`, `docs/`, `test/`, `benchmark/`, `k8s/`, `helm/`
3. Create `.gitignore` (Go binaries, `*.pb.go`, vendor/, IDE files)
4. Create `Makefile` with targets: `build`, `test`, `lint`, `proto`, `clean`
5. Add `.golangci.yml` with standard Go linters (`vet`, `staticcheck`, `errcheck`, `gocritic`)
6. Add `go.sum` by running `go mod tidy` after adding initial dependencies (`google.golang.org/grpc`, `google.golang.org/protobuf`, `github.com/spf13/viper`, `github.com/gorilla/mux`, `go.uber.org/zap`, `github.com/prometheus/client_golang`)
7. Create `.github/workflows/ci.yml` with `go build ./...`, `go test ./...`, and `golangci-lint` steps
8. Write a minimal `goblob/blob.go` placeholder (`package main; func main() {}`) to confirm `go build` works
9. Add `CONTRIBUTING.md` with branch naming and commit message conventions

**Dependencies**
None

**Related Plan**
N/A (repository infrastructure)

**Expected Output**
- `go.mod` and `go.sum`
- `Makefile`
- `.gitignore`
- `.golangci.yml`
- `.github/workflows/ci.yml`
- Top-level directory skeleton

### Phase 0 Checkpoint

Run `make build && make lint` and confirm zero errors before proceeding to Phase 1.

---

## Phase 1: Foundation Layer

**Purpose**: Establish core types, configuration infrastructure, and code generation tooling that all other components depend on.

### Task: Define Core Types Package

**Goal**  
Create the foundational type system used across all GoBlob components, including volume identifiers, needle IDs, replica placement descriptors, and TTL handling.

**Implementation Steps**

1. Create `goblob/types/` package directory
2. Define `VolumeId` type with string parsing and validation
3. Define `NeedleId` (uint64) with hex encoding/decoding
4. Define `Cookie` (uint32) for anti-brute-force protection
5. Implement `FileId` composite type combining VolumeId, NeedleId, and Cookie with string serialization
6. Define `ServerAddress` type with host:port parsing
7. Implement `ReplicaPlacement` type encoding replication policy (datacenter, rack, node copies)
8. Define `TTL` type with duration parsing and expiration calculation
9. Add unit tests for all type conversions and validations

**Dependencies**  
None (Go standard library only)

**Related Plan**  
`plans/plan-core-types.md`

**Expected Output**  
- `goblob/types/volume_id.go`
- `goblob/types/needle_id.go`
- `goblob/types/file_id.go`
- `goblob/types/server_address.go`
- `goblob/types/replica_placement.go`
- `goblob/types/ttl.go`
- Unit tests for all types

### Task: Setup Protobuf Code Generation

**Goal**  
Establish protobuf schema definitions and code generation pipeline for all gRPC services.

**Implementation Steps**

1. Create `goblob/pb/` directory for generated code
2. Create `proto/` directory for `.proto` schema files
3. Define `master.proto` with MasterService service (Assign, Lookup, Heartbeat, Statistics)
4. Define `volume_server.proto` with VolumeServer service (BatchDelete, VacuumVolumeCheck, VacuumVolumeCompact)
5. Define `filer.proto` with FilerService service (LookupDirectoryEntry, ListEntries, CreateEntry, UpdateEntry, DeleteEntry, SubscribeMetadata)
6. Define `iam.proto` with IAMService service (CreateIdentity, DeleteIdentity, ListIdentities)
7. Define common message types (VolumeInformationMessage, Location, FileChunk, Entry, FuseAttributes)
8. Create `Makefile` target for protoc code generation
9. Generate Go code with `protoc-gen-go` and `protoc-gen-go-grpc`
10. Add generated files to `.gitignore`

**Dependencies**  
Core Types (for embedding in protobuf messages)

**Related Plan**  
`plans/plan-protobuf.md`

**Expected Output**  
- `proto/master.proto`
- `proto/volume_server.proto`
- `proto/filer.proto`
- `proto/iam.proto`
- `goblob/pb/*.pb.go` (generated)
- `Makefile` with `protoc` target

### Task: Implement Configuration System

**Goal**  
Build unified configuration loading using TOML files and CLI flag overrides with Viper library.

**Implementation Steps**

1. Create `goblob/config/` package
2. Define configuration structs for each server role (MasterConfig, VolumeConfig, FilerConfig, S3Config)
3. Define common configuration (SecurityConfig, LogConfig, MetricsConfig)
4. Implement configuration file search paths (`/etc/goblob/`, `~/.goblob/`, `./`)
5. Implement Viper-based TOML file loading
6. Implement CLI flag binding with precedence over file values
7. Add configuration validation functions
8. Implement default value population
9. Add configuration dump/print functionality for debugging
10. Write unit tests for loading and validation

**Dependencies**  
Core Types

**Related Plan**  
`plans/plan-configuration.md`

**Expected Output**  
- `goblob/config/config.go`
- `goblob/config/master.go`
- `goblob/config/volume.go`
- `goblob/config/filer.go`
- `goblob/config/s3.go`
- `goblob/config/validation.go`
- Unit tests

### Task: Setup Observability Infrastructure

**Goal**  
Establish structured logging and Prometheus metrics collection across all components.

**Implementation Steps**

1. Create `goblob/observability/` package
2. Implement structured logger wrapper using `logrus` or `zap`
3. Define log levels and formatting (JSON for production, text for development)
4. Create metrics registry using Prometheus client library
5. Define standard metric types (counters, gauges, histograms) for common operations
6. Implement HTTP `/metrics` endpoint handler
7. Implement HTTP `/debug/pprof` endpoint handlers
8. Add context-aware logging helpers
9. Create metric helper functions for timing and counting operations
10. Create `goblob/stats/` package with pre-declared Prometheus metric variables for each component (e.g., `MasterAssignRequests`, `VolumeReadBytes`, `FilerMetadataOps`); other packages import `goblob/stats` to record metrics without importing the full observability package
11. Write examples and documentation

**Dependencies**  
Core Types

**Related Plan**  
`plans/plan-observability.md`

**Expected Output**
- `goblob/observability/logger.go`
- `goblob/observability/metrics.go`
- `goblob/observability/handlers.go`
- `goblob/stats/stats.go` (pre-declared metric variables, imported by all server packages)
- Example usage documentation

### Task: Implement Security Primitives

**Goal**  
Build JWT token generation/verification, TLS configuration, and IP whitelist guard middleware.

**Implementation Steps**

1. Create `goblob/security/` package
2. Implement JWT token generation with HS256 signing
3. Implement JWT token verification and claims extraction
4. Define `Guard` struct with IP whitelist and JWT verification
5. Implement HTTP middleware for Guard
6. Implement gRPC interceptor for Guard
7. Implement TLS configuration loading from certificate files
8. Add certificate hot-reload capability
9. Implement secret key loading from file or environment
10. Write unit tests for JWT and Guard logic

**Dependencies**  
Core Types, Configuration

**Related Plan**  
`plans/plan-security.md`

**Expected Output**  
- `goblob/security/jwt.go`
- `goblob/security/guard.go`
- `goblob/security/tls.go`
- `goblob/security/middleware.go`
- Unit tests


### Task: Implement Shared Utilities Package

**Goal**
Build the `goblob/util/` package containing data structures and helpers shared across topology, filer, storage, and server packages.

**Implementation Steps**

1. Create `goblob/util/` package directory
2. Implement `ConcurrentReadMap` — a thread-safe map using `sync.RWMutex` for concurrent reads
3. Implement `UnboundedQueue` — a goroutine-safe unbounded FIFO queue using a linked list; used by the Filer's background deletion queue
4. Implement `BytesReader` helper for reading fixed-size structs from byte slices
5. Implement `FullPath` string type for filer entry paths with helper methods (`Dir()`, `Name()`, `Child()`)
6. Add `MinUint64` / `MaxUint64` and other numeric helpers
7. Write unit tests for all data structures

**Dependencies**
Core Types

**Related Plan**
`plans/plan-filer-store.md`, `plans/plan-topology.md`

**Expected Output**
- `goblob/util/concurrent_read_map.go`
- `goblob/util/unbounded_queue.go`
- `goblob/util/bytes_reader.go`
- `goblob/util/full_path.go`
- Unit tests

### Task: Implement Graceful Shutdown Package

**Goal**
Build `goblob/grace/` providing a cross-platform signal handler that runs registered shutdown hooks in reverse registration order when SIGINT or SIGTERM is received.

**Implementation Steps**

1. Create `goblob/grace/` package
2. Implement `OnInterrupt(fn func())` to register a shutdown hook
3. Implement signal listener goroutine watching `os.Interrupt` and `syscall.SIGTERM`
4. On signal: call registered hooks in LIFO order, then `os.Exit(0)`
5. Implement `NotifyExit()` for programmatic shutdown (used in tests)
6. Write unit tests using `NotifyExit()`

**Dependencies**
None (Go standard library only)

**Related Plan**
`plans/plan-cmd.md`

**Expected Output**
- `goblob/grace/grace.go`
- Unit tests

### Phase 1 Checkpoint

Run `go test ./goblob/...` across all Phase 1 packages (types, config, observability, security, util, grace) and confirm all unit tests pass before proceeding to Phase 2.

---

## Phase 2: Storage Engine

**Purpose**: Build the core blob storage layer including needle format, volume management, and persistence mechanisms.

### Task: Implement Needle Binary Format

**Goal**  
Define the binary serialization format for storing blobs as needles with metadata, checksums, and compression support.

**Implementation Steps**

1. Create `goblob/storage/needle/` package
2. Define `Needle` struct with fields: Cookie, Id, Size, DataSize, Data, Flags, NameSize, Name, MimeSize, Mime, PairsSize, Pairs, LastModified, Ttl, Checksum
3. Implement needle header serialization (fixed 16-byte header)
4. Implement needle body serialization with variable-length fields
5. Implement CRC32 checksum calculation and verification
6. Add compression support (gzip flag in header)
7. Implement needle parsing from byte stream
8. Add needle size calculation helpers
9. Implement needle append-to-file operation
10. Write unit tests for serialization round-trips

**Dependencies**  
Core Types

**Related Plan**  
`plans/plan-needle-format.md`

**Expected Output**  
- `goblob/storage/needle/needle.go`
- `goblob/storage/needle/needle_read.go`
- `goblob/storage/needle/needle_write.go`
- Unit tests

### Task: Implement SuperBlock and Volume Metadata

**Goal**  
Create volume header format storing replication policy and version information.

**Implementation Steps**

1. Create `goblob/storage/super_block/` package
2. Define `SuperBlock` struct with Version and ReplicaPlacement fields
3. Implement 8-byte binary serialization format
4. Implement SuperBlock read from volume file
5. Implement SuperBlock write to volume file
6. Add version compatibility checking
7. Write unit tests

**Dependencies**  
Core Types

**Related Plan**  
`plans/plan-storage-engine.md`

**Expected Output**  
- `goblob/storage/super_block/super_block.go`
- Unit tests

### Task: Implement NeedleMap Interface and Implementations

**Goal**  
Build in-memory and LevelDB-backed needle index for fast needle lookup by ID.

**Implementation Steps**

1. Create `goblob/storage/needle_map/` package
2. Define `NeedleMapper` interface with Put, Get, Delete, Close methods
3. Define `NeedleValue` struct with Offset and Size
4. Implement `MemoryNeedleMap` using `map[NeedleId]NeedleValue`
5. Add RWMutex for concurrent access in memory implementation
6. Implement `LevelDbNeedleMap` using LevelDB for persistence
7. Implement index file loading on startup
8. Add index file persistence for memory map
9. Implement compact index format (.cpx) for space efficiency
10. Write unit tests and benchmarks

**Dependencies**  
Core Types, Needle Format

**Related Plan**  
`plans/plan-storage-engine.md`

**Expected Output**  
- `goblob/storage/needle_map/needle_map.go`
- `goblob/storage/needle_map/memory_map.go`
- `goblob/storage/needle_map/leveldb_map.go`
- Unit tests and benchmarks

### Task: Implement Volume Management

**Goal**  
Build Volume abstraction managing .dat data file and .idx index file pairs.

**Implementation Steps**

1. Create `goblob/storage/volume.go`
2. Define `Volume` struct with Id, DataFile, NeedleMap, SuperBlock, ReadOnly flag
3. Implement volume file creation with SuperBlock initialization
4. Implement volume loading from existing files
5. Implement needle write operation (append to .dat, update index)
6. Implement needle read operation (index lookup, file read at offset)
7. Implement needle delete operation (mark as deleted in index)
8. Add RWMutex for concurrent read/write access
9. Implement volume close and sync operations
10. Add volume statistics (file count, size, deleted count)
11. Write unit tests for volume lifecycle

**Dependencies**  
Core Types, Needle Format, SuperBlock, NeedleMap

**Related Plan**  
`plans/plan-storage-engine.md`

**Expected Output**  
- `goblob/storage/volume.go`
- `goblob/storage/volume_read.go`
- `goblob/storage/volume_write.go`
- Unit tests

### Task: Implement DiskLocation Management

**Goal**  
Build DiskLocation abstraction managing multiple volumes in a single directory.

**Implementation Steps**

1. Create `goblob/storage/disk_location.go`
2. Define `DiskLocation` struct with Directory path, MaxVolumeCount, volumes map
3. Implement volume discovery by scanning directory for .dat files
4. Implement volume loading on startup
5. Implement new volume creation
6. Add volume lookup by VolumeId
7. Implement volume deletion
8. Add disk space tracking and capacity management
9. Implement concurrent access control
10. Write unit tests

**Dependencies**  
Core Types, Volume

**Related Plan**  
`plans/plan-storage-engine.md`

**Expected Output**  
- `goblob/storage/disk_location.go`
- Unit tests

### Task: Implement Store (Top-Level Storage Manager)

**Goal**  
Build Store abstraction managing multiple DiskLocations across different directories.

**Implementation Steps**

1. Create `goblob/storage/store.go`
2. Define `Store` struct with Locations slice, IP, Port, PublicUrl
3. Implement store initialization with multiple directory paths
4. Implement volume lookup across all locations
5. Implement volume creation with location selection
6. Add volume statistics aggregation
7. Implement volume compaction coordination
8. Add concurrent operation limits using sync.Cond
9. Implement store shutdown and cleanup
10. Write unit tests

**Dependencies**  
Core Types, DiskLocation, Volume

**Related Plan**  
`plans/plan-storage-engine.md`

**Expected Output**  
- `goblob/storage/store.go`
- Unit tests

### Task: Implement Volume Compaction (Local)

**Goal**
Build the per-volume garbage collection mechanism that rewrites a volume file retaining only live needles, then atomically swaps the new file in.

**Implementation Steps**

1. Create `goblob/storage/compaction.go`
2. Implement garbage ratio calculation: `deletedBytes / totalBytes`; skip compaction if ratio < `garbageThreshold` (default 0.3)
3. Implement `Volume.Compact(bytesPerSecond int64)`: write live needles to `*.cpd` (data) and `*.cpx` (index) temp files
4. Implement needle copy loop: iterate `.idx`, skip tombstones (`Size == TombstoneFileSize`) and TTL-expired needles
5. Implement `Volume.CommitCompact()`: atomically rename `*.cpd` → `.dat` and `*.cpx` → `.idx`, reload index, increment `SuperBlock.CompactionRevision`
6. Implement `Volume.setVolumeNoWriteOrDeleteFlag(true/false)` to freeze writes during the commit swap
7. Add I/O throttling via token bucket or `time.Sleep` between needle copies when `bytesPerSecond > 0`
8. Implement compaction progress tracking (bytes copied, needles skipped)
9. Add compaction statistics and logging
10. Write unit tests covering: threshold check, needle filtering, atomic swap, TTL expiry filtering

**Dependencies**
Store, Volume, Observability

**Related Plan**
`plans/plan-garbage-collection.md`

**Expected Output**
- `goblob/storage/compaction.go`
- Unit tests

### Task: Implement Master-Coordinated Vacuum

**Goal**
Build the master-side orchestration that identifies volumes needing compaction, coordinates with volume servers to pause writes, triggers compaction, and commits the result — following the 3-step vacuum protocol from `plan-garbage-collection.md`.

**Implementation Steps**

1. Add HTTP handler `GET /vol/vacuum/check?garbageThreshold=X` on the volume server: scan all volumes, return list of VolumeIds whose garbage ratio exceeds threshold
2. Add HTTP handler `GET /vol/vacuum/needle?vid=N` on the volume server: set `noWriteOrDelete = true` on the volume, then run `Volume.Compact()` in background; return 200 when compaction file is ready
3. Add HTTP handler `GET /vol/vacuum/commit?vid=N` on the volume server: call `Volume.CommitCompact()`, then clear `noWriteOrDelete`
4. Implement `topology/topology_vacuum.go` on the master: `Vacuum(garbageThreshold float64)` iterates all DataNodes, calls `/vol/vacuum/check` per node, collects candidate volumes
5. For each candidate volume, call `/vol/vacuum/needle` (compact) then `/vol/vacuum/commit` on each replica location
6. Add `POST /vol/vacuum` HTTP handler on the master to trigger vacuum manually (called by admin shell `volume.vacuum`)
7. Add `startAdminScripts()` background goroutine on master that periodically runs vacuum per `maintenance.sleep_minutes` config
8. Write integration tests for the full 3-step vacuum flow

**Dependencies**
Volume Server, Master Server Core, gRPC Transport

**Related Plan**
`plans/plan-garbage-collection.md`, `plans/plan-master-server.md`

**Expected Output**
- `goblob/topology/topology_vacuum.go`
- Vacuum HTTP handlers on volume server
- `POST /vol/vacuum` handler on master server
- Integration tests

### Phase 2 Checkpoint

Bring up a single volume server and run a manual vacuum cycle end-to-end: write 100 needles, delete 40, confirm garbage ratio > 0.3, trigger `/vol/vacuum/check` → `/vol/vacuum/needle` → `/vol/vacuum/commit`, confirm the volume file shrinks and the deleted needles are gone. Run `go test ./goblob/storage/...` before proceeding to Phase 3.

---

## Phase 3: Cluster Coordination

**Purpose**: Implement consensus, topology management, and distributed coordination primitives for cluster orchestration.

### Task: Implement Raft Consensus Layer

**Goal**  
Integrate Raft consensus for master server high availability and leader election.

**Implementation Steps**

1. Create `goblob/raft/` package
2. Use `goblob/raft` as the default Raft implementation; add `hashicorp/raft` as an opt-in alternative selectable via `-raftHashicorp` CLI flag (both must implement the same `raft.Server` interface)
3. Define `RaftServer` struct wrapping chosen library
4. Implement Raft FSM (Finite State Machine) for topology state
5. Implement log store using BoltDB or LevelDB
6. Implement snapshot store for state persistence
7. Implement peer discovery and cluster join logic
8. Add leader election callbacks
9. Implement state replication verification
10. Add Raft statistics and health endpoints
11. Write integration tests for leader election and failover

**Dependencies**  
Core Types, Configuration, Observability

**Related Plan**  
`plans/plan-raft-consensus.md`

**Expected Output**  
- `goblob/raft/raft_server.go`
- `goblob/raft/fsm.go`
- `goblob/raft/store.go`
- Integration tests

### Task: Implement File ID Sequencer

**Goal**  
Build globally unique, monotonic NeedleId generation with multiple backend strategies.

**Implementation Steps**

1. Create `goblob/sequence/` package
2. Define `Sequencer` interface with NextFileId and SetMax methods
3. Implement `FileSequencer` using file-based atomic counter
4. Implement `RaftSequencer` using Raft log for distributed generation
5. Implement `SnowflakeSequencer` using timestamp + machine ID + sequence
6. Add sequence persistence and recovery
7. Implement sequence reservation for batch allocation
8. Add sequence monitoring and gap detection
9. Write unit tests for each implementation
10. Write benchmarks for throughput testing

**Dependencies**  
Core Types, Raft (for RaftSequencer)

**Related Plan**  
`plans/plan-sequencer.md`

**Expected Output**  
- `goblob/sequence/sequencer.go`
- `goblob/sequence/file_sequencer.go`
- `goblob/sequence/raft_sequencer.go`
- `goblob/sequence/snowflake_sequencer.go`
- Unit tests and benchmarks

### Task: Implement Topology Manager

**Goal**  
Build hierarchical cluster model (DataCenter → Rack → DataNode → Disk) with volume placement logic.

**Implementation Steps**

1. Create `goblob/topology/` package
2. Define `Node` interface with common node operations
3. Implement `Topology` root node with collection map
4. Implement `DataCenter` node type
5. Implement `Rack` node type
6. Implement `DataNode` node type representing volume servers
7. Implement volume location tracking per node
8. Implement `VolumeLayout` for managing volumes in a collection
9. Add writable volume tracking and selection
10. Implement replica placement algorithm respecting ReplicaPlacement policy
11. Implement volume location lookup by VolumeId
12. Add topology statistics and capacity reporting
13. Write unit tests for placement logic

**Dependencies**  
Core Types, Sequencer

**Related Plan**  
`plans/plan-topology.md`

**Expected Output**  
- `goblob/topology/topology.go`
- `goblob/topology/node.go`
- `goblob/topology/data_center.go`
- `goblob/topology/rack.go`
- `goblob/topology/data_node.go`
- `goblob/topology/volume_layout.go`
- Unit tests

### Task: Implement Volume Growth Strategy

**Goal**  
Build automated volume creation when cluster capacity runs low.

**Implementation Steps**

1. Create `goblob/topology/volume_growth.go`
2. Define `VolumeGrowOption` and `VolumeGrowRequest` structs (collection, replication, TTL, diskType, count)
3. Implement `VolumeGrowth.AutomaticGrowByType(option, grpcDialOption, topo)`:
   - Determine volume count from `VolumeGrowthConfig` (copy_1=7, copy_2=6, copy_3=3, copy_other=1)
   - Call `findEmptySlots()` to identify DataNodes with available capacity satisfying the ReplicaPlacement
   - For each selected node, call `ReserveOneVolume(option)` to lock capacity before issuing the gRPC call (prevents double-allocation under concurrent growth requests)
   - Call `AllocateVolume` gRPC to the volume server for each reserved node
   - Release `CapacityReservation` on both success and failure
4. Implement `CapacityReservations` on each `NodeImpl`: `addReservation(diskType, count) reservationId` and `removeReservation(reservationId)`; `FreeSpace()` must subtract reserved counts
5. Implement `Node.ReserveOneVolume(option)` that walks the topology tree filtering by DataCenter/Rack/DiskType, calls `CapacityReservations.addReservation`, checks `FreeSpace() > reservedCount`, and rolls back if insufficient
6. Implement capacity threshold monitoring: send `VolumeGrowRequest` to `volumeGrowthRequestChan` when writable volume count drops below threshold (default 0.9 × total writable capacity)
7. Implement `ProcessGrowRequest()` goroutine on master: drain `volumeGrowthRequestChan`, call `AutomaticGrowByType`, deduplicate in-flight requests
8. Add growth statistics and logging
9. Write unit tests for: growth trigger threshold, capacity reservation race prevention, growth with insufficient nodes (error), and reservation rollback on AllocateVolume failure

**Dependencies**
Topology Manager, gRPC Transport (for AllocateVolume RPC)

**Related Plan**
`plans/plan-volume-growth.md`

**Expected Output**  
- `goblob/topology/volume_growth.go`
- Unit tests

### Task: Implement Cluster Registry

**Goal**  
Build registry tracking non-volume nodes (filers, S3 gateways) with heartbeat streams.

**Implementation Steps**

1. Create `goblob/cluster/` package
2. Define `ClusterRegistry` struct with node maps
3. Implement node registration with metadata
4. Implement `KeepConnected` gRPC streaming for heartbeats
5. Add node expiration based on heartbeat timeout
6. Implement node type filtering (filer, s3, etc.)
7. Add cluster statistics and node listing
8. Implement node discovery for clients
9. Write unit tests

**Dependencies**  
Core Types, Protobuf, gRPC Transport

**Related Plan**  
`plans/plan-cluster-registry.md`

**Expected Output**  
- `goblob/cluster/registry.go`
- Unit tests


### Phase 3 Checkpoint

Start a single master node in single-master mode (`-peers=none`). Confirm: (1) Raft bootstraps immediately, (2) sequencer generates monotonically increasing IDs under concurrent load (`go test -race ./goblob/sequence/...`), (3) topology correctly registers a mock DataNode heartbeat, (4) volume growth fires when writable count < threshold. Run `go test ./goblob/topology/... ./goblob/sequence/... ./goblob/raft/...` before proceeding to Phase 4.

---

## Phase 4: Core Servers

**Purpose**: Implement the three primary server roles (Master, Volume, Filer) that form the distributed storage system.

### Task: Implement gRPC Transport Layer

**Goal**  
Build gRPC client/server helpers with connection pooling, TLS support, and service discovery.

**Implementation Steps**

1. Create `goblob/wdclient/` package for master discovery
2. Implement `MasterClient` with leader discovery and failover
3. Add connection pooling for gRPC clients
4. Implement TLS credential loading for secure connections
5. Create helper functions for common RPC patterns
6. Implement retry logic with exponential backoff
7. Add gRPC interceptors for logging and metrics
8. Implement streaming RPC helpers
9. Write unit tests for connection management

**Dependencies**  
Protobuf, Security, Configuration

**Related Plan**  
`plans/plan-grpc-transport.md`

**Expected Output**  
- `goblob/wdclient/master_client.go`
- `goblob/wdclient/vidmap.go`
- `goblob/operation/grpc_client.go`
- Unit tests

### Task: Implement Master Server Core

**Goal**  
Build central coordinator handling file ID assignment, volume lookup, and topology management.

**Implementation Steps**

1. Create `goblob/server/master_server.go`
2. Define `MasterServer` struct with: `Topo *topology.Topology`, `vg *topology.VolumeGrowth`, `Sequencer sequence.Sequencer`, `RaftServer`, `Cluster *cluster.Cluster`, `MasterClient *wdclient.MasterClient`, `guard *security.Guard`, `volumeGrowthRequestChan chan *VolumeGrowRequest`
3. Implement HTTP handler `POST /dir/assign`: pick writable volume from topology, call sequencer for NeedleId, return `{fid, url, count}`; proxy to Raft leader if this node is a follower
4. Implement HTTP handler `GET /dir/lookup`: return volume server URL list for a given VolumeId; proxy to leader if follower
5. Implement HTTP handler `GET /vol/grow`: manually trigger volume growth for a given collection/replication
6. Implement HTTP handler `GET /cluster/status`: return topology tree summary and connected node counts
7. Implement HTTP handler `POST /vol/vacuum`: trigger master-coordinated vacuum (calls `topology_vacuum.go`)
8. Implement follower reverse-proxy: non-leader masters forward write requests (`/dir/assign`, `/dir/lookup`) to the current Raft leader via HTTP reverse proxy
9. Implement gRPC `KeepConnected` streaming handler: receive heartbeats from volume servers, call `Topology.ProcessJoinMessage()` to register DataNode/update volume locations
10. Implement `startAdminScripts()` background goroutine: run configured maintenance shell commands periodically (`maintenance.scripts`, `maintenance.sleep_minutes`)
11. Add `ProcessGrowRequest()` background goroutine draining `volumeGrowthRequestChan`
12. Add `StartRefreshWritableVolumes()` background goroutine to periodically re-evaluate writable volume health
13. Add master server initialization and graceful shutdown via `grace.OnInterrupt`
14. Write integration tests for assign, lookup, heartbeat processing, and follower proxy

**Dependencies**
Raft, Topology, Sequencer, Volume Growth, Cluster Registry, gRPC Transport, Grace

**Related Plan**
`plans/plan-master-server.md`

**Expected Output**  
- `goblob/server/master_server.go`
- `goblob/server/master_grpc_server.go`
- `goblob/server/master_server_handlers.go`
- Integration tests

### Task: Implement Operation Primitives (Assign + Upload)

**Goal**
Build the minimal client-side `goblob/operation/` helpers that the Replication Engine and Volume Server need to forward writes to replica nodes. The full Client SDK (with caching, retries, download) is completed in Phase 5; this task only covers what is strictly needed for replication.

**Implementation Steps**

1. Create `goblob/operation/` package
2. Implement `Assign(masterAddr, count, collection, replication, ttl string) (*AssignResult, error)` — HTTP POST to master `/dir/assign`
3. Implement `Upload(uploadUrl string, filename string, mimeType string, data io.Reader) (*UploadResult, error)` — HTTP PUT to volume server
4. Implement `LookupVolumeId(masterAddr string, vid types.VolumeId) ([]string, error)` — HTTP GET to master `/dir/lookup`
5. Add basic error handling and HTTP timeout (no retry logic yet — that comes in Phase 5)
6. Write unit tests using `httptest.Server` mocks

**Dependencies**
Core Types, Protobuf

**Related Plan**
`plans/plan-client-sdk.md`

**Expected Output**
- `goblob/operation/assign.go`
- `goblob/operation/upload.go`
- `goblob/operation/lookup.go`
- Unit tests

### Task: Implement Replication Engine

**Goal**
Build synchronous replication ensuring strong consistency across replica volume servers.

**Implementation Steps**

1. Create `goblob/replication/` package
2. Implement replica location discovery from master
3. Implement parallel write to all replicas
4. Add replication loop prevention (check request origin)
5. Implement replica write verification
6. Add replica failure handling and retry
7. Implement replication statistics and monitoring
8. Write unit tests for replication logic

**Dependencies**  
Core Types, gRPC Transport

**Related Plan**  
`plans/plan-replication-engine.md`

**Expected Output**  
- `goblob/replication/replicator.go`
- Unit tests

### Task: Implement Volume Server Core

**Goal**  
Build data plane server storing blobs and serving read/write requests with replication.

**Implementation Steps**

1. Create `goblob/server/volume_server.go`
2. Define `VolumeServer` struct with Store, MasterClient, Guard
3. Implement HTTP handler for `PUT /{fid}` (upload): parse FileId from path, write needle to volume, trigger replication to replicas
4. Implement HTTP handler for `GET /{fid}` (download): parse FileId, look up needle offset in index, read from `.dat` file, serve with ETag and Cache-Control headers
5. Implement HTTP handler for `DELETE /{fid}` (delete): write tombstone entry to index; actual space reclaimed during vacuum
6. Implement gRPC handlers for batch operations
7. Add replication integration for write operations
8. Implement heartbeat streaming to master
9. Add concurrent upload/download limiting using sync.Cond
10. Implement volume server initialization and shutdown
11. Write integration tests

**Dependencies**  
Storage Engine, Replication Engine, Security, gRPC Transport

**Related Plan**  
`plans/plan-volume-server.md`

**Expected Output**  
- `goblob/server/volume_server.go`
- `goblob/server/volume_grpc_server.go`
- `goblob/server/volume_server_handlers.go`
- Integration tests

### Task: Implement Filer Store Interface and LevelDB Backend

**Goal**  
Build pluggable metadata persistence layer with default LevelDB implementation.

**Implementation Steps**

1. Create `goblob/filer/` package
2. Define `FilerStore` interface with InsertEntry, UpdateEntry, FindEntry, DeleteEntry, ListDirectoryEntries methods
3. Define `Entry` struct with FullPath, Attr (FuseAttributes), Chunks, Extended metadata
4. Define `FileChunk` struct with FileId, Offset, Size, ModifiedTsNs, ETag
5. Implement `LevelDB2Store` using LevelDB for persistence
6. Implement directory entry encoding/decoding with protobuf
7. Add transaction support for atomic operations
8. Implement directory listing with pagination
9. Add store initialization and shutdown
10. Write unit tests for CRUD operations

**Dependencies**  
Core Types, Protobuf

**Related Plan**  
`plans/plan-filer-store.md`

**Expected Output**  
- `goblob/filer/filer_store.go`
- `goblob/filer/entry.go`
- `goblob/filer/leveldb2/leveldb2_store.go`
- Unit tests

### Task: Implement Distributed Lock Manager

**Goal**  
Build named distributed locks for cross-process coordination using filer store.

**Implementation Steps**

1. Create `goblob/filer/lock/` package
2. Define lock entry format in filer store
3. Implement `AcquireLock` with expiry and renewal token
4. Implement `ReleaseLock` with token verification
5. Implement `RenewLock` for extending lock duration
6. Add lock expiration cleanup
7. Implement gRPC service for lock operations
8. Write unit tests for lock contention

**Dependencies**  
Filer Store

**Related Plan**  
`plans/plan-distributed-lock.md`

**Expected Output**  
- `goblob/filer/lock/lock_manager.go`
- Unit tests

### Task: Implement Log Buffer for Metadata Events

**Goal**  
Build in-memory event bus for filer metadata change subscriptions.

**Implementation Steps**

1. Create `goblob/filer/log_buffer/` package
2. Implement ring buffer for event storage
3. Define event types (Create, Update, Delete, Rename)
4. Implement event publishing on metadata changes
5. Implement subscriber registration and notification
6. Add volume-based persistence for event replay
7. Implement subscriber catch-up from persisted events
8. Add buffer size management and overflow handling
9. Write unit tests

**Dependencies**  
Filer Store, Observability

**Related Plan**  
`plans/plan-log-buffer.md`

**Expected Output**  
- `goblob/filer/log_buffer/log_buffer.go`
- Unit tests

### Task: Implement Filer Server Core

**Goal**  
Build filesystem metadata layer mapping paths to chunks with cross-filer synchronization.

**Implementation Steps**

1. Create `goblob/server/filer_server.go`
2. Define `FilerServer` struct with FilerStore, MasterClient, LogBuffer, LockManager
3. Implement HTTP handler for `POST /path` (create file/directory)
4. Implement HTTP handler for `GET /path` (read metadata)
5. Implement HTTP handler for `DELETE /path` (delete file/directory)
6. Implement HTTP handler for `GET /path/?list` (list directory)
7. Implement gRPC handlers for metadata operations
8. Implement chunk upload coordination with master assignment
9. Implement chunk deletion with background retry queue
10. Add metadata change notification via LogBuffer
11. Implement `MetaAggregator` for cross-filer synchronization
12. Add filer server initialization and shutdown
13. Write integration tests

**Dependencies**  
Filer Store, Log Buffer, Distributed Lock, gRPC Transport

**Related Plan**  
`plans/plan-filer-server.md`

**Expected Output**  
- `goblob/server/filer_server.go`
- `goblob/server/filer_grpc_server.go`
- `goblob/server/filer_server_handlers.go`
- `goblob/filer/meta_aggregator.go`
- Integration tests


### Phase 4 Checkpoint

Bring up a 1-master + 2-volume-server cluster. Run an end-to-end write: call `/dir/assign` on master → `PUT /{fid}` on volume server → confirm replication on the second volume server. Run a read: `/dir/lookup` → `GET /{fid}`. Run a filer mkdir + file create + list. Confirm all three servers start, heartbeat successfully, and shut down cleanly via Ctrl-C. Run `go test -race ./goblob/server/...` before proceeding to Phase 5.

---

## Phase 5: Client Interfaces

**Purpose**: Build client libraries and gateway services for accessing the storage system.

### Task: Implement Client SDK

**Goal**  
Build Go client library for direct blob access with retry logic and location caching.

**Implementation Steps**

1. Create `goblob/operation/` package
2. Implement `Assign` operation for file ID allocation from master
3. Implement `Upload` operation for blob upload to volume server
4. Implement `LookupVolumeId` for volume location discovery
5. Implement `Download` operation for blob retrieval
6. Add location cache with TTL for volume lookups
7. Implement retry logic with exponential backoff
8. Add chunk upload helpers for large files
9. Implement concurrent upload/download with worker pools
10. Write unit tests and examples

**Dependencies**  
Core Types, gRPC Transport

**Related Plan**  
`plans/plan-client-sdk.md`

**Expected Output**  
- `goblob/operation/assign.go`
- `goblob/operation/upload.go`
- `goblob/operation/lookup.go`
- `goblob/operation/download.go`
- Unit tests and examples

### Task: Implement IAM System

**Goal**  
Build identity and access management for S3 authentication and authorization.

**Implementation Steps**

1. Create `goblob/s3api/iam/` package
2. Define `Identity` struct with Name, Credentials (access key, secret key), Actions
3. Implement identity storage using filer store
4. Implement `CreateIdentity` with key generation
5. Implement `DeleteIdentity` and `ListIdentities`
6. Implement AWS Signature V4 verification
7. Implement AWS Signature V2 verification (legacy)
8. Implement action authorization against identity permissions
9. Add gRPC service for IAM operations
10. Write unit tests for signature verification

**Dependencies**  
Filer Store, Security

**Related Plan**  
`plans/plan-iam.md`

**Expected Output**  
- `goblob/s3api/iam/identity.go`
- `goblob/s3api/iam/signature_v4.go`
- `goblob/s3api/iam/signature_v2.go`
- `goblob/s3api/iam/authorization.go`
- Unit tests

### Task: Implement S3 API Gateway Core

**Goal**  
Build S3-compatible REST API translating requests to filer operations.

**Implementation Steps**

1. Create `goblob/s3api/` package
2. Define `S3ApiServer` struct with FilerClient, IAM, CircuitBreaker
3. Implement S3 request routing and path parsing
4. Implement bucket operations (CreateBucket, DeleteBucket, ListBuckets, HeadBucket)
5. Implement object operations (PutObject, GetObject, DeleteObject, HeadObject, ListObjects)
6. Add S3 error response formatting (XML)
7. Implement authentication middleware using IAM
8. Add circuit breaker for rate limiting
9. Implement S3 server initialization and shutdown
10. Write integration tests

**Dependencies**  
Filer Server, IAM, Security, Client SDK

**Related Plan**  
`plans/plan-s3-gateway.md`

**Expected Output**  
- `goblob/s3api/s3api_server.go`
- `goblob/s3api/s3api_bucket_handlers.go`
- `goblob/s3api/s3api_object_handlers.go`
- `goblob/s3api/auth_signature.go`
- Integration tests

### Task: Implement S3 Multipart Upload

**Goal**  
Add support for S3 multipart upload protocol for large objects.

**Implementation Steps**

1. Create `goblob/s3api/s3api_multipart.go`
2. Implement `InitiateMultipartUpload` handler
3. Implement `UploadPart` handler with part storage
4. Implement `CompleteMultipartUpload` handler with part assembly
5. Implement `AbortMultipartUpload` handler with cleanup
6. Implement `ListParts` handler
7. Add multipart upload state tracking in filer
8. Implement part validation and ETag generation
9. Write integration tests

**Dependencies**  
S3 API Gateway Core

**Related Plan**  
`plans/plan-s3-gateway.md`

**Expected Output**  
- `goblob/s3api/s3api_multipart.go`
- Integration tests

### Task: Implement S3 Advanced Features

**Goal**  
Add server-side encryption, object versioning, and bucket policies.

**Implementation Steps**

1. Implement SSE-S3 (server-managed encryption)
2. Implement SSE-C (customer-provided keys)
3. Implement SSE-KMS (key management service integration)
4. Add encryption metadata to file chunks
5. Implement object versioning with version ID generation
6. Implement bucket policy parsing and evaluation
7. Add CORS configuration support
8. Implement object tagging
9. Write integration tests

**Dependencies**  
S3 API Gateway Core

**Related Plan**  
`plans/plan-s3-gateway.md`

**Expected Output**  
- `goblob/s3api/s3api_encryption.go`
- `goblob/s3api/s3api_versioning.go`
- `goblob/s3api/s3api_policy.go`
- Integration tests


### Phase 5 Checkpoint

Run `aws s3 mb s3://test-bucket` and `aws s3 cp testfile.txt s3://test-bucket/` against the running S3 gateway. Confirm the object is retrievable with `aws s3 cp s3://test-bucket/testfile.txt -`. Run `go test ./goblob/s3api/...` and `go test ./goblob/operation/...` before proceeding to Phase 6.

---

## Phase 6: Command Line Interface

**Purpose**: Build CLI binary with subcommands for launching servers and administrative operations.

### Task: Implement Command Framework

**Goal**  
Create CLI framework with subcommand dispatch and flag parsing.

**Implementation Steps**

1. Create `goblob/command/` package
2. Define `Command` interface with Run method
3. Implement command registry and dispatcher
4. Add global flags (config file, log level, etc.)
5. Implement help text generation
6. Add version command
7. Write unit tests

**Dependencies**  
Configuration

**Related Plan**  
`plans/plan-cmd.md`

**Expected Output**  
- `goblob/command/command.go`
- `goblob/command/version.go`
- Unit tests

### Task: Implement Server Subcommands

**Goal**  
Create subcommands for launching each server role.

**Implementation Steps**

1. Create `goblob/command/master.go` for `blob master` subcommand
2. Create `goblob/command/volume.go` for `blob volume` subcommand
3. Create `goblob/command/filer.go` for `blob filer` subcommand
4. Create `goblob/command/s3.go` for `blob s3` subcommand
5. Create `goblob/command/server.go` for `blob server` (all-in-one) subcommand
6. Implement configuration loading for each role
7. Implement server initialization and startup
8. Add graceful shutdown handling with signal catching
9. Implement startup delay coordination for all-in-one mode
10. Write integration tests

**Dependencies**  
Master Server, Volume Server, Filer Server, S3 Gateway, Configuration

**Related Plan**  
`plans/plan-cmd.md`

**Expected Output**  
- `goblob/command/master.go`
- `goblob/command/volume.go`
- `goblob/command/filer.go`
- `goblob/command/s3.go`
- `goblob/command/server.go`
- Integration tests

### Task: Implement Admin Shell

**Goal**  
Build interactive REPL and batch script executor for cluster operations.

**Implementation Steps**

1. Create `goblob/shell/` package
2. Implement command parser and executor
3. Implement volume commands (volume.list, volume.delete, volume.mount, volume.unmount)
4. Implement cluster commands (cluster.status, cluster.check)
5. Implement lock commands (lock.acquire, lock.release)
6. Implement fs commands (fs.ls, fs.cat, fs.rm, fs.mkdir)
7. Add interactive mode with readline support
8. Add batch mode for script execution
9. Implement command help and autocomplete
10. Create `goblob/command/shell.go` for `blob shell` subcommand
11. Write integration tests

**Dependencies**  
Distributed Lock, gRPC Transport, Client SDK

**Related Plan**  
`plans/plan-admin-shell.md`

**Expected Output**  
- `goblob/shell/shell.go`
- `goblob/shell/command_volume.go`
- `goblob/shell/command_cluster.go`
- `goblob/shell/command_lock.go`
- `goblob/shell/command_fs.go`
- `goblob/command/shell.go`
- Integration tests

### Task: Implement Utility Subcommands

**Goal**  
Add utility commands for backup, export, benchmark, and maintenance.

**Implementation Steps**

1. Create `goblob/command/backup.go` for volume backup
2. Create `goblob/command/export.go` for data export
3. Create `goblob/command/compact.go` for manual compaction trigger
4. Create `goblob/command/fix.go` for volume repair
5. Create `goblob/command/benchmark.go` for performance testing
6. Implement each command with appropriate flags
7. Write integration tests

**Dependencies**  
Storage Engine, Client SDK

**Related Plan**  
`plans/plan-cmd.md`

**Expected Output**  
- `goblob/command/backup.go`
- `goblob/command/export.go`
- `goblob/command/compact.go`
- `goblob/command/fix.go`
- `goblob/command/benchmark.go`
- Integration tests

### Task: Create Main Binary Entry Point

**Goal**  
Build main.go that ties all commands together into single binary.

**Implementation Steps**

1. Create `goblob/blob.go` (main package)
2. Implement command registration
3. Implement command dispatch based on CLI args
4. Add panic recovery and error handling
5. Add build version injection
6. Create Makefile for building binary
7. Write end-to-end tests

**Dependencies**  
All Command implementations

**Related Plan**  
`plans/plan-cmd.md`

**Expected Output**  
- `goblob/blob.go`
- `Makefile`
- End-to-end tests


### Phase 6 Checkpoint

Run `blob server` (all-in-one mode) and confirm all subcommands start without errors. Run `blob shell` and execute `cluster.status`, `volume.list`, `fs.ls /`. Run `go test ./goblob/command/... ./goblob/shell/...` before proceeding to Phase 7.

---

## Phase 7: Production Readiness

**Purpose**: Add monitoring, testing, documentation, and deployment tooling for production use.

### Task: Enhance Observability

**Goal**  
Add comprehensive metrics, tracing, and health checks across all components.

**Implementation Steps**

1. Add Prometheus metrics for all server operations (request count, latency, errors)
2. Implement volume server metrics (disk usage, needle count, compaction stats)
3. Implement master server metrics (topology size, assignment rate, heartbeat lag)
4. Implement filer server metrics (metadata operations, chunk count)
5. Add health check endpoints for all servers
6. Implement readiness probes for Kubernetes
7. Add distributed tracing integration (OpenTelemetry)
8. Create Grafana dashboard templates
9. Write observability documentation

**Dependencies**  
All servers, Observability infrastructure

**Related Plan**  
`plans/plan-observability.md`

**Expected Output**  
- Enhanced metrics in all server components
- `docs/observability/metrics.md`
- `docs/observability/dashboards/`
- Grafana dashboard JSON files

### Task: Implement Additional Filer Store Backends

**Goal**  
Add support for production-grade metadata stores beyond LevelDB.

**Implementation Steps**

1. Implement `RedisStore` using Redis for metadata
2. Implement `PostgreSQLStore` using PostgreSQL
3. Implement `MySQLStore` using MySQL
4. Implement `CassandraStore` using Cassandra
5. Implement `MongoDBStore` using MongoDB
6. Add store-specific configuration options
7. Implement connection pooling and retry logic
8. Add store selection via configuration
9. Write integration tests for each store
10. Document store selection guidelines

**Dependencies**  
Filer Store interface

**Related Plan**  
`plans/plan-filer-store.md`

**Expected Output**  
- `goblob/filer/redis3/redis_store.go`
- `goblob/filer/postgres/postgres_store.go`
- `goblob/filer/mysql/mysql_store.go`
- `goblob/filer/cassandra/cassandra_store.go`
- `goblob/filer/mongodb/mongodb_store.go`
- Integration tests
- `docs/filer/store-backends.md`

### Task: Create Integration Test Suite

**Goal**  
Build comprehensive integration tests covering multi-server scenarios.

**Implementation Steps**

1. Create `test/integration/` directory
2. Implement test cluster launcher (master + volumes + filer)
3. Write tests for basic blob upload/download flow
4. Write tests for replication verification
5. Write tests for master failover (Raft)
6. Write tests for volume compaction
7. Write tests for filer metadata operations
8. Write tests for S3 API compatibility
9. Add test data generators
10. Create CI/CD pipeline configuration

**Dependencies**  
All servers

**Related Plan**  
N/A (testing infrastructure)

**Expected Output**  
- `test/integration/cluster_test.go`
- `test/integration/replication_test.go`
- `test/integration/failover_test.go`
- `test/integration/s3_test.go`
- `.github/workflows/ci.yml` or equivalent

### Task: Create Deployment Configurations

**Goal**  
Provide deployment templates for various platforms.

**Implementation Steps**

1. Create Docker images for each server role
2. Create `Dockerfile` with multi-stage build
3. Create Docker Compose configuration for local development
4. Create Kubernetes manifests (StatefulSet for master/volume, Deployment for filer/S3)
5. Create Helm chart for Kubernetes deployment
6. Add configuration examples for different topologies
7. Create systemd service files for bare-metal deployment
8. Write deployment documentation

**Dependencies**  
All servers, Configuration

**Related Plan**  
N/A (deployment infrastructure)

**Expected Output**  
- `Dockerfile`
- `docker-compose.yml`
- `k8s/master-statefulset.yaml`
- `k8s/volume-statefulset.yaml`
- `k8s/filer-deployment.yaml`
- `k8s/s3-deployment.yaml`
- `helm/goblob/`
- `systemd/goblob-master.service`
- `docs/deployment/`

### Task: Write User Documentation

**Goal**  
Create comprehensive documentation for users and operators.

**Implementation Steps**

1. Write getting started guide
2. Write configuration reference
3. Write API documentation (HTTP and gRPC)
4. Write S3 compatibility matrix
5. Write operational runbook (backup, restore, scaling)
6. Write troubleshooting guide
7. Write performance tuning guide
8. Create architecture diagrams
9. Write contribution guidelines
10. Set up documentation website (e.g., using MkDocs)

**Dependencies**  
All components

**Related Plan**  
N/A (documentation)

**Expected Output**  
- `docs/getting-started.md`
- `docs/configuration.md`
- `docs/api-reference.md`
- `docs/s3-compatibility.md`
- `docs/operations/`
- `docs/troubleshooting.md`
- `docs/performance.md`
- `docs/contributing.md`
- `mkdocs.yml`

### Task: Implement Performance Benchmarks

**Goal**  
Create benchmark suite for measuring system performance.

**Implementation Steps**

1. Create `benchmark/` directory
2. Implement blob upload benchmark
3. Implement blob download benchmark
4. Implement concurrent operation benchmark
5. Implement large file (multi-chunk) benchmark
6. Implement metadata operation benchmark
7. Add benchmark result reporting
8. Create performance regression tests
9. Document benchmark methodology

**Dependencies**  
All servers, Client SDK

**Related Plan**  
N/A (benchmarking infrastructure)

**Expected Output**  
- `benchmark/upload_bench.go`
- `benchmark/download_bench.go`
- `benchmark/concurrent_bench.go`
- `benchmark/largefile_bench.go`
- `benchmark/metadata_bench.go`
- `docs/benchmarks.md`

### Task: Security Hardening

**Goal**  
Implement additional security features and conduct security review.

**Implementation Steps**

1. Add rate limiting per client IP
2. Implement request size limits
3. Add audit logging for sensitive operations
4. Implement secret rotation mechanism
5. Add security headers to HTTP responses
6. Implement CORS policy enforcement
7. Add input validation and sanitization
8. Conduct security audit of authentication flows
9. Write security best practices documentation
10. Add security scanning to CI/CD pipeline

**Dependencies**  
Security infrastructure, All servers

**Related Plan**  
`plans/plan-security.md`

**Expected Output**  
- Enhanced security middleware
- `docs/security/best-practices.md`
- `docs/security/audit-logging.md`
- Security scanning configuration


## Phase 8: Advanced Features

**Purpose**: Implement optional advanced features for enhanced functionality and performance.

### Task: Implement Erasure Coding

**Goal**  
Add erasure coding as an alternative to replication for space efficiency.

**Implementation Steps**

1. Create `goblob/storage/erasure_coding/` package
2. Integrate Reed-Solomon erasure coding library
3. Implement data shard and parity shard generation
4. Implement shard distribution across volume servers
5. Implement shard reconstruction on read
6. Add erasure coding configuration (data shards, parity shards)
7. Implement volume layout for erasure-coded volumes
8. Add erasure coding support to volume server
9. Write unit tests and benchmarks
10. Document erasure coding configuration

**Dependencies**  
Storage Engine, Volume Server, Topology

**Related Plan**  
N/A (advanced feature)

**Expected Output**  
- `goblob/storage/erasure_coding/ec.go`
- Configuration updates
- `docs/advanced/erasure-coding.md`

### Task: Implement Tiered Storage

**Goal**  
Add support for hot/warm/cold storage tiers with automatic data migration.

**Implementation Steps**

1. Create `goblob/storage/tiering/` package
2. Define storage tier types (SSD, HDD, S3, Glacier)
3. Implement tier assignment based on access patterns
4. Implement automatic data migration between tiers
5. Add tier-aware volume allocation
6. Implement tier statistics and monitoring
7. Add tier configuration per collection
8. Write integration tests
9. Document tiering strategy

**Dependencies**  
Storage Engine, Volume Server, Topology

**Related Plan**  
N/A (advanced feature)

**Expected Output**  
- `goblob/storage/tiering/tiering.go`
- Configuration updates
- `docs/advanced/tiered-storage.md`

### Task: Implement Cross-Region Replication

**Goal**  
Add asynchronous replication across geographically distributed clusters.

**Implementation Steps**

1. Create `goblob/replication/async/` package
2. Implement metadata change streaming between filers
3. Implement blob replication queue
4. Add conflict resolution for concurrent updates
5. Implement replication lag monitoring
6. Add replication topology configuration
7. Implement replication pause/resume
8. Write integration tests
9. Document cross-region setup

**Dependencies**  
Filer Server, Log Buffer, Replication Engine

**Related Plan**  
N/A (advanced feature)

**Expected Output**  
- `goblob/replication/async/async_replicator.go`
- Configuration updates
- `docs/advanced/cross-region-replication.md`

### Task: Implement Caching Layer

**Goal**  
Add distributed caching for frequently accessed blobs.

**Implementation Steps**

1. Create `goblob/cache/` package
2. Implement LRU cache for blob data
3. Add Redis-based distributed cache support
4. Implement cache invalidation on updates
5. Add cache statistics and hit rate monitoring
6. Implement cache warming strategies
7. Add cache configuration (size, TTL, eviction policy)
8. Write unit tests and benchmarks
9. Document caching strategy

**Dependencies**  
Volume Server, Client SDK

**Related Plan**  
N/A (advanced feature)

**Expected Output**  
- `goblob/cache/cache.go`
- `goblob/cache/redis_cache.go`
- Configuration updates
- `docs/advanced/caching.md`

### Task: Implement Data Deduplication

**Goal**  
Add content-based deduplication to reduce storage usage.

**Implementation Steps**

1. Create `goblob/storage/dedup/` package
2. Implement content hash calculation (SHA256)
3. Implement hash-to-needle mapping in filer store
4. Add reference counting for deduplicated blobs
5. Implement deduplication on upload
6. Add deduplication statistics
7. Implement garbage collection for unreferenced blobs
8. Write unit tests
9. Document deduplication behavior

**Dependencies**  
Storage Engine, Filer Server

**Related Plan**  
N/A (advanced feature)

**Expected Output**  
- `goblob/storage/dedup/dedup.go`
- Filer store schema updates
- `docs/advanced/deduplication.md`

### Task: Implement Quota Management

**Goal**  
Add per-user and per-bucket storage quotas.

**Implementation Steps**

1. Create `goblob/quota/` package
2. Implement quota tracking in filer store
3. Add quota enforcement on uploads
4. Implement quota reporting API
5. Add quota configuration per identity/bucket
6. Implement quota alerts and notifications
7. Write unit tests
8. Document quota configuration

**Dependencies**  
Filer Server, IAM

**Related Plan**  
N/A (advanced feature)

**Expected Output**  
- `goblob/quota/quota.go`
- API updates
- `docs/advanced/quota-management.md`

### Task: Implement Object Lifecycle Policies

**Goal**  
Add automatic object expiration and transition based on lifecycle rules.

**Implementation Steps**

1. Create `goblob/s3api/lifecycle/` package
2. Implement lifecycle rule parsing (S3 XML format)
3. Implement rule evaluation engine
4. Add background job for lifecycle processing
5. Implement object expiration
6. Implement object transition between storage tiers
7. Add lifecycle statistics
8. Write integration tests
9. Document lifecycle configuration

**Dependencies**  
S3 Gateway, Filer Server, Tiered Storage (optional)

**Related Plan**  
N/A (advanced feature)

**Expected Output**  
- `goblob/s3api/lifecycle/lifecycle.go`
- Background job implementation
- `docs/advanced/lifecycle-policies.md`

### Task: Implement WebDAV Interface

**Goal**  
Add WebDAV protocol support for filesystem-like access.

**Implementation Steps**

1. Create `goblob/webdav/` package
2. Implement WebDAV protocol handlers (PROPFIND, PROPPATCH, MKCOL, COPY, MOVE, LOCK, UNLOCK)
3. Integrate with filer server for metadata operations
4. Add WebDAV authentication
5. Implement WebDAV server initialization
6. Create `goblob/command/webdav.go` subcommand
7. Write integration tests
8. Document WebDAV setup

**Dependencies**  
Filer Server, Security

**Related Plan**  
N/A (advanced feature)

**Expected Output**  
- `goblob/webdav/webdav_server.go`
- `goblob/command/webdav.go`
- `docs/interfaces/webdav.md`


## Implementation Guidelines

### Development Workflow

1. **Start with Phase 1**: Foundation components have no dependencies and are required by everything else
2. **Follow dependency order**: Don't start a task until its dependencies are complete
3. **Test incrementally**: Write unit tests for each component before moving to the next
4. **Integration test early**: After Phase 4, run integration tests to verify server interactions
5. **Document as you go**: Update documentation when implementing each component

### Testing Strategy

- **Unit tests**: Test individual components in isolation (aim for 80%+ coverage)
- **Integration tests**: Test multi-component interactions (server startup, RPC communication)
- **End-to-end tests**: Test complete workflows (upload → download, S3 API operations)
- **Performance tests**: Benchmark critical paths (needle I/O, replication, metadata operations)
- **Chaos tests**: Test failure scenarios (network partitions, server crashes, disk failures)

### Code Organization

```
goblob/
├── blob.go                    # Main entry point
├── types/                     # Core types (Phase 1)
├── config/                    # Configuration (Phase 1)
├── observability/             # Logging and metrics (Phase 1)
├── security/                  # Security primitives (Phase 1)
├── pb/                        # Generated protobuf code (Phase 1)
├── storage/                   # Storage engine (Phase 2)
│   ├── needle/                # Needle format
│   ├── super_block/           # Volume headers
│   └── needle_map/            # Needle indexes
├── raft/                      # Raft consensus (Phase 3)
├── sequence/                  # File ID sequencer (Phase 3)
├── topology/                  # Cluster topology (Phase 3)
├── cluster/                   # Cluster registry (Phase 3)
├── replication/               # Replication engine (Phase 4)
├── wdclient/                  # Master client (Phase 4)
├── operation/                 # Client SDK (Phase 5)
├── filer/                     # Filer metadata (Phase 4)
│   ├── leveldb2/              # LevelDB store
│   ├── redis3/                # Redis store
│   ├── postgres/              # PostgreSQL store
│   └── lock/                  # Distributed locks
├── s3api/                     # S3 gateway (Phase 5)
│   └── iam/                   # IAM system
├── server/                    # Server implementations (Phase 4)
├── command/                   # CLI commands (Phase 6)
└── shell/                     # Admin shell (Phase 6)
```

### Configuration Management

- Use TOML for configuration files
- Support environment variable overrides
- Provide sensible defaults for development
- Document all configuration options
- Validate configuration on startup

### Error Handling

- Use structured errors with context
- Log errors with appropriate severity
- Return meaningful error messages to clients
- Implement retry logic for transient failures
- Add circuit breakers for cascading failures

### Performance Considerations

- Use connection pooling for gRPC clients
- Implement caching for frequently accessed data (volume locations, metadata)
- Use buffered I/O for needle operations
- Limit concurrent operations to prevent resource exhaustion
- Profile and optimize hot paths

### Security Best Practices

- Enable TLS by default in production
- Use JWT tokens for inter-service authentication
- Implement IP whitelisting for sensitive operations
- Validate all user inputs
- Audit log security-relevant operations
- Rotate secrets regularly

### Monitoring and Observability

- Expose Prometheus metrics for all operations
- Log structured JSON in production
- Implement health check endpoints
- Add distributed tracing for request flows
- Create alerting rules for critical metrics

### Deployment Recommendations

- **Development**: Use `blob server` all-in-one mode with single master
- **Staging**: Deploy separate master (3 nodes), volume (3+ nodes), filer (2+ nodes)
- **Production**: Add S3 gateway (2+ nodes), enable TLS, configure monitoring
- **High Availability**: Use Raft cluster for master (3 or 5 nodes), multiple replicas per volume

### Minimum Viable Product (MVP)

To achieve a working blob storage system, implement through Phase 4:

1. **Phase 1**: Foundation (types, config, protobuf, observability, security)
2. **Phase 2**: Storage Engine (needle format, volumes, store)
3. **Phase 3**: Cluster Coordination (Raft, sequencer, topology)
4. **Phase 4**: Core Servers (master, volume, filer with basic operations)

This MVP provides:
- Blob upload/download via HTTP API
- Distributed storage with replication
- High availability master with Raft
- File metadata management

Add Phase 5 (S3 Gateway) for S3 compatibility, Phase 6 (CLI) for operational tools, and Phase 7+ for production features.

### Critical Path Summary

The fastest path to a working system:

1. Core Types → Configuration → Protobuf → Security (1-2 weeks)
2. Needle Format → Volume → Store (1-2 weeks)
3. Sequencer → Topology (1 week)
4. Master Server → Volume Server (2 weeks)
5. Filer Store → Filer Server (2 weeks)
6. Integration Testing (1 week)

**Total MVP timeline**: 8-10 weeks for a small team

### Next Steps After Roadmap Completion

1. **Performance optimization**: Profile and optimize bottlenecks
2. **Feature expansion**: Implement advanced features from Phase 8
3. **Ecosystem integration**: Add support for backup tools, monitoring systems
4. **Client libraries**: Create SDKs for other languages (Python, Java, JavaScript)
5. **Community building**: Open source release, documentation, examples

---

## Appendix: Component Dependency Graph

```
Core Types (no dependencies)
    ↓
Configuration, Protobuf, Observability, Security
    ↓
Needle Format, SuperBlock
    ↓
NeedleMap, Volume
    ↓
DiskLocation, Store
    ↓
Compaction
    ↓
Raft, Sequencer
    ↓
Topology, Volume Growth
    ↓
Cluster Registry, Replication Engine
    ↓
gRPC Transport
    ↓
Master Server, Volume Server
    ↓
Filer Store, Distributed Lock, Log Buffer
    ↓
Filer Server
    ↓
Client SDK, IAM
    ↓
S3 Gateway
    ↓
Commands, Admin Shell
    ↓
Main Binary
```

This dependency graph shows the build order. Components on the same level can be implemented in parallel.

---

**End of Implementation Roadmap**

This roadmap provides a complete guide for building GoBlob from scratch. Follow the phases sequentially, test thoroughly, and refer to the detailed plan documents in `plans/` for implementation specifics of each component.
