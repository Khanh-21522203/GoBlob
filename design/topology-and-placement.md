# Topology and Placement

### Purpose

Maintain an in-memory cluster topology graph and use it to select writable volumes and allocate new volumes according to replica placement constraints.

### Scope

**In scope:**
- Topology tree and heartbeat reconciliation in `goblob/topology/topology.go`.
- Data node/rack/datacenter state models (`goblob/topology/data_node.go`, related node files).
- Volume layout grouping and write selection in `goblob/topology/volume_layout.go`.
- Auto-grow allocation workflow in `goblob/topology/volume_growth.go`.

**Out of scope:**
- Master HTTP/gRPC endpoint behavior that calls into topology.
- Physical volume file creation internals on volume servers.

### Primary User Flow

1. Volume servers send heartbeat messages to master.
2. Topology updates data centers, racks, nodes, and live volume memberships.
3. Upload assign requests query volume layouts and select writable volume IDs.
4. If writable capacity drops below threshold, master schedules/executes volume grow.
5. Volume growth allocates new volume IDs and asks selected nodes to create volumes by gRPC.

### System Flow

1. Heartbeat ingest (`Topology.ProcessJoinMessage`):
   - Finds/creates node path `DataCenter -> Rack -> DataNode`.
   - Updates node identity and volume list from heartbeat.
   - Reconciles stale/deleted volumes.
2. Layout organization:
   - `VolumeLayoutCollection` key = `collection:replication:ttl:diskType`.
   - Each `VolumeLayout` maps `VolumeId` to list of replica `VolumeLocation`s and tracks writable/readonly/crowded sets.
3. Write selection:
   - `VolumeLayout.PickForWrite` chooses first writable volume with optional datacenter/rack affinity filter.
4. Grow path (`VolumeGrowth.CheckAndGrow`):
   - triggers when writable ratio `< 0.9`.
   - finds candidate data nodes with free slots.
   - reserves slots, allocates next `volumeId`, calls target `VolumeServer.AllocateVolume` gRPC, updates topology.
5. Rollback behavior:
   - failed allocate on one node triggers deallocate attempts on previously allocated nodes and reservation release.

```
Heartbeat -> ProcessJoinMessage
          -> update DataNode + layouts

Assign write -> PickForWrite
             -> [no writable volume]
                -> enqueue CheckAndGrow
                   -> select nodes + AllocateVolume RPC
```

### Data Model

- `Topology` fields:
  - root `NodeImpl`, `volumeLayouts *VolumeLayoutCollection`, `nextVolumeId uint32`.
- `DataNode` fields include:
  - network identity (`url`, `publicUrl`, `ip`, `port`, `grpcPort`),
  - `disks map[DiskType]*DiskInfo`,
  - `volumes map[uint32]*VolumeInformationMessage`,
  - `reservations map[DiskType]int`.
- `VolumeLayout` partitions:
  - `writableVolumes`, `readonlyVolumes`, `crowdedVolumes` keyed by volume ID.
- Grow config input (`VolumeGrowOption`):
  - `Collection`, `ReplicaPlacement`, `Ttl`, `DiskType`, optional `DataCenter`, `Rack`.

### Interfaces and Contracts

- External input contract: `master_pb.Heartbeat` fields drive topology updates (`Volumes`, `DeletedVids`, `NewVids`, `MaxVolumeCounts`, node identity fields).
- Write selection contract: `PickForWrite` returns `(volumeId, locations, error)`; returns error when no writable volume.
- Growth contract:
  - `CheckAndGrow(..., count)` returns number of created volumes and error.
  - requires candidate count >= replication total copies.

### Dependencies

**Internal modules:**
- `goblob/core/types` for replica placement/disk types.
- `goblob/pb/master_pb` for heartbeat and topology response models.
- `goblob/pb/volume_server_pb` for allocate/delete volume RPC calls during grow.

**External services/libraries:**
- Depends on volume server gRPC availability for actual allocation/deallocation.
- Master may update in-memory topology even if RPC side effects partially fail.

### Failure Modes and Edge Cases

- Nil heartbeat returns error and topology is not updated.
- Candidate shortage for placement returns `not enough candidates`.
- Reservation failures abort growth cycle and release prior reservations.
- `PickForWrite` currently short-circuits on first suitable writable volume, which can create hot-spot behavior.
- Dead node cleanup requires explicit `SyncDeadNodeRemoval` call; stale nodes may linger until invoked.

### Observability and Debugging

- Topology can be inspected through master status and `VolumeList` responses.
- Debug entry points:
  - `ProcessJoinMessage` for heartbeat reconciliation.
  - `VolumeLayout.PickForWrite` for write-target decisions.
  - `VolumeGrowth.CheckAndGrow` for allocation failures and rollback behavior.
- No dedicated topology-specific Prometheus metrics in this package.

### Risks and Notes

- In-memory topology is authoritative for routing decisions at runtime; inconsistency with real node state can cause stale assign targets.
- Growth trigger uses global writable ratio heuristic and may not reflect per-collection hotspot conditions.
- Selection in `selectNodesForReplica` is deterministic over candidate order and may under-distribute load if candidate ordering is stable.

Changes:

