# GoBlob Code Review Guide

This guide is for reviewing the full GoBlob codebase written by an LLM agent, phase by phase.
Each phase section tells you exactly which files to open, what to verify, and what LLM-generated
code most commonly gets wrong in that area.

---

## How to Use This Guide

1. Work through phases in order — later phases depend on earlier ones being correct.
2. For each file, open it and run through its checklist before moving on.
3. Run the verification commands at the end of each phase before declaring it done.
4. Use the **Common LLM Pitfalls** in each section — these are the most likely failure modes.
5. Use `go test -race ./goblob/<pkg>/...` after reviewing each package.

---

## General LLM Code Patterns to Watch For (All Phases)

These issues appear everywhere in LLM-generated Go code. Keep them in mind throughout:

| Pattern | What to check |
|---|---|
| **Silent error swallowing** | `_ = someErr` or missing error checks after function calls |
| **Goroutine leaks** | Goroutines started with no cancellation path, or no `ctx.Done()` exit |
| **Lock/unlock imbalance** | `mu.Lock()` without `defer mu.Unlock()` — especially in early-return paths |
| **Context not threaded** | `context.Background()` used deep inside call chains instead of passed `ctx` |
| **Test with no assertions** | Test functions that call code but never `t.Fatal` / `t.Error` on bad output |
| **Interface over-design** | Interface defined with one implementation and no clear second use case |
| **Stale TODO comments** | `// TODO: implement` on functions that return zero values |
| **Misleading function names** | Functions that do more (or less) than their name implies |
| **Off-by-one in pagination** | `includeStart` flag inverted, or cursor not advanced correctly |
| **Race on struct fields** | Struct fields read/written from multiple goroutines without a lock |

---

## Phase 0: Project Bootstrap

**Files to review**

- `go.mod` — module name, Go version, dependency versions
- `Makefile` — build, test, lint, proto targets
- `.gitignore` — generated files excluded (*.pb.go, binaries)
- `.golangci.yml` — linter config
- `goblob/blob.go` — main entry point, signal handling

**Checklist**

- [ ] Module name is `GoBlob` throughout — verify with `grep -r "module" go.mod` and that all imports use `GoBlob/goblob/...`
- [ ] Go version in `go.mod` matches `go.sum` and CI workflow
- [ ] `Makefile` `test` target uses `-race -count=1`; `build` target produces a binary
- [ ] `blob.go` handles SIGINT, SIGTERM (graceful shutdown) and SIGHUP (config reload) correctly — confirm signal channels are buffered (`make(chan os.Signal, 1)`)
- [ ] SIGHUP path calls the reload hook set in `command/reload.go`, not a no-op
- [ ] `blob.go` exits with non-zero code on command failure

**Common LLM Pitfalls**

- Signal channels must be buffered (`size 1`). An unbuffered channel drops the signal if the goroutine is busy.
- `context.WithCancel` must have its `cancel()` called — check there is a `defer cancel()`.
- `os.Exit` called directly instead of returning exit codes up through `command.Execute`.

---

## Phase 1: Foundation Layer

### Package: `goblob/core/types`

**Files:** `types.go`, `types_test.go`

**Checklist**

- [ ] `Offset` encoding: `ToEncoded(actualOffset) = Offset(actualOffset / 8)` and `ToActualOffset() = int64(o) * 8` — verify the math is symmetric and lossless for 8-byte aligned values
- [ ] `FileId.String()` format: `"<volumeId>,<hex(cookie)><hex(needleId)>"` — verify with the test, then manually parse one
- [ ] `ParseFileId` correctly rejects malformed strings (empty, wrong comma position, non-hex cookie/needleId)
- [ ] `ReplicaPlacement.TotalCopies()` returns `DifferentDataCenterCount + DifferentRackCount + SameRackCount + 1` — verify +1 is present (the primary copy)
- [ ] `TTL.Bytes()` and `ParseTTL` are inverse operations — verify round-trip in test
- [ ] `ServerAddress.ToGrpcAddress()` adds exactly 10000 to the HTTP port — check edge case where port is already a gRPC port
- [ ] `NeedleAlignmentSize = 8` is used consistently (not hardcoded `8` elsewhere)
- [ ] `TombstoneFileSize = 0` is used in deletion paths, not a magic `0` literal

**Common LLM Pitfalls**

- `Offset` arithmetic: LLMs sometimes use division instead of right-shift, or get the direction wrong (divide on encode, multiply on decode). Verify both directions.
- `Cookie` in `FileId` — must be a random uint32, not zero. Check if any code creates `FileId` with `Cookie: 0`.
- `ParseReplicaPlacementString("000")` should return `{0,0,0}` meaning 1 copy total. Verify the string format is documented.

---

### Package: `goblob/config`

**Files:** `master.go`, `volume.go`, `filer.go`, `security.go`, `config.go`, `validation.go`, `config_test.go`

**Checklist**

- [ ] `ValidateMasterConfig`: Raft peers count must be odd — verify this check exists and the error message is clear
- [ ] `ValidateVolumeConfig`: at least one directory required; port in valid range (1–65535)
- [ ] `SecurityConfig` has `JWT.Signing.Key` — verify it is never logged or included in error messages
- [ ] Config structs use `mapstructure` tags consistently — verify field names match TOML/YAML keys
- [ ] Default values are sensible: master port 9333, volume port 8080, filer port 8888, gRPC = HTTP + 10000
- [ ] `MaintenanceConfig.SleepMinutes` default is non-zero (prevents busy-loop)

**Common LLM Pitfalls**

- Validation functions that return `nil` for all inputs (stub validation).
- Security keys or passwords validated only for non-empty, not for minimum length or format.
- gRPC port derived at runtime from HTTP port, not hardcoded separately — confirm `GRPCPort` is not used as a static value but computed as `Port + 10000`.

---

### Package: `proto/`

**Files:** `master.proto`, `volume_server.proto`, `filer.proto`, `iam.proto`

**Checklist**

- [ ] Field numbers are stable (no re-numbering between proto files)
- [ ] `Heartbeat` message includes: VolumeId list, DataCenter, Rack, Ip, Port, MaxVolumeCount, Collection, HasSealedVolumes
- [ ] `HeartbeatResponse` includes: Leader address, VolumeSizeLimit
- [ ] `SubscribeMetadataRequest` includes: PathPrefix, SinceNs, ClientId, ClientName
- [ ] `SubscribeMetadataResponse` includes: EventType (CREATE/UPDATE/DELETE), Entry, TimestampNs
- [ ] Generated `*.pb.go` files are NOT committed (covered by `.gitignore`)
- [ ] `proto/` directory has a `buf.gen.yaml` or the Makefile `proto` target builds correctly

---

## Phase 2: Storage Engine

### Package: `goblob/storage/needle`

**Files:** `needle.go`, `needle_write.go`, `needle_read.go`, `needle_test.go`, `crc32.go`

**Checklist**

- [ ] Needle binary layout (V3):
  ```
  Header: Cookie(4) + NeedleId(8) + Size(4) = 16 bytes
  Body:   DataSize(4) + Data(N) + [optional fields based on Flags] + Padding(align to 8)
  Footer: Checksum(4) + AppendAtNs(8) = 12 bytes
  ```
  Verify `WriteTo` and `ReadFrom` produce the exact same byte sequence.
- [ ] CRC32 checksum is computed over `Data` only (not headers) — verify which bytes are included
- [ ] `Padding` calculation: total body must be 8-byte aligned — verify `(size + 7) &^ 7` or equivalent
- [ ] Optional fields (Name, Mime, LastModified, TTL, Pairs) are only written when their flag bit is set
- [ ] `Needle.IsDeleted()` returns true when `Size == 0` (tombstone) — verify `Size` field, not `DataSize`
- [ ] Version-specific handling: V1 has no AppendAtNs, V2 has, V3 has all fields — verify `switch version` covers all cases
- [ ] `needle_test.go` tests a round-trip: write needle → read back → compare all fields

**Common LLM Pitfalls**

- CRC32 computed on wrong bytes (e.g., including header or padding).
- Padding added incorrectly: `size % 8 == 0` case must add zero padding (no extra bytes needed).
- Flags byte not set when optional fields are populated.
- `AppendAtNs` written as 8-byte big-endian — verify byte order matches read path.

---

### Package: `goblob/storage/volume`

**Files:** `volume.go`, `superblock.go`, `needle_map.go`, `disk_location.go`, `compact.go`

**Checklist**

**volume.go**
- [ ] Volume files are `{dir}/{collection}_{volumeId}.dat` and `.idx` — verify naming convention
- [ ] Write: appends needle to `.dat`, updates `.idx` entry — both must succeed or be rolled back
- [ ] Read: looks up offset/size in `.idx`, seeks `.dat` file at `offset * 8`, reads `size` bytes
- [ ] Delete: writes tombstone needle (Size=0) to `.dat`, updates `.idx` with Size=0
- [ ] `ReadOnly` flag prevents writes — verify returns error, not silent ignore
- [ ] Volume size check: refuse writes when `datFile.Size() >= MaxVolumeSize (8GB)`
- [ ] File descriptors are closed on `Volume.Close()`

**superblock.go**
- [ ] SuperBlock is exactly 8 bytes at offset 0 in `.dat` — verify struct layout
- [ ] Fields: Version(1) + ReplicaPlacement(1) + Ttl(2) + Collection len(1) + DiskType(1) + padding(2)
- [ ] `SuperBlock.ReadFrom` and `SuperBlock.WriteTo` are inverse operations — verify round-trip

**needle_map.go**
- [ ] In-memory map: `map[types.NeedleId]types.NeedleValue` — verify NeedleValue stores Offset + Size
- [ ] LevelDB map: key = NeedleId (8 bytes big-endian), value = Offset(4) + Size(4) = 8 bytes
- [ ] Lookup returns `(NeedleValue, found bool)` — verify callers check `found`
- [ ] `NeedleValue.IsDeleted()` checks `Size == 0`

**disk_location.go**
- [ ] Scans directory for `*.dat` files on startup to load existing volumes
- [ ] Volume ID extracted from filename: parse `{collection}_{id}.dat`
- [ ] `GetVolume(id)` returns `(*Volume, bool)` — verify concurrent access uses a lock
- [ ] Free space calculation includes all volumes on the disk, not just one

**compact.go**
- [ ] Compaction copies all non-deleted needles to a new `.dat` file
- [ ] Compaction is atomic: new file written first, then replaces old on success
- [ ] Old `.idx` is rebuilt from compacted `.dat`
- [ ] `compactionBytePerSecond` throttling is applied

**Common LLM Pitfalls**

- Volume write without fsync — data loss on crash. Check if `.dat` writes are fsynced or if there's an explicit flush.
- Needle map not updated on delete (only tombstone written to `.dat`).
- Race condition on `Volume.ReadOnly` flag — check it is read atomically.
- `disk_location.LoadExistingVolumes` skips files with unexpected naming — verify error handling.

---

### Package: `goblob/storage`

**Files:** `store.go`, `store_test.go`

**Checklist**

- [ ] `Store.WriteVolumeNeedle(volumeId, needle)`: finds volume → delegates to `Volume.WriteNeedle`
- [ ] `Store.ReadVolumeNeedle(volumeId, fid)`: finds volume → `Volume.ReadNeedle(fid.NeedleId)`
- [ ] `Store.DeleteVolumeNeedle(volumeId, needleId)`: finds volume → `Volume.DeleteNeedle`
- [ ] Returns a typed error (not just `"volume not found"` string) when volume ID is unknown
- [ ] `ReloadExistingVolumes` re-scans disk dirs and mounts any new `.dat` files
- [ ] `Store.Locations` slice is not modified concurrently — verify it's set at startup only
- [ ] Heartbeat info: `Store.CollectHeartbeat()` returns accurate volume metadata for master

**Common LLM Pitfalls**

- `WriteVolumeNeedle` returning success even when the volume is read-only (bug: should return error).
- Error messages that leak internal paths.

---

## Phase 3: Cluster Coordination

### Package: `goblob/sequence`

**Files:** `sequencer.go`, `file_sequencer.go`, `raft_sequencer.go`, `snowflake_sequencer.go`

**Checklist**

- [ ] `Sequencer` interface: `NextFileId(count uint64) uint64`, `SetMax(max uint64)`, `GetMax() uint64`, `Close()`
- [ ] `FileSequencer`: persists current max to a file; reads on startup; `NextFileId` increments by `count + StepSize` (StepSize pre-allocated to avoid every write hitting disk)
- [ ] `RaftSequencer`: only leader can call `NextFileId` — verify it refuses if not leader
- [ ] IDs are monotonically increasing even across restarts — verify file sequencer reads max from disk on init
- [ ] `SetMax` only advances (never decrements) — verify `if newMax > current { current = newMax }`
- [ ] Concurrent calls to `NextFileId` are safe — verify mutex or atomic

**Common LLM Pitfalls**

- `NextFileId` returning the same ID twice under concurrent calls (missing lock).
- `FileSequencer` not calling `SetMax` on startup — IDs restart from 0 after crash.
- `StepSize` too small (1) means every ID assignment writes to disk.

---

### Package: `goblob/raft`

**Files:** `raft_server.go`, `fsm.go`, `config.go`, `raft_test.go`

**Checklist**

- [ ] `RaftServer` interface is complete: `IsLeader()`, `LeaderAddress()`, `Apply(cmd, timeout)`, `Barrier(timeout)`, `AddPeer`, `RemovePeer`, `Stats()`, `Shutdown()`
- [ ] `NewRaftServer`: creates BoltDB store for log/stable storage; Raft transport over TCP; bootstraps single-node cluster if first start
- [ ] `IsLeader()` uses `raft.State() == raft.Leader` — not a cached field that could be stale
- [ ] `LeaderAddress()` returns empty string if no leader yet — verify callers handle this
- [ ] `MasterFSM.Apply(log)` is idempotent — applying the same log entry twice produces the same state
- [ ] `onLeaderChange` callback is called on transition to/from leader — used for metrics (`MasterLeadershipGauge`)
- [ ] `raft_test.go` tests leader election in a 3-node cluster (not just 1-node)

**Common LLM Pitfalls**

- `IsLeader()` checking a `bool` field instead of `raft.State()` — causes split-brain bugs.
- FSM `Apply` not deserializing the log payload before applying — silently applies empty state.
- Bootstrap called on every startup (instead of only when data dir is empty) — prevents cluster from forming.
- `Barrier()` not called before reading Raft-committed state in tests.

---

### Package: `goblob/topology`

**Files:** `topology.go`, `data_center.go`, `rack.go`, `data_node.go`, `node.go`, `volume_layout.go`, `volume_growth.go`

**Checklist**

**topology.go**
- [ ] `ProcessJoinMessage(hb *Heartbeat)`: finds or creates `DataCenter → Rack → DataNode` path, updates volume list
- [ ] Node lookup is under a write lock — verify `TopoLock` or equivalent protects the tree
- [ ] `LookupVolumeId(vid)`: returns all DataNode addresses that have that volume
- [ ] `PickForWrite(count, option)`: selects a DataNode with capacity, respects DataCenter/Rack/DiskType filters
- [ ] Dead node detection: nodes not heartbeating for > threshold are marked as dead (not immediately removed)

**volume_growth.go**
- [ ] `AutomaticGrowByType`: called when writable volumes run low; allocates new volumes via gRPC to volume servers
- [ ] Growth respects ReplicaPlacement — creates `TotalCopies()` volumes distributed across nodes
- [ ] No infinite loop if no nodes have capacity — returns error after checking all nodes

**volume_layout.go**
- [ ] Separates writable volumes from read-only volumes
- [ ] `PickForWrite` prefers volumes that are less than 80% full
- [ ] `SetVolumeCapacityFull` moves a volume from writable to read-only list

**Common LLM Pitfalls**

- `ProcessJoinMessage` called without holding `TopoLock` — race condition under concurrent heartbeats.
- `PickForWrite` returns an error 503 when no volumes are writable — verify the error type/code.
- Volume growth loop not bounded — can keep creating volumes if allocation fails silently.
- DataNode equality compared by pointer instead of by address string.

---

### Package: `goblob/cluster`

**Files:** `registry.go`, `registry_test.go`

**Checklist**

- [ ] `Register` is idempotent — re-registering same nodeId updates LastSeen, not creates duplicate
- [ ] `ExpireDeadNodes` removes nodes where `time.Since(LastSeen) > expireSeconds` — verify the comparison direction
- [ ] `ForEachNode` does not hold the lock while calling `fn` (to avoid deadlocks)
- [ ] `ListNodes` filters by clientType correctly — verify empty string returns all nodes
- [ ] Thread safety: all map reads/writes behind `sync.RWMutex`

---

## Phase 4: Core Servers

### Package: `goblob/filer` (metadata store)

**Files:** `filer.go`, `filer_store.go`, `entry.go`, `filer_conf.go`, `lock_manager.go`

**Checklist**

**filer_store.go (interface)**
- [ ] `ListDirectoryEntries` pagination: `startFileName` is the cursor, `includeStart` controls whether the cursor entry is returned, `limit` caps results — verify the semantics match all backends
- [ ] `BeginTransaction` / `CommitTransaction` / `RollbackTransaction` are implemented (even if no-op for LevelDB)
- [ ] `KvPut` / `KvGet` / `KvDelete` use a distinct key namespace from entry keys (e.g., `__kv__:` prefix)

**entry.go**
- [ ] `Entry.IsDirectory()` returns `entry.Attr.Mode.IsDir()` — not a custom flag
- [ ] `FullPath.DirAndName()` correctly handles root `/` (dir=`/`, name=`""`)
- [ ] `FileChunk` `Offset` and `Size` are int64, not int32 — verify for large files
- [ ] `Entry.Content` is only populated for small inline files (< 64KB) — chunked files use `Chunks`

**filer_store backends** (check each):

| Backend | File | Key check |
|---|---|---|
| LevelDB2 | `filer/leveldb2/leveldb2_store.go` | Key format: `dir + "\x00" + name` |
| Redis3 | `filer/redis3/redis_store.go` | `HSET {dir} {name} {bytes}` |
| Postgres2 | `filer/postgres2/postgres_store.go` | Table `filer_meta(dir_hash, dir, name, meta)` |
| MySQL2 | `filer/mysql2/mysql_store.go` | Same schema as Postgres |
| Cassandra | `filer/cassandra/cassandra_store.go` | Partition key = dir |

For each backend, verify:
- [ ] `ListDirectoryEntries` pagination cursor is correctly advanced (returns last-seen name, not index)
- [ ] `DeleteFolderChildren` deletes ALL children, not just direct children
- [ ] `FindEntry` returns `filer.ErrNotFound` (not a raw DB "not found") so callers can distinguish

**lock_manager.go**
- [ ] Lock stored as KV entry: key `__lock__:<name>`, value = JSON with owner + expiry
- [ ] `TryLock` is atomic: check-then-set must be atomic (use KV compare-and-set or transaction)
- [ ] Expired locks are detected on `TryLock` attempt, not via a background cleaner
- [ ] `Unlock` verifies the caller is the lock owner before deleting

**Common LLM Pitfalls**

- `ListDirectoryEntries` returning entries from the wrong directory (key prefix scan bug).
- Lock manager `TryLock` using separate `KvGet` + `KvPut` without a transaction (TOCTOU race).
- `FindEntry` returning `nil, nil` instead of `nil, ErrNotFound` when entry is missing.
- Backend `Initialize` called more than once — verify it is idempotent.

---

### Package: `goblob/wdclient`

**Files:** `master_client.go`, `vidmap.go`, `dial.go`

**Checklist**

- [ ] `KeepConnectedToMaster`: runs a goroutine that dials the current leader, subscribes to `KeepConnected` stream, retries with `time.Sleep(1 * time.Second)` on error
- [ ] On receiving a response with a new leader address, updates `currentMaster` atomically
- [ ] `GetCurrentMaster()` is safe to call from any goroutine (protected by mutex)
- [ ] `VidCache.Get` returns `(locs, false)` on expiry — verify TTL comparison uses `time.Now().After(entry.expiresAt)`
- [ ] `GetDialOption`: insecure when `CertFile == ""`; mTLS credentials otherwise
- [ ] gRPC port offset: `ToGrpcAddress()` adds 10000 — `"localhost:9333"` → `"localhost:19333"`

**Common LLM Pitfalls**

- `KeepConnectedToMaster` goroutine not reading the `ctx.Done()` channel — leaks on shutdown.
- `VidCache` TTL set at `Put` time but checked in wall-clock time — verify `lastSet + TTL < now`, not `age > TTL`.

---

### Package: `goblob/pb`

**Files:** `grpc_client.go`

**Checklist**

- [ ] `WithMasterServerClient`: `grpc.NewClient(addr, opt)` → `defer conn.Close()` → call `fn(client)` → return error
- [ ] Each `WithXxxClient` creates a new connection per call (no caching) — this is intentional
- [ ] All three helpers (`Master`, `VolumeServer`, `Filer`) follow the exact same pattern
- [ ] `grpc.NewClient` is used (not deprecated `grpc.Dial`)

---

### Package: `goblob/replication`

**Files:** `replicator.go`, `replica_locations.go`, `replication_test.go`

**Checklist**

- [ ] `HTTPReplicator.ReplicatedWrite`: sends needle to ALL replicas in parallel goroutines — not sequential
- [ ] Returns error if ANY replica fails — not majority-wins
- [ ] Sets `X-Replication: true` header on replica requests to prevent cascading replication
- [ ] Volume server receiving a request with `X-Replication: true` skips calling `ReplicatedWrite`
- [ ] `ReplicaLocations.Get(vid)` returns a copy of the slice, not the internal slice (prevents data races)
- [ ] `ReplicaLocations` update happens on heartbeat processing in topology

**Common LLM Pitfalls**

- Goroutine for each replica but result channel not drained if context is cancelled — goroutine leak.
- `X-Replication` header checked case-sensitively — verify the exact string used in both set and check.
- Replica error aggregation uses `errors.Join` (Go 1.20+) — verify Go version supports it, or use a slice.

---

### Package: `goblob/operation`

**Files:** `assign.go`, `upload.go`, `lookup.go`, `client.go`, `volumelocationcache.go`

**Checklist**

- [ ] `Assign`: POST to `/dir/assign?collection=X&replication=Y&ttl=Z&count=N` — verify all query params are correctly URL-encoded
- [ ] Returns `ErrNoWritableVolumes` specifically on HTTP 503 — callers use this to retry
- [ ] `Upload`: PUT to `http://{url}/{fid}` with body as multipart or raw binary; JWT in `Authorization: Bearer {jwt}`
- [ ] `Upload` checks `UploadResult.Error` field even on HTTP 201 — some logic errors come back as 201 with error JSON
- [ ] `LookupVolumeId`: uses `VolumeLocationCache`; on cache miss, fetches from master; on cache hit, skips network
- [ ] `UploadWithRetry`: retries on network errors and 5xx; does NOT retry on 4xx
- [ ] `ChunkUpload`: splits data into 8MB chunks; uploads with worker pool (verify pool size is bounded)

**Common LLM Pitfalls**

- `Assign` not URL-encoding the collection name (fails if collection has special chars).
- `Upload` multipart boundary not unique per request (though rare, LLMs sometimes hardcode it).
- `ChunkUpload` not assembling chunks in order — filer entry `Chunks` must have correct `Offset` values.
- `VolumeLocationCache` TTL race: get → expired check → set done non-atomically.

---

### Package: `goblob/server` (Master)

**Files:** `master_server.go`, `master_server_handlers.go`, `master_option.go`, `master_grpc_server.go`

**Checklist**

**master_server.go**
- [ ] `NewMasterServer`: creates Topology, VolumeGrowth, Sequencer, Guard; registers HTTP routes; starts background goroutines
- [ ] `startAdminScripts()`: ticker fires every `MaintenanceConfig.SleepMinutes` minutes; only runs if `IsLeader()`
- [ ] `ProcessGrowRequest()` goroutine: drains `volumeGrowthRequestChan` — verify channel is buffered (prevents blocking assign handler)
- [ ] Graceful shutdown: stops heartbeat, stops admin scripts, closes Raft

**master_server_handlers.go**
- [ ] `handleAssign`:
  1. Check `IsLeader()` → if not: `proxyToLeader(w, r)` and return
  2. Call `Topo.PickForWrite(count, option)`
  3. Call `Sequencer.NextFileId(count)` to assign NeedleId
  4. Generate JWT: `security.SignJWT(key, expiresAfterSec)`
  5. Return JSON: `{fid, url, publicUrl, count, auth}`
  - Returns HTTP 503 (not 500) when no writable volumes
- [ ] `handleLookup`: NO leader check required — any master can answer; parse `volumeId` from query
- [ ] `handleVolGrow`: leader check; enqueues to `volumeGrowthRequestChan` (non-blocking)
- [ ] `handleVacuum`: leader check; calls `topology.Vacuum(garbageThreshold, collection)`
- [ ] `proxyToLeader`: uses `httputil.NewSingleHostReverseProxy` targeting `"http://" + Raft.LeaderAddress()`

**master_grpc_server.go**
- [ ] `SendHeartbeat(stream)`: `stream.Recv()` in a loop (NOT called once); calls `Topo.ProcessJoinMessage(hb)` for each; sends back `HeartbeatResponse{Leader, VolumeSizeLimit}`
- [ ] `KeepConnected(stream)`: for filer/S3 registration; registers in `ClusterRegistry`; removes on stream close
- [ ] On leader change: sends new leader address in `HeartbeatResponse` so clients reconnect

**Common LLM Pitfalls**

- `handleAssign` calling `Sequencer.NextFileId` BEFORE `PickForWrite` — wrong order, wastes IDs on failures.
- `handleAssign` on follower not proxying — just returning an error instead.
- `handleVolGrow` not checking `IsLeader()` — followers cannot grow volumes.
- `SendHeartbeat` calling `stream.Recv()` once instead of in a loop — misses subsequent heartbeats.
- `proxyToLeader` using HTTP scheme when TLS is enabled — should be `https://` if TLS is configured.

---

### Package: `goblob/server` (Volume)

**Files:** `volume_server.go`, `volume_option.go`, `volume_grpc_server.go`

**Checklist**

**volume_server.go**
- [ ] `handleWrite`:
  1. Upload throttle via `uploadLimitCond`
  2. Guard authorization check
  3. Parse `fid` from URL path
  4. Build needle from request body
  5. Write to store
  6. Increment metrics
  7. If NOT `X-Replication: true` AND replicator set: replicate to all replicas in parallel
  8. Return HTTP 201 with JSON `{fid, size, eTag}`
- [ ] `handleRead`:
  1. Download throttle
  2. Guard authorization
  3. Parse `fid`
  4. Check `blobCache` (cache-aside) — serve from cache on hit
  5. Read from store on miss, populate cache
  6. Set `Content-Type`, `Content-Length`, `ETag` headers
  7. Return HTTP 200 with body
- [ ] `handleDelete`: authorizes, deletes from store, invalidates cache entry
- [ ] `heartbeatLoop`: sends heartbeat to master every `HeartbeatInterval`; on receiving response: updates current master address, checks if volume size limit changed
- [ ] Graceful shutdown: `PreStopSeconds` delay (master reroutes traffic) → stop gRPC → close store

**volume_grpc_server.go**
- [ ] `AllocateVolume`: creates new volume on disk
- [ ] `VolumeDelete`: deletes volume files from disk
- [ ] `VolumeCopy`: copies volume from another volume server
- [ ] All gRPC handlers properly propagate `ctx` for cancellation

**Common LLM Pitfalls**

- `handleWrite` calling `replicator.ReplicatedWrite` AFTER returning HTTP 201 — client thinks write succeeded before replication
- Cache `Put` called on every read (including 404) — should only populate on successful read
- `heartbeatLoop` not exiting when `stopChan` is closed — goroutine leak on shutdown
- `handleRead` mime type detection: `n.GetMime()` returning empty string → must default to `"application/octet-stream"`

---

### Package: `goblob/server` (Filer)

**Files:** `filer_server.go`, `filer_option.go`, `filer_grpc_server.go`, `iam_grpc_server.go`

**Checklist**

**filer_server.go**
- [ ] `handleFileUpload`:
  1. Throttle via `inFlightLimitCond`
  2. Authorization check
  3. Read body (max 64KB inline, else `StatusNotImplemented`)
  4. Create `Entry` with `FullPath`, `Attr`, `Content`
  5. Call `filer.CreateEntry(ctx, entry)`
  6. Append to `logBuffer`
  7. Broadcast to listeners
  8. Return HTTP 201 with JSON
- [ ] `handleFileDownload`: returns `entry.Content` for inline; proxies from volume server for chunked
- [ ] `handleDeleteEntry`: deletes entry from filer; also cleans up volume chunks asynchronously
- [ ] `loopProcessingDeletion`: background goroutine drains deletion queue; deletes volume needles

**filer_grpc_server.go**
- [ ] `ListEntries`: uses `filer.ListDirectoryEntries` with pagination; correctly passes cursor between pages
- [ ] `SubscribeMetadata`: streams `LogBuffer` entries to subscribers; handles `SinceNs` correctly (catch-up from past)
- [ ] `AtomicRenameEntry`: uses `BeginTransaction` → rename → `CommitTransaction` atomically

**iam_grpc_server.go**
- [ ] `GetIAMConfiguration`: returns current IAM config from store
- [ ] `PutIAMConfiguration`: validates and persists new IAM config
- [ ] Config updates hot-reload the IAM manager (verify `iam.Reload()` is called)

**Common LLM Pitfalls**

- `SubscribeMetadata` not respecting `SinceNs` — always starts from current time, missing catch-up events.
- `loopProcessingDeletion` not exiting cleanly on `ctx.Done()`.
- `AtomicRenameEntry` not rolling back on partial failure.
- `handleFileUpload` limit check (`> 64*1024`) is a known temporary stub — verify the TODO comment is present.

---

## Phase 5: Client Interfaces

### Package: `goblob/s3api`

**Files:** `server.go`, `object_handlers.go`, `bucket_handlers.go`, `multipart_handlers.go`, `xml.go`, `errors.go`

**Checklist**

**server.go**
- [ ] Routes registered: `GET /` (list buckets), `PUT /{bucket}`, `DELETE /{bucket}`, `GET /{bucket}`, `PUT /{bucket}/{key+}`, `GET /{bucket}/{key+}`, `HEAD /{bucket}/{key+}`, `DELETE /{bucket}/{key+}`, `POST /{bucket}/{key+}?uploads` (initiate multipart), `PUT /{bucket}/{key+}?partNumber=N&uploadId=X`, `POST /{bucket}/{key+}?uploadId=X` (complete multipart), `DELETE /{bucket}/{key+}?uploadId=X` (abort multipart)
- [ ] All handlers go through auth middleware (`s3api/auth/auth.go` → IAM check)
- [ ] Quota check happens BEFORE writing data, not after

**object_handlers.go**
- [ ] `putObject`: max single object 256MB (`maxSinglePutObjectSize`); larger → must use multipart
- [ ] `getObject`: supports `Range` header for partial reads; `If-None-Match` / `If-Modified-Since` conditional GET
- [ ] `headObject`: returns headers without body; same auth as GET
- [ ] `deleteObject`: returns 204 (not 200) on success
- [ ] ETag is MD5 of object body (hex, no quotes for regular upload; `"hash-N"` for multipart)

**s3api/auth/auth.go**
- [ ] `VerifyRequest` parses Authorization header: `AWS4-HMAC-SHA256 Credential=.../SignedHeaders=.../Signature=...`
- [ ] Canonical request: `Method\nURI\nQueryString\nHeaders\nSignedHeaders\nPayloadHash`
- [ ] String to sign: `AWS4-HMAC-SHA256\nDate\nCredentialScope\nHashOfCanonicalRequest`
- [ ] Signature: `HMAC(signingKey, stringToSign)` where `signingKey = HMAC(HMAC(HMAC(HMAC("AWS4" + secretKey, date), region), service), "aws4_request")`
- [ ] Constant-time comparison for signature to prevent timing attacks
- [ ] Presigned URL: check `X-Amz-Expires` is not past `time.Now()`

**s3api/iam/iam.go**
- [ ] `GetCredential(accessKey)` returns `(Identity, bool)` — verify thread-safe read
- [ ] `IsAllowed(identity, action, resource)` checks action wildcards (e.g., `s3:*` matches `s3:PutObject`)
- [ ] `Reload(newConfig)` atomically swaps the identity map — uses `sync/atomic` or `sync.RWMutex`
- [ ] HMAC key pools used for constant-time secret key comparison

**s3api/multipart**
- [ ] Minimum part size: 5MB (except last part)
- [ ] `CompleteMultipartUpload`: verifies parts are in order by part number; concatenates chunks
- [ ] `AbortMultipartUpload`: cleans up all uploaded parts
- [ ] `uploadId` format is unique per upload session (e.g., UUID or random hex)

**Common LLM Pitfalls**

- S3 key (object path) not URL-decoded before forwarding to filer — paths with `%2F` break.
- ETag includes or omits quotes inconsistently (`"abc"` vs `abc`).
- SigV4: signed headers list not sorted alphabetically → signature mismatch.
- Multipart `CompleteMultipartUpload` not validating ETags match uploaded parts → corrupt objects silently accepted.
- IAM `IsAllowed` not handling resource ARN wildcards (`arn:aws:s3:::bucket/*`).

---

### Package: `goblob/webdav`

**Files:** `webdav_server.go`, `filesystem.go`

**Checklist**

- [ ] `NewServer` signature: `(addr, fs, username, password string, middleware func(http.Handler) http.Handler)` — middleware applied after auth wrapper
- [ ] Auth wrapper: if `username == ""`, skip auth entirely (unauthenticated mode)
- [ ] `FilerFileSystem` implements `golang.org/x/net/webdav.FileSystem` interface: `Mkdir`, `OpenFile`, `RemoveAll`, `Rename`, `Stat`
- [ ] Remote mode (gRPC-backed): all operations delegate to filer gRPC client
- [ ] `Mkdir` with `perm` correctly sets `entry.Attr.Mode` as a directory
- [ ] `Rename` is atomic on the filer side (uses `AtomicRenameEntry`)
- [ ] Graceful shutdown: `httpSrv.Shutdown(ctx)` with timeout

**Common LLM Pitfalls**

- `OpenFile` flag handling: `os.O_CREATE` → create entry; `os.O_RDONLY` → read-only; `os.O_WRONLY|os.O_TRUNC` → overwrite — verify all flag combinations handled.
- `Stat` returning wrong `IsDir()` for root path `/`.
- gRPC connection not closed on `FilerFileSystem.Close()`.

---

## Phase 6: Command Line Interface

### Package: `goblob/command`

**Files:** `command.go`, `master.go`, `volume.go`, `filer.go`, `s3.go`, `server.go`, `shell.go`, `reload.go`, `replicator.go`

**Checklist**

**command.go**
- [ ] `Register` is idempotent — re-registering same name overwrites (no panic)
- [ ] `Execute` returns exit codes: 0 (success), 1 (runtime error), 2 (usage error)
- [ ] `help` and `version` pseudo-commands handled before looking up in registry
- [ ] Unknown command: helpful error message pointing to `blob help`

**command/runtime.go**
- [ ] `startVolumeRuntime`, `startMasterRuntime`, `startFilerRuntime`, `startS3Runtime`: each creates its server, wraps mux with `security.ApplyHardening`
- [ ] `ApplyHardening` called with non-nil `Logger` field (not `nil` which would lose audit logs)
- [ ] HTTP server `ReadHeaderTimeout` set (prevents Slowloris)
- [ ] gRPC server created with `grpc.MaxRecvMsgSize` limit

**reload.go**
- [ ] `setReloadHook(fn)` thread-safe (uses mutex or atomic pointer)
- [ ] `HandleSIGHUP` calls the current reload hook; logs if hook is nil (not panic)

**server.go (all-in-one)**
- [ ] Startup sequence: master first → wait for Raft election → volume → filer → S3
- [ ] Wait between components uses a sleep or readiness poll (not fixed `time.Sleep`)
- [ ] Shutdown in reverse order of startup

**replicator.go**
- [ ] `-targetFiler` validated as non-empty before starting
- [ ] Calls `r.Start(ctx)` and only returns on `ctx.Done()` or error
- [ ] Metrics port started with `startMetricsRuntime`

**Common LLM Pitfalls**

- `server.go` startup not waiting for master to elect a leader before starting volume servers — volume server fails to heartbeat.
- `reload.go` hook called from signal handler goroutine without a mutex — data race.
- Command `Run` not respecting `ctx.Done()` — hangs on shutdown.

---

### Package: `goblob/shell`

**Files:** `shell.go`, `command.go`, `env.go`, `command_cluster.go`, `command_fs.go`, `command_volume.go`, `command_lock.go`, `command_maintenance.go`

**Checklist**

- [ ] `Shell.Run` reads lines from stdin (or readline); parses with `shellSplit` (handles quoted strings); dispatches to registered command
- [ ] `shellSplit("volume.list 'name with spaces'")` → `["volume.list", "name with spaces"]` — verify quote handling
- [ ] `CommandEnv` holds master address, filer address, grpc dial option — passed to all shell commands
- [ ] Tab completion works for command names (not required to complete arguments)
- [ ] `volume.list` shows: VolumeId, Collection, ReplicaPlacement, DataCenter/Rack/DataNode, Size, ReadOnly
- [ ] `fs.ls <path>` shows: name, size, mode, mtime for each entry
- [ ] `lock.status` shows: lock name, owner, expiry
- [ ] Unknown commands return a helpful error, not a panic
- [ ] CTRL-C / SIGINT in shell exits the shell gracefully (not the whole process)

**Common LLM Pitfalls**

- `shellSplit` not handling escaped quotes (`\"`) inside quoted strings.
- Shell commands that ignore `ctx` passed to `Run` — hang if filer/master is unavailable.
- `CommandEnv` master address not updated when master leader changes.

---

## Phase 7: Production Readiness

### Package: `goblob/obs`

**Files:** `logger.go`, `metrics.go`, `prometheus.go`, `metrics_server.go`, `push.go`

**Checklist**

- [ ] `obs.New("subsystem")` returns a `*slog.Logger` with a `"subsystem"` attribute pre-set
- [ ] `SetLevel` uses `slog.LevelVar` (atomic — safe to call at runtime)
- [ ] All Prometheus metrics use namespace `"goblob"` prefix consistently
- [ ] Metrics registered in `init()` with `prometheus.MustRegister` — if any metric is registered twice it will panic at startup (check for duplicate registrations across packages)
- [ ] `MetricsServer`: `/metrics` → Prometheus handler, `/debug/pprof/*` → pprof, `/debug/vars` → expvar
- [ ] `StartMetricsPusher`: runs on a ticker; uses `push.New(url, job).Gatherer(prometheus.DefaultGatherer).Push()`
- [ ] Metrics server uses a **separate** HTTP mux from the application mux

**Common LLM Pitfalls**

- Same metric registered in both `obs/metrics.go` and another package — causes panic at startup.
- `obs.New` creating a new logger on every call (expensive) instead of returning a cached instance per subsystem.
- Push interval hardcoded instead of from config.

---

### Package: `goblob/security`

**Files:** `jwt.go`, `middleware.go`, `tls.go`, `hardening.go`, `rate_limit.go`, `size_limit.go`, `audit.go`, `headers.go`, `validation.go`, `jwt_fuzz_test.go`

**Checklist**

- [ ] `SignJWT`: uses HS256; token includes `exp` claim; signing key never logged
- [ ] `VerifyJWT`: verifies signature AND checks `exp` claim is not expired; returns `ErrTokenExpired` on expiry
- [ ] `jwt_fuzz_test.go` uses `f.Fuzz` — verify `FuzzVerifyJwt` has a `defer recover()` to assert no panics
- [ ] `ApplyHardening` chain order: `RateLimiter → MaxBytesReader → AuditLog → SecurityHeaders → handler`
  - Rate limiter first (cheapest rejection)
  - MaxBytesReader wraps body before handler reads it
- [ ] `RateLimiter.cleanupLocked` runs periodically to prevent memory growth from unique IPs
- [ ] `SecurityHeaders` sets: `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `X-XSS-Protection: 1; mode=block`, `Content-Security-Policy: default-src 'self'`, `Referrer-Policy: strict-origin-when-cross-origin`
- [ ] `audit.go`: logs requests where `status >= 400` OR `method in {POST, PUT, DELETE, PATCH}` OR `isAuthenticated`
- [ ] `ValidatePath`: rejects paths containing `..`; rejects absolute paths starting with `/`
- [ ] `ValidateFilename`: rejects `.` and `..`; rejects names longer than 255 bytes

**Common LLM Pitfalls**

- `VerifyJWT` not checking `exp` claim — accepts expired tokens.
- `AuditLog` using a value-type `responseWriter` (misses `Flush`, `Hijack` interface — breaks streaming).
- `rate_limit.go` `allow()` not thread-safe — concurrent calls to `getLimiter` without mutex.
- `SecurityHeaders` missing `Content-Security-Policy` or `Referrer-Policy` headers.

---

### Integration Tests: `test/`

**Files:** `integration/integration_test.go`

**Checklist**

- [ ] Uses `//go:build integration` tag — not run by default `go test ./...`
- [ ] `TestEndToEndBlobUploadDownload`: uploads a blob through the full stack, downloads it, compares bytes
- [ ] `TestFilerMetadataOps`: creates, lists, deletes entries; verifies pagination
- [ ] `TestS3HealthEndpoints`: hits `/health` and `/ready` endpoints
- [ ] Tests allocate random ports via `net.Listen(":0")` — no hardcoded port conflicts
- [ ] `t.Cleanup()` used for teardown (not `defer` inside loops)
- [ ] Tests pass with `-race` flag

---

### Deployment: `Dockerfile`, `docker-compose.yml`, `k8s/`

**Checklist**

**Dockerfile**
- [ ] Multi-stage build: builder stage (Go) + runtime stage (distroless or alpine)
- [ ] Binary is statically linked: `CGO_ENABLED=0 GOOS=linux go build`
- [ ] Runtime stage runs as non-root user
- [ ] No secrets or credentials in any layer

**docker-compose.yml**
- [ ] Master service has health check with `wget -qO- http://localhost:9333/health` or equivalent
- [ ] Volume service `depends_on` master with `condition: service_healthy`
- [ ] Filer service `depends_on` master and volume
- [ ] Persistent volumes mapped for master metadata and volume data directories

**k8s/**
- [ ] Master: `StatefulSet` (stable network identity for Raft peers) with `PersistentVolumeClaim`
- [ ] Volume: `StatefulSet` with `PersistentVolumeClaim` per replica
- [ ] Filer/S3: `Deployment` (stateless)
- [ ] `livenessProbe` and `readinessProbe` configured on all containers
- [ ] Resource `requests` and `limits` set
- [ ] No hardcoded image tags (use `{{ .Values.image.tag }}` or a specific version)

**Common LLM Pitfalls**

- `docker-compose.yml` volume mounts using relative paths that break in CI.
- K8s manifests missing `namespace` field — deploys to `default` silently.
- Liveness probe using `/health` when `/ready` is what should gate traffic.

---

## Phase 8: Advanced Features

### Package: `goblob/storage/erasure_coding`

**Files:** `ec.go`, `encoder.go`, `decoder.go`, `encoder_test.go`

**Checklist**

- [ ] Uses `github.com/klauspost/reedsolomon` library (or equivalent)
- [ ] Default shards: `DefaultDataShards = 10`, `DefaultParityShards = 3` (13 total)
- [ ] `Encode(data []byte)` → returns 13 shards (10 data + 3 parity)
- [ ] `Decode(shards [][]byte)` → reconstructs original data when up to 3 shards are nil/corrupt
- [ ] `encoder_test.go` tests reconstruction with missing shards — verify at least: 0 missing, 1 missing, 3 missing (max tolerable), 4 missing (should fail)
- [ ] Shard size is equal for all data shards (padding used if data length not divisible by 10)
- [ ] `ECVolume.TotalShards()` returns `DataShards + ParityShards`

**Common LLM Pitfalls**

- Encoding without padding when `len(data) % DataShards != 0` — causes panic in Reed-Solomon library.
- `Decode` modifying the input shards slice in place — callers must not rely on original shards after decode.
- Not testing reconstruction with the LAST shard missing (index boundary bug).

---

### Package: `goblob/replication/async`

**Files:** `replicator.go`, `replicator_test.go`

**Checklist**

- [ ] `Config` fields: `SourceCluster`, `SourceFiler`, `TargetCluster`, `TargetFiler`, `PathPrefix`, `BatchSize`, `FlushInterval`
- [ ] `Start(ctx)` subscribes to `SubscribeMetadata` stream; processes events in loop; exits on `ctx.Done()` or `io.EOF`
- [ ] Event handling order for `CREATE/UPDATE`: replicate blob chunks FIRST, then replicate metadata (critical — reversed order breaks references)
- [ ] Conflict resolution in `replicateUpsert`: if `target.Attr.Mtime.After(source.Attr.Mtime)` → skip (last-write-wins)
- [ ] Conflict resolution in `replicateDelete`: if target is newer → skip delete
- [ ] `record(statusLabel, eventTsNs, err)`: updates `ReplicationLagSeconds` gauge and `ReplicatedEntriesTotal` counter
- [ ] `SnapshotStatuses()` returns a copy of statuses (not the live map)
- [ ] `replicator_test.go` uses mock `filerClient` — verify it tests conflict resolution, not just happy path

**Common LLM Pitfalls**

- Blob chunks replicated after metadata — target has metadata pointing to source volume IDs which don't exist on target yet.
- `Start` not reconnecting after gRPC stream disconnect — exits and never retries.
- Lag metric not updated on successful events, only on errors.

---

### Package: `goblob/cache`

**Files:** `cache.go`, `lru.go`, `redis.go`

**Checklist**

- [ ] `LRUCache`: uses a doubly-linked list + map; `Put` evicts LRU entry when `len(map) >= capacity`
- [ ] `LRUCache.Get` moves accessed entry to front of list (LRU order maintained)
- [ ] `LRUCache.Invalidate` removes from both list and map
- [ ] All `LRUCache` methods are protected by mutex
- [ ] `RedisCache.Get` returns `cache.ErrCacheMiss` (not a Redis-specific error) on key miss — callers only depend on `ErrCacheMiss`
- [ ] `RedisCache.Put` sets TTL (not indefinite storage) — verify `SET key value EX ttlSeconds`
- [ ] `NewLRUCache(capacity int64)`: capacity of 0 or negative should be treated as disabled (returns nil or a no-op cache)

**Common LLM Pitfalls**

- LRU eviction off-by-one: evicts when `len >= capacity` but should evict when `len > capacity`.
- `RedisCache` not handling Redis connection errors gracefully — should return `ErrCacheMiss` on connection error (degrade gracefully, not propagate Redis internals).
- Concurrent map write in `LRUCache` without lock.

---

### Package: `goblob/storage/dedup`

**Files:** `dedup.go`, `dedup_test.go`

**Checklist**

- [ ] `ComputeHash(data []byte) string` — uses SHA-256, returns lowercase hex string
- [ ] `LookupOrCreate(ctx, data)` — atomic: checks KV for existing hash → if found: increment refcount → return `(fid, true, nil)`; if not found: return `("", false, nil)` (caller stores and calls `RecordStored`)
- [ ] `RecordStored(ctx, hash, fid)` — stores `hashKey(hash) → fid` and initializes refcount to 1
- [ ] `DecrementRefCount(ctx, fid)` — decrements; returns true if refcount reaches 0 (caller should delete the volume needle)
- [ ] All KV operations use consistent key format: `__dedup__hash__:<hash>` and `__dedup__ref__:<fid>`
- [ ] `dedup_test.go` tests: hash collision returns same fid, refcount increments and decrements correctly, zero-refcount signals deletion

**Note:** Dedup is not yet wired into the filer write path (pending chunked storage). Verify the TODO comment is present in `filer_server.go:handleFileUpload`.

---

### Package: `goblob/storage/tiering`

**Files:** `scanner.go` (types.go), `scanner_test.go`

**Checklist**

- [ ] `Tier` struct: `Name`, `WarmAfterDays`, `ColdAfterDays`, `ArchiveAfterDays`, `StorageClass`
- [ ] `Scanner.Run(ctx)` runs a loop with `time.After(interval)`; calls `listFn` to iterate entries; applies `migrateFn` for warm tier, `archiveFn` for cold tier based on `entry.Attr.Mtime`
- [ ] `SetInterval`, `SetMigrator`, `SetArchiver` are optional — `Scanner` has sensible defaults
- [ ] `scanner_test.go` injects mock `listFn` and `migrateFn` to verify tiering rules fire correctly
- [ ] `Scanner.Run` exits on `ctx.Done()`

**Note:** Scanner is not yet started by any server (pending tiering policy config). Verify the TODO comment is present in `command/volume_tier_upload.go`.

---

### Package: `goblob/quota`

**Files:** `quota.go`, `quota_test.go`

**Checklist**

- [ ] `Manager` holds quota rules per identity (user or bucket) in a map
- [ ] `CheckUserQuota(ctx, identity, additionalBytes)` — returns error if `usedBytes + additionalBytes > quota`
- [ ] `CheckBucketQuota(ctx, bucket, additionalBytes)` — same for bucket-level quota
- [ ] Quota state is stored in the filer KV store (not in-memory only) — verify `KvGet`/`KvPut` calls
- [ ] Used-bytes tracking is updated on upload AND decremented on delete
- [ ] CLI commands `quota.set` and `quota.get` correctly serialize/deserialize quota config

---

### Package: `goblob/s3api/lifecycle`

**Files:** `lifecycle.go` (processor.go)

**Checklist**

- [ ] `Policy` struct: `Rules []Rule`, each with `ID`, `Status`, `Filter`, `Expiration`, `Transitions`
- [ ] `Rule.Filter`: `Prefix` string and/or `Tags` map
- [ ] `Expiration.Days` — object deleted after N days from creation
- [ ] Processor runs on a periodic ticker; scans filer entries under `/buckets/<bucket>/`; applies expiration rules
- [ ] Expiration checks `entry.Attr.Crtime` (creation time), not `Mtime`
- [ ] Status `"Enabled"` / `"Disabled"` on each rule is respected
- [ ] `command/lifecycle_process.go` starts the processor as a background goroutine

---

## Final Cross-Phase Checks

After reviewing all phases individually, do these end-to-end checks:

### 1. Data Flow: Write Path
Trace a single `PUT /bucket/key` request from S3 client to disk:
```
S3 Client → s3api/server.go (SigV4 auth) → object_handlers.go (quota check)
  → operation/assign.go (GET fid from master)
  → operation/upload.go (PUT to volume server)
    → server/volume_server.go handleWrite (store + replicate)
      → storage/store.go WriteVolumeNeedle
        → storage/volume/volume.go WriteNeedle (append to .dat, update .idx)
  → server/filer_server.go CreateEntry (metadata)
    → filer/leveldb2/leveldb2_store.go InsertEntry
    → log_buffer AppendEntry (metadata event)
```
Verify each hop: correct error propagation, no silent failures, context passed through.

### 2. Data Flow: Read Path
Trace a `GET /bucket/key`:
```
S3 Client → s3api/server.go (SigV4 auth) → object_handlers.go
  → filer_server.go FindEntry (metadata lookup)
  → volume_server.go handleRead (cache check → storage read)
    → storage/store.go ReadVolumeNeedle
      → storage/volume/volume.go ReadNeedle (idx lookup → .dat seek → read)
  → Return body to client
```

### 3. Leader Failover
- What happens when the Raft leader dies mid-assign?
- Check: follower masters receive `proxyToLeader` requests → leader dies → Raft elects new leader → followers start proxying to new leader
- Verify `handleAssign` is the only handler that MUST proxy; `handleLookup` does NOT

### 4. Volume Server Crash Recovery
- `store.ReloadExistingVolumes` on startup finds `.dat` files and re-mounts them
- `.idx` rebuilt from `.dat` if `.idx` is corrupt or missing (check if this is implemented)
- Partially written needle (crash mid-write) detected via CRC32 mismatch

### 5. Security: Authorization Chain
- Every HTTP handler checks `guard.Allowed(r, isWrite)` before accessing data
- Every gRPC handler applies `GRPCUnaryInterceptor` or checks guard inside handler
- `X-Replication: true` requests bypass JWT (but should still be IP-whitelisted)
- WebDAV: basic auth → `ApplyHardening` (rate limit, headers, audit log)

### 6. Goroutine Lifecycle
Verify every goroutine started in these files has a clean exit path:
- `server/volume_server.go` → `heartbeatLoop`
- `server/master_server.go` → `startAdminScripts`, `ProcessGrowRequest`
- `server/filer_server.go` → `loopProcessingDeletion`
- `replication/async/replicator.go` → `Start`
- `log_buffer/log_buffer.go` → flush daemon
- `wdclient/master_client.go` → `KeepConnectedToMaster`

For each, verify: exits when `ctx.Done()` fires, no channel sends after exit, no deferred operations after `ctx` is done.

---

## Verification Commands (Run After Each Phase)

```bash
# Phase 0-1
go build ./...
go vet ./...

# Phase 2
go test -race ./goblob/core/types/...
go test -race ./goblob/config/...
go test -race ./goblob/storage/...
go test -race ./goblob/storage/needle/...
go test -race ./goblob/storage/volume/...

# Phase 3
go test -race ./goblob/raft/...
go test -race ./goblob/topology/...
go test -race ./goblob/sequence/...
go test -race ./goblob/cluster/...

# Phase 4
go test -race ./goblob/filer/...
go test -race ./goblob/filer/leveldb2/...
go test -race ./goblob/wdclient/...
go test -race ./goblob/pb/...
go test -race ./goblob/replication/...
go test -race ./goblob/operation/...
go test -race ./goblob/server/...

# Phase 5
go test -race ./goblob/s3api/...
go test -race ./goblob/webdav/...

# Phase 6
go test -race ./goblob/command/...
go test -race ./goblob/shell/...

# Phase 7
go test -race ./goblob/obs/...
go test -race ./goblob/security/...

# Phase 8
go test -race ./goblob/storage/erasure_coding/...
go test -race ./goblob/storage/dedup/...
go test -race ./goblob/storage/tiering/...
go test -race ./goblob/replication/async/...
go test -race ./goblob/cache/...
go test -race ./goblob/quota/...

# Full suite
go test -race -count=1 ./goblob/...

# Fuzz (run for a few minutes)
go test -fuzz=FuzzVerifyJwt -fuzztime=2m ./goblob/security/

# Integration (requires running services)
go test -tags integration ./test/integration/...
```

---

## Reviewer Notes Template

Use this template to track findings as you review each package:

```
Package: goblob/xxx
Reviewer: [your name]
Date: [date]

PASS items:
-

FAIL items (must fix):
-

WARN items (should fix):
-

Questions / unclear:
-
```
