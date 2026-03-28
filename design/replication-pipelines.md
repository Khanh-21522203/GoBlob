# Replication Pipelines

### Purpose

Replicate data across nodes/clusters using two mechanisms: synchronous per-write replica fan-out for volume writes, and asynchronous filer metadata replication between clusters.

### Scope

**In scope:**
- Synchronous HTTP write replication (`goblob/replication/replicator.go`).
- Replica location map (`goblob/replication/replica_locations.go`).
- Async metadata replicator pipeline (`goblob/replication/async/replicator.go`).
- CLI wrappers: `blob replicator` and `blob replication.status`.

**Out of scope:**
- Volume placement and replica selection policy (topology feature).
- Transport-level retries beyond current implementation.

### Primary User Flow

1. Primary volume write succeeds locally.
2. Volume server uses known replica addresses and sends same needle bytes to peers synchronously.
3. For cross-cluster replication, operator runs `blob replicator` with source/target filer gRPC addresses.
4. Async replicator consumes metadata events from source filer and applies upsert/delete on target filer.
5. Operator checks in-process status with `blob replication.status`.

### System Flow

1. Sync path:
   - `VolumeServer.handleWrite` gathers replica addresses from `replicaLocations.Get(vid)`.
   - `HTTPReplicator.ReplicatedWrite` spawns one goroutine per replica.
   - `writeToReplica` issues `PUT http://<replica>/<vid>,<needleIdHex>` with `X-Replication:true` and optional bearer token.
2. Async path:
   - `asyncrepl.NewReplicator(Config)` validates source/target settings.
   - `Start` subscribes to source `SubscribeMetadata` stream.
   - `handleEvent` classifies create/rename/delete event forms.
   - Upsert flow fetches source entry, replicates chunk payloads to target (assign + HTTP PUT), then create/update target entry.
   - Delete flow removes target entry unless target mtime is newer than event timestamp.
3. Status tracking:
   - `record` updates `Status` map (`TargetCluster`, `LagSeconds`, `Replicated`, `LastError`, `UpdatedAt`) and Prometheus metrics.

```
Primary volume write
  -> HTTPReplicator.ReplicatedWrite
     -> PUT to each replica in parallel
     -> fail request if any replica fails

Async mode
  source SubscribeMetadata stream
    -> classify event
    -> target create/update/delete
    -> update lag/error metrics + status snapshot
```

### Data Model

- Sync request (`ReplicateRequest`):
  - `VolumeId`, `NeedleId`, `NeedleBytes`, `JWTToken`.
- Async config (`async.Config`):
  - `SourceCluster`, `SourceFiler`, `TargetCluster`, `TargetFiler`, `PathPrefix`, `BatchSize`, `FlushInterval`.
- Async status (`async.Status`):
  - `TargetCluster string`, `LagSeconds float64`, `LastEventTsNs int64`, `Replicated uint64`, `LastError string`, `UpdatedAt time.Time`.
- Replica location table (`ReplicaLocations.locations map[VolumeId][]ServerAddress`) is in-memory only.

### Interfaces and Contracts

- Internal sync API:
  - `Replicator.ReplicatedWrite(ctx, req, replicaAddrs)` returns error when any replica fails.
- CLI:
  - `blob replicator -sourceFiler <host:grpc> -targetFiler <host:grpc> -sourceCluster <name> -targetCluster <name> [-pathPrefix /]`.
  - `blob replication.status [-target <cluster>]` prints in-process status snapshot.
- Metrics:
  - `goblob_replication_lag_seconds{target_cluster}` gauge.
  - `goblob_replication_replicated_entries_total{target_cluster,status}` counter.

### Dependencies

**Internal modules:**
- `goblob/server/volume_server.go` invokes sync replicator.
- `goblob/pb/filer_pb` for async metadata stream and CRUD APIs.
- `goblob/core/types` for `FileId` parsing and address normalization.

**External services/libraries:**
- HTTP reachability of target volume nodes for sync replication.
- gRPC reachability of source/target filer for async replication.

### Failure Modes and Edge Cases

- Sync replication is strict: one failed replica response causes full write failure response, even if local write already committed.
- Async event handling continues on errors; failed event increments error metrics/status and loop proceeds to next event.
- Rename is implemented as upsert destination then delete source; partial failures can leave duplicate entries.
- Chunk replication reads chunk data from source volume via master lookup and writes to target assigned volume; any step can fail with partial progress.
- `replication.status` only reports statuses in current process memory (not globally persisted).

### Observability and Debugging

- Prometheus metrics in `goblob/replication/async/replicator.go`:
  - lag gauge and replicated entries counter with status labels (`ok`, `error`, `skipped_stale`).
- Logs via `obs.New("replication")` for replication failures.
- Debug entry points:
  - `replicateUpsert`/`replicateDelete` for divergence behavior.
  - `writeToReplica` for sync replication HTTP status failures.

### Risks and Notes

- Sync path has no retry/backoff; transient replica network errors fail foreground writes.
- Async path keeps status in-memory; process restart clears historical replication counters/status.
- BatchSize/FlushInterval config fields exist but event loop currently processes stream events one-by-one.

Changes:

