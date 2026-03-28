# Volume Data Plane

### Purpose

Store blob payloads on volume files and expose read/write/delete/maintenance APIs over HTTP and gRPC for clients, filers, and maintenance commands.

### Scope

**In scope:**
- Volume server construction and route wiring in `goblob/server/volume_server.go`.
- HTTP write/read/delete/status/health handlers.
- gRPC volume management methods in `goblob/server/volume_grpc_server.go`.
- Master heartbeat loop and replica write fan-out.

**Out of scope:**
- On-disk needle encoding internals (`goblob/storage/needle/*`, documented separately).
- Topology placement decisions and grow logic.

### Primary User Flow

1. Client obtains `fid` from master and sends `PUT /{fid}` to a volume server.
2. Volume server validates auth, parses payload, writes needle to local volume store, and optionally replicates to peer replicas.
3. Reader fetches `GET /{fid}`; server serves from cache or reads from volume store.
4. Delete operation sends `DELETE /{fid}`; server writes tombstone and invalidates cache.
5. Master and maintenance tools call gRPC methods for allocate/copy/compact/status operations.

### System Flow

1. Entry point: `goblob/server/volume_server.go:NewVolumeServer` builds `storage.Store`, guard, replicator, cache, and route handlers.
2. Write path (`handleWrite`):
   - Enforce upload throttle (`ConcurrentUploadLimitMB`).
   - `guard.Allowed(r,true)` for whitelist + JWT check.
   - Parse `fid` via `types.ParseFileId`.
   - Build needle from multipart/raw payload (`buildNeedleFromRequest`).
   - `store.WriteVolumeNeedle(fid.VolumeId, needle)` persists data.
   - If request is not replication (`X-Replication != true`), replicate serialized needle to peer addresses via `replicator.ReplicatedWrite`.
3. Read path (`handleRead`):
   - Enforce download throttle.
   - Authorization check.
   - Cache lookup by path key; fallback to `store.ReadVolumeNeedle`.
   - Emit payload with `Content-Type`, `ETag`, `Content-Length`.
4. gRPC path (`VolumeGRPCServer`):
   - `AllocateVolume`, `VolumeDelete`, `VolumeCompact`, `VolumeMarkReadonly`, `VolumeStatus`, `VolumeCopy`, `ReadAllNeedles`.
5. Heartbeat path:
   - `heartbeatLoop` ticks and calls `sendHeartbeat` to master `SendHeartbeat` stream.
   - Updates `currentMaster` when leader changes.

```
PUT /{fid}
  -> auth + fid parse + payload parse
  -> store.WriteVolumeNeedle
  -> [if primary write] replicate to peers
  -> 201 {size,eTag}

GET /{fid}
  -> auth + fid parse
  -> cache hit OR store.ReadVolumeNeedle
  -> 200 blob bytes
```

### Data Model

- `VolumeServer` (`goblob/server/volume_server.go`) key state:
  - `store storage.VolumeStore`
  - `replicator replication.Replicator`
  - `replicaLocations *replication.ReplicaLocations`
  - in-flight counters: `inFlightUpload`, `inFlightDownload`.
- `VolumeServerOption` (`goblob/server/volume_option.go`) fields include:
  - network: `Host`, `Port`, `GRPCPort`, `PublicUrl`, `Masters[]`
  - storage: `Directories[]DiskDirectoryConfig`, `IndexType`, `FileSizeLimitMB`
  - throttles: `ConcurrentUploadLimitMB`, `ConcurrentDownloadLimitMB`
  - runtime: `HeartbeatInterval`, `ReadMode`, `RatePerSecond`.
- Needle data entity written through store: includes `NeedleId`, `Cookie`, `Data`, optional metadata and checksum (see needle storage feature).

### Interfaces and Contracts

- HTTP endpoints (`adminMux`):
  - `PUT /{fid}`: accepts multipart or raw body; returns `201` with JSON `{size,eTag}`.
  - `GET /{fid}`: returns blob bytes or `404`.
  - `DELETE /{fid}`: returns `202 {"status":"deleted"}`.
  - `GET /status`: `{"version":"1.0","volumes":<count>,"limit":<maxMB>}`.
  - `GET /health`, `GET /ready`.
- gRPC (`volume_server_pb.VolumeServer`):
  - `AllocateVolume`, `VolumeDelete`, `VolumeCopy(stream)`, `VolumeCompact`, `VolumeMarkReadonly`, `VolumeStatus`, `VolumeCommitCompact`, `ReadAllNeedles(stream)`.
- Auth contract:
  - write/delete require JWT when signing key configured.
  - read requires JWT only when `guard.Allowed(..., false)` rejects by whitelist/key policy.

### Dependencies

**Internal modules:**
- `goblob/storage` for volume file persistence.
- `goblob/replication` for synchronous replica fan-out.
- `goblob/cache` for in-memory blob cache.
- `goblob/pb/master_pb` for heartbeat stream.

**External services/libraries:**
- gRPC to master and peer volume servers.
- If master gRPC unavailable, heartbeats fail and master location can become stale.

### Failure Modes and Edge Cases

- Invalid `fid` yields `400` for write/delete/read.
- Unauthorized requests yield `401`.
- Payload exceeding max size returns `413` (`errPayloadTooLarge`).
- Replication failure after local write causes `500`; data may exist locally while peer replicas are missing.
- Missing volume ID in gRPC methods returns logical errors (`volume not found`) or gRPC `Unavailable` if store not ready.
- `VolumeCopy` cleans up partially-copied target volume on failure via deferred delete.

### Observability and Debugging

- Metrics:
  - `goblob_volume_needle_write_bytes_total{volume}`
  - `goblob_volume_needle_read_bytes_total{volume}`
  - `goblob_volume_needle_write_latency_seconds`
  - `goblob_volume_needle_read_latency_seconds`
  - `goblob_volume_disk_free_bytes{directory}`.
- Runtime status:
  - `GET /status` for volume counts and file size limit.
  - gRPC `VolumeStatus` for per-volume readonly and size.
- Debug entry points:
  - `handleWrite` for ingestion/replication failures.
  - `sendHeartbeat` for leader routing issues.

### Risks and Notes

- `publicMux` is created but runtime currently serves `adminMux` in `buildVolumeRuntime`; public-route split is not active in this process wiring.
- Download throttle counts requests (`inFlightDownload`) rather than bytes, so limit semantics differ from upload byte-based control.
- Replica write path is all-or-fail; no retry queue or async compensation exists.

Changes:

