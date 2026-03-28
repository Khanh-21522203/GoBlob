# Master Control Plane

### Purpose

Coordinate cluster topology, file ID assignment, writable volume selection, and cluster leadership-sensitive operations for GoBlob clients and volume/filer nodes.

### Scope

**In scope:**
- Master server boot and route registration in `goblob/server/master_server.go`.
- HTTP APIs in `goblob/server/master_server_handlers.go`.
- gRPC `MasterService` APIs in `goblob/server/master_grpc_server.go`.
- Leader/follower behavior and follower-to-leader proxying.
- Security config reload for master guard.

**Out of scope:**
- Volume server storage behavior and heartbeats payload creation.
- Topology internals and volume growth selection algorithms (documented separately).
- Raft storage implementation details (documented in Raft feature).

### Primary User Flow

1. Client requests file assignment (`POST /dir/assign` or gRPC `Assign`).
2. Master validates leadership and selects a writable volume layout.
3. Master allocates needle IDs via sequencer and returns `fid`, volume URL, and optional JWT.
4. Volume servers continuously stream `SendHeartbeat` gRPC updates to refresh topology.
5. Admin tools query topology (`/dir/status`, `VolumeList`) and manage peers (`/cluster/peer`).

### System Flow

1. Entry point: `goblob/server/master_server.go:NewMasterServerWithGRPC` uses a 3-phase construction to avoid circular dependencies:
   - Phase 1: `MasterFSM` with no callbacks.
   - Phase 2: `NewRaftServer` + `NewRaftSequencer` (both only need the FSM/applier interface, not the full master).
   - Phase 3: Assemble `MasterServer`, prime state from `fsm.CommittedState()`, wire event loops (`handleFSMEvents`, `handleLeaderEvents`).
2. Route registration: `registerRoutes` attaches HTTP handlers for assign/lookup/status/grow/vacuum/health/ready/peer APIs.
3. Assign path:
   - `handleAssign` parses query (`collection`, `replication`, `ttl`, `count`, `dataCenter`, `rack`, `diskType`).
   - `Topo.GetOrCreateVolumeLayout(...).PickForWrite(...)` returns candidate volume ID + locations.
   - `Sequencer.NextFileId(count)` allocates needle ID range.
   - `security.SignJWT(...)` signs short-lived token when guard signing key is present.
4. Follower behavior: non-leader requests are proxied by `proxyToLeader` using `httputil.NewSingleHostReverseProxy` and leader address translation.
5. gRPC path:
   - `SendHeartbeat` updates topology via `Topo.ProcessJoinMessage` and replies with leader + volume size limit.
   - `VolumeList` materializes `TopologyInfo` hierarchy (data center/rack/node/disk/volume).
6. Background loops:
   - `processVolumeGrowRequests` dequeues grow requests and calls `VolumeGrowth.CheckAndGrow`.
   - `raftStatsWorker` exports Raft stats to Prometheus gauges.

```
Assign request
  -> handleAssign
     -> leader check
        -> [follower] proxyToLeader
        -> [leader] PickForWrite + NextFileId + SignJWT
           -> JSON AssignResponse

Volume server heartbeat stream
  -> MasterGRPCServer.SendHeartbeat
     -> Topology.ProcessJoinMessage
     -> return leader + volume size limit
```

### Data Model

- `MasterServer` (`goblob/server/master_server.go`) fields:
  - `Topo *topology.Topology`
  - `Vg *topology.VolumeGrowth`
  - `Sequencer sequence.Sequencer`
  - `Raft raft.RaftServer`
  - `Cluster *cluster.ClusterRegistry`
  - `Guard *security.Guard`
- HTTP DTOs (`goblob/server/master_server_handlers.go`):
  - `AssignResponse` fields: `fid (string)`, `url (string)`, `publicUrl (string)`, `count (uint64)`, `auth (string)`, `error (string)`.
  - `LookupResponse` fields: `volumeId`, `locations[]` with `url/publicUrl`.
- Persisted state inputs:
  - Max file ID / max volume ID / topology ID are persisted through Raft commands and FSM snapshots, not local-only memory.

### Interfaces and Contracts

- HTTP endpoints:
  - `POST /dir/assign` query params: `collection`, `replication`, `ttl`, `count`, `dataCenter`, `rack`, `diskType`.
    - `200`: `AssignResponse`.
    - `503`: `{error:"no writable volumes available"}` when no writable volume.
  - `GET /dir/lookup?volumeId=<id>`:
    - `200`: locations array.
    - `400`: missing/invalid volumeId.
  - `GET /dir/status`: leader flag, topology, raft stats.
  - `POST /vol/grow`: enqueue grow requests (`count`, `collection`, `replication`, `ttl`).
  - `POST /vol/vacuum`: placeholder `{"status":"ok"}`.
  - `POST /cluster/peer` / `DELETE /cluster/peer` body `{ "addr": "host:port" }`.
  - `GET /cluster/healthz`, `GET /health`, `GET /ready`.
- gRPC (`master_pb.MasterService`):
  - Streaming: `SendHeartbeat`, `KeepConnected`.
  - Unary: `Assign`, `LookupVolume`, `GetMasterConfiguration`, `VolumeList`.
- Leader contract:
  - Writes and peer changes require leader; followers proxy HTTP or return `FailedPrecondition` in gRPC assign.

### Dependencies

**Internal modules:**
- `goblob/topology` for layout and location resolution.
- `goblob/sequence` for monotonic file ID allocation.
- `goblob/raft` for leadership and replicated state.
- `goblob/security` for JWT signing and whitelist enforcement.
- `goblob/cluster` for non-volume KeepConnected registry.

**External services/libraries:**
- HashiCorp Raft via `goblob/raft` abstraction.
- gRPC transport for master-volume/filer streams.
- If Raft transport or storage is unavailable, leader-required APIs fail.

### Failure Modes and Edge Cases

- No leader available: follower proxy returns `503` with `no leader available`.
- Invalid `volumeId` lookup input returns `400`.
- No writable volume: assign returns `503`, and master attempts async grow by queueing `VolumeGrowOption`.
- Growth queue full: `/vol/grow` returns `503` `volume growth channel full`.
- `KeepConnected` stream cleanup: on stream close, `Cluster.Unregister(clientAddr)` is invoked.
- JWT signing key missing: assign still succeeds but `auth` is empty.

### Observability and Debugging

- Metrics (`goblob/obs/metrics.go` + master server usage):
  - `goblob_master_assign_requests_total`
  - `goblob_master_is_leader`
  - `goblob_master_volume_count`
  - Raft gauges (`commit_index`, `last_log_index`, `num_peers`, `last_contact_seconds`).
- Logs:
  - `ms.logger.Warn("volume growth failed"...)` and leader transition logs in `handleLeaderEvents`.
- Debug entry points:
  - `GET /dir/status` for runtime topology/raft state.
  - `MasterGRPCServer.VolumeList` for full hierarchy returned to clients.

### Risks and Notes

- `handleVacuum` is currently a stub returning success, so callers may assume vacuum happened when it did not.
- `proxyToLeader` rewrites based on port conventions (`grpcPort = httpPort + 10000`), which can break in non-standard deployments.
- `PickForWrite` currently selects first matching writable volume; no per-request load balancing strategy is applied here.

Changes:

