# Topology and Replication

## Assumptions

- The topology tree is: Topology > DataCenter > Rack > DataNode > Disk.
- Replica placement uses a 3-digit notation (e.g., `001`, `010`, `100`) encoding data center, rack, and server copy counts.
- Volume growth is driven by the master; configurable strategy per replication level.

## Code Files / Modules Referenced

- `goblob/topology/topology.go` - `Topology` struct, `NewTopology()`
- `goblob/topology/node.go` - `Node` interface, `NodeImpl`, `CapacityReservations`
- `goblob/topology/data_center.go` - `DataCenter` struct
- `goblob/topology/rack.go` - `Rack` struct
- `goblob/topology/data_node.go` - `DataNode` struct (represents a volume server)
- `goblob/topology/disk.go` - `Disk` struct (represents a disk type on a data node)
- `goblob/topology/collection.go` - `Collection` struct
- `goblob/topology/volume_layout.go` - `VolumeLayout` (tracks writable/readonly volumes)
- `goblob/topology/volume_location_list.go` - `VolumeLocationList`
- `goblob/topology/volume_growth.go` - `VolumeGrowth`, `VolumeGrowOption`, `VolumeGrowRequest`
- `goblob/topology/allocate_volume.go` - Volume allocation to data nodes
- `goblob/topology/store_replicate.go` - Replication write logic
- `goblob/topology/topology_vacuum.go` - Distributed vacuum coordination
- `goblob/topology/topology_ec.go` - EC shard location tracking
- `goblob/topology/topology_event_handling.go` - Volume join/leave events
- `goblob/topology/topology_info.go` - Topology information reporting
- `goblob/storage/super_block/replica_placement.go` - `ReplicaPlacement` encoding

## Overview

The topology module models the physical cluster layout and manages volume placement, replication, and growth. The master server uses this module to decide where to create new volumes, which volumes are writable, and how to satisfy replication requirements. The topology is dynamically updated as volume servers join and leave the cluster via heartbeats.

## Responsibilities

- **Cluster modeling**: Hierarchical tree of DataCenter > Rack > DataNode > Disk
- **Volume layout**: Track which volumes are writable, full, or crowded per collection
- **Volume growth**: Create new volumes when existing ones reach capacity threshold
- **Replica placement**: Ensure volumes are replicated according to placement policy
- **Capacity reservation**: Prevent race conditions during concurrent volume allocations
- **Vacuum coordination**: Orchestrate compaction across volume servers
- **EC shard tracking**: Map EC shard locations across the topology
- **Volume assignment**: Pick the best writable volume for new file uploads

## Architecture Role

```
+------------------------------------------------------------------+
|                       Topology Tree                               |
+------------------------------------------------------------------+
|                                                                   |
|                        Topology                                   |
|                        (root)                                     |
|                     /          \                                  |
|              DataCenter-1    DataCenter-2                         |
|              /        \        /       \                          |
|         Rack-A     Rack-B   Rack-C   Rack-D                      |
|         /    \      /         |        \                          |
|     Node-1  Node-2 Node-3  Node-4   Node-5                       |
|     /    \    |      |       |        |                           |
|   Disk   Disk Disk  Disk   Disk     Disk                         |
|   (ssd)  (hdd)(hdd) (ssd)  (hdd)   (ssd)                        |
|   [v1,v2][v3] [v4]  [v5]   [v6,v7] [v8]                         |
|                                                                   |
+------------------------------------------------------------------+

Each node at every level tracks:
  - DiskUsages (per disk type: volume count, active, max, EC shard count)
  - Children (sub-nodes)
  - CapacityReservations (temporary allocation locks)
```

## Component Structure Diagram

```
+---------------------------------------------------------------+
|                        Topology                                |
+---------------------------------------------------------------+
| NodeImpl (embedded)                                            |
|   id, nodeType, children map[NodeId]Node                       |
|   diskUsages *DiskUsages                                       |
|   capacityReservations *CapacityReservations                   |
|                                                                |
| collectionMap   *ConcurrentReadMap    # Collection -> VolumeLayout|
| Sequence        sequence.Sequencer    # file ID generator      |
| RaftServer      raft.Server           # consensus              |
| HashicorpRaft   *hashicorpRaft.Raft   # alt consensus          |
| volumeSizeLimit uint64                                         |
| replicationAsMin bool                 # treat replication as min|
| chanFullVolumes    chan VolumeInfo     # signal full volumes    |
| chanCrowdedVolumes chan VolumeInfo     # signal crowded volumes |
| Configuration   *Configuration        # topology config        |
| UuidMap         map[string][]string   # directory UUID mapping |
+---------------------------------------------------------------+
         |
         v
+---------------------------------------------------------------+
|                    Node Interface                              |
+---------------------------------------------------------------+
| Id() NodeId                                                    |
| String() string                                                |
| Children() map[NodeId]Node                                     |
| Parent() Node                                                  |
| IsLocked() bool                                                |
| GetDiskUsages() *DiskUsages                                    |
| PickNodesByWeight(count, fn) []*DataNode                       |
| ReserveOneVolume(option) (*DataNode, error)                    |
+---------------------------------------------------------------+
         |
    +----+----+----+----+
    |    |    |    |    |
    DC  Rack  DN  Disk  (all implement Node)

+---------------------------------------------------------------+
|                      VolumeLayout                              |
+---------------------------------------------------------------+
| rp               *ReplicaPlacement                             |
| ttl              *needle.TTL                                   |
| diskType         types.DiskType                                |
| volumeSizeLimit  uint64                                        |
| replicationAsMin bool                                          |
| vid2location     map[VolumeId]*VolumeLocationList              |
| writables        []VolumeId           # volumes accepting writes|
| readonlyVolumes  map[VolumeId]bool                             |
| oversizedVolumes map[VolumeId]bool                             |
| crowded          map[VolumeId]struct{}                          |
+---------------------------------------------------------------+

+---------------------------------------------------------------+
|                   VolumeGrowth                                 |
+---------------------------------------------------------------+
| accessLock  sync.Mutex                                         |
+---------------------------------------------------------------+
| AutomaticGrowByType(option, grpcDialOption, topo)              |
|   +--> findEmptySlots()                                        |
|   +--> grow(topo, option, ...)                                 |
|        +--> ReserveOneVolume() per required server              |
|        +--> AllocateVolume() via gRPC                          |
|        +--> Release reservations                               |
+---------------------------------------------------------------+
```

## Control Flow

### Volume Assignment for Write

```
Topology.PickForWrite(count, option)
    |
    +--> Find or create VolumeLayout for (collection, replication, ttl, diskType)
    |
    +--> VolumeLayout.PickForWrite(count, option)
    |       |
    |       +--> Filter writables by DataCenter/Rack/DataNode preference
    |       +--> Random selection from candidates
    |       +--> Return (VolumeId, VolumeLocationList, error)
    |
    +--> If no writable volume found:
    |       Send VolumeGrowRequest to volumeGrowthRequestChan
    |       Wait for growth to complete
    |       Retry PickForWrite
    |
    +--> Return (fileId, count, volumeServerAddress)
```

### Volume Growth Strategy

```
VolumeGrowStrategy (defaults):
  Copy1Count:     7    # create 7 volumes for replication "000"
  Copy2Count:     6    # create 6 volumes for replication "001"
  Copy3Count:     3    # create 3 volumes for replication "010"
  CopyOtherCount: 1    # create 1 volume for other replication types
  Threshold:      0.9  # grow when 90% of writable volumes are full

Growth flow:
  1. Master detects writable volume count below threshold
  2. VolumeGrowRequest sent to channel
  3. ProcessGrowRequest() goroutine picks up request
  4. VolumeGrowth.AutomaticGrowByType():
     a. Determine count based on replication level
     b. Find data nodes with available capacity
     c. Reserve capacity on selected nodes (prevents race)
     d. Allocate volumes via gRPC to volume servers
     e. Release reservations (success or failure)
  5. New volumes appear in heartbeat, added to VolumeLayout.writables
```

### Capacity Reservation System

```
ReserveOneVolume(randomFn, option)
    |
    +--> Walk topology tree to find candidate DataNode
    |    (filtered by DataCenter, Rack, DiskType)
    |
    +--> Node.ReserveOneVolume(option):
    |    +--> CapacityReservations.addReservation(diskType, 1)
    |    |    (generates unique reservationId)
    |    |    (increments reservedCounts[diskType])
    |    |
    |    +--> Check: FreeSpace() > reservedCount
    |    |    If not enough: removeReservation() and try next node
    |    |
    |    +--> Return (DataNode, reservationId)
    |
    +--> After AllocateVolume succeeds or fails:
         CapacityReservations.removeReservation(reservationId)
```

## Runtime Sequence Flow

### Heartbeat Processing

```
Master gRPC                   Topology
  |                              |
  |-- ProcessJoinMessage() ----->|
  |   (from volume server        |
  |    heartbeat)                 |
  |                              |
  |                              +--> GetOrCreateDataCenter(dc)
  |                              +--> GetOrCreateRack(rack)
  |                              +--> GetOrCreateDataNode(ip:port)
  |                              |
  |                              +--> For each volume in heartbeat:
  |                              |    +--> Register in VolumeLayout
  |                              |    +--> Update VolumeLocationList
  |                              |    +--> Check if full -> chanFullVolumes
  |                              |    +--> Check if crowded -> chanCrowdedVolumes
  |                              |
  |                              +--> For each deleted volume:
  |                              |    +--> Remove from VolumeLayout
  |                              |
  |<-- topology updated ---------|
```

## Data Flow Diagram

```
Replica Placement "001" (one extra copy in same data center):

  Write Request
       |
       v
  +----------+          +----------+
  | Volume 5 |  --copy-->| Volume 5 |
  | on VS-A  |          | on VS-B  |
  | (primary)|          | (replica)|
  +----------+          +----------+
  Rack-1                 Rack-1
  DataCenter-1           DataCenter-1

Replica Placement "010" (one extra copy in different rack):

  Write Request
       |
       v
  +----------+          +----------+
  | Volume 5 |  --copy-->| Volume 5 |
  | on VS-A  |          | on VS-C  |
  | Rack-1   |          | Rack-2   |
  +----------+          +----------+
  DataCenter-1           DataCenter-1

Replica Placement "100" (one extra copy in different data center):

  Write Request
       |
       v
  +----------+          +----------+
  | Volume 5 |  --copy-->| Volume 5 |
  | on VS-A  |          | on VS-D  |
  | DC-1     |          | DC-2     |
  +----------+          +----------+
```

## Dependencies

| Dependency | Purpose |
|---|---|
| `goblob/storage` | VolumeInfo, NeedleMapKind |
| `goblob/storage/needle` | VolumeId, TTL |
| `goblob/storage/super_block` | ReplicaPlacement |
| `goblob/storage/types` | DiskType |
| `goblob/sequence` | Sequencer interface |
| `goblob/pb/master_pb` | Heartbeat messages, volume info |
| `goblob/stats` | Prometheus metrics |
| `goblob/util` | ConcurrentReadMap |

## Error Handling

- **No writable volume**: Returns error; triggers volume growth request
- **Insufficient nodes**: Cannot satisfy replication if not enough nodes in required topology levels
- **Reservation timeout**: Stale reservations are cleaned up; no explicit TTL but cleared on growth completion
- **Topology lock**: `IsChildLocked()` checks prevent operations on locked nodes during maintenance
- **Volume growth failure**: Reservations released; error logged; retry on next request

## Configuration

- **Replication types**: `000` (no copy), `001` (1 same-rack copy), `010` (1 different-rack), `100` (1 different-DC), `200` (2 different-DC), etc.
- **Growth strategy**: Configurable via `master.toml` (`master.volume_growth.copy_1`, etc.)
- **Threshold**: Default 0.9 (grow when 90% of writable volumes are full)
- **`replication_as_minimums`**: Treat replication count as minimum instead of exact
- **Vacuum**: `-garbageThreshold` (default 0.3) triggers compaction when 30% of volume is garbage

## Edge Cases

- **Topology auto-discovery**: DataCenter, Rack, DataNode created automatically from heartbeats
- **Empty cluster**: First volume growth creates the initial set of volumes
- **Uneven distribution**: `volume.balance` shell command redistributes volumes
- **Node failure**: Volumes on failed node become unavailable; replicas on other nodes continue serving
- **Crowded volumes**: Volumes approaching capacity are marked crowded; new writes prefer non-crowded volumes
- **Collection isolation**: Each collection has independent VolumeLayouts per (replication, TTL, diskType)
