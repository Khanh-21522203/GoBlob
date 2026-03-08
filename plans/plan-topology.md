# Feature: Topology Manager

## 1. Purpose

The Topology Manager maintains the live cluster layout within the Master Server. It models the physical hierarchy of DataCenter → Rack → DataNode → Disk, tracks which volumes reside on which nodes, decides where new volumes should be placed, and serves as the primary source for volume location lookups.

Every volume server heartbeat updates the topology. Every file assignment query reads from it. The topology is the authoritative in-memory state of what the cluster looks like right now.

## 2. Responsibilities

- Model the cluster as a hierarchy: Topology → DataCenter → Rack → DataNode → Disk
- Track all volumes per DataNode, grouped by Collection × ReplicaPlacement × TTL × DiskType (`VolumeLayout`)
- Maintain writable volume sets per `VolumeLayout`; detect when volumes become full or crowded
- Process heartbeat join messages from volume servers: register/update nodes and volume lists
- Answer volume location lookups: given a VolumeId, return all server addresses holding it
- Pick a writable volume for a new file upload (`PickForWrite`)
- Support capacity reservation to prevent races during concurrent volume allocation
- Signal the master when volume growth is needed (via channels)
- Coordinate vacuum (compaction) across volume servers

## 3. Non-Responsibilities

- Does not persist topology state to disk (it is rebuilt from heartbeats on restart)
- Does not directly allocate volumes (delegated to Volume Growth subsystem)
- Does not perform the actual replication write (delegated to Replication Engine)
- Does not run the Raft consensus (delegated to Raft layer)

## 4. Architecture Design

```
+-----------------------------------------------------------+
|                        Topology                            |
|                                                            |
|    collectionMap:                                         |
|    "photos" --> VolumeLayout("000", "", "")               |
|                   writables: [vol5, vol7, vol11]          |
|                   vid2location: {5: [VS-A, VS-B], ...}    |
|                                                            |
|    NodeImpl (embedded):                                    |
|    children["DC1"] --> DataCenter                         |
|                        children["Rack-A"] --> Rack        |
|                                children["10.0.0.1:8080"]  |
|                                --> DataNode               |
|                                   children["hdd"] --> Disk|
|                                   volumes: [vol1, vol5]   |
|    chanFullVolumes    chan VolumeInfo                      |
|    chanCrowdedVolumes chan VolumeInfo                      |
+-----------------------------------------------------------+
```

### VolumeLayout responsibilities
Each `VolumeLayout` tracks volumes for a specific (collection, replication, ttl, diskType) combination:
- `writables []VolumeId`: volumes currently accepting writes
- `readonlyVolumes map[VolumeId]bool`: volumes that are full
- `crowded map[VolumeId]struct{}`: volumes that are near-full (>80% default)
- `vid2location map[VolumeId]*VolumeLocationList`: all servers holding each volume

### Capacity Reservation System
To prevent two concurrent volume growth operations from picking the same DataNode, each node maintains a `CapacityReservations` counter. Before calling `AllocateVolume`, the master reserves capacity on the selected nodes; on completion (success or failure), reservations are released.

## 5. Core Data Structures (Go)

```go
package topology

import (
    "sync"
    "goblob/core/types"
    "goblob/storage/needle"
)

// NodeId is the string identifier for a node at any level of the hierarchy.
type NodeId string

// Node is the common interface for every level of the topology tree.
type Node interface {
    Id() NodeId
    String() string
    Children() map[NodeId]Node
    Parent() Node
    SetParent(Node)
    AddChild(Node)
    RemoveChild(NodeId)
    // Disk usage
    GetDiskUsages() *DiskUsages
    UpAdjustDiskUsageDelta(diskType types.DiskType, delta DiskUsageDelta)
    DownAdjustDiskUsageDelta(diskType types.DiskType, delta DiskUsageDelta)
    // Capacity reservation
    ReserveOneVolume(option *VolumeGrowOption) (*DataNode, ReservationId, error)
    ReleaseReservation(id ReservationId)
    FreeSpace(diskType types.DiskType) int // max - active - reserved
}

// NodeImpl contains the shared fields for all topology nodes.
type NodeImpl struct {
    id       NodeId
    parent   Node
    children map[NodeId]Node
    childrenLock sync.RWMutex

    diskUsages  *DiskUsages
    reservations *CapacityReservations
}

// DiskUsages tracks capacity metrics for a single disk type at a node level.
type DiskUsages struct {
    mu    sync.RWMutex
    byDisk map[types.DiskType]*DiskUsage
}

type DiskUsage struct {
    VolumeCount       int32  // total volumes hosted
    ActiveVolumeCount int32  // writable volumes
    MaxVolumeCount    int32  // maximum volumes this node can host
}

type DiskUsageDelta struct {
    VolumeCount       int32
    ActiveVolumeCount int32
    MaxVolumeCount    int32
}

// ReservationId is an opaque token for a capacity reservation.
type ReservationId uint64

// CapacityReservations tracks temporary volume slots reserved during allocation.
type CapacityReservations struct {
    mu       sync.Mutex
    byDisk   map[types.DiskType]int32     // total reserved count per disk type
    byId     map[ReservationId]reservationEntry
    nextId   ReservationId
}

type reservationEntry struct {
    diskType types.DiskType
}

// Topology is the root of the cluster topology tree.
type Topology struct {
    NodeImpl // embedded; children are DataCenters

    collectionMap *ConcurrentReadMap // string -> *Collection (Collection -> VolumeLayouts)

    Sequence sequence.Sequencer

    volumeSizeLimit  uint64
    replicationAsMin bool

    chanFullVolumes    chan VolumeInfo
    chanCrowdedVolumes chan VolumeInfo

    logger *slog.Logger
}

// DataCenter is the second level of the topology tree.
type DataCenter struct {
    NodeImpl
}

// Rack is the third level of the topology tree.
type Rack struct {
    NodeImpl
}

// DataNode represents a single volume server in the topology.
type DataNode struct {
    NodeImpl
    Ip        string
    Port      int
    GrpcPort  int
    PublicUrl string
    DataCenter string
    Rack       string
    // disks maps disk type to its Disk node.
    disks     map[types.DiskType]*Disk
    disksLock sync.RWMutex
    lastSeen  time.Time
}

// Disk represents a disk type on a DataNode (e.g., hdd or ssd).
type Disk struct {
    NodeImpl
    diskType    types.DiskType
    volumes     map[types.VolumeId]*VolumeInfo
    volumesLock sync.RWMutex
}

// VolumeInfo is the master's view of a single volume replica.
type VolumeInfo struct {
    Id          types.VolumeId
    Collection  string
    ReplicaPlacement types.ReplicaPlacement
    Ttl         types.TTL
    DiskType    types.DiskType
    Size        uint64
    DeleteCount uint64
    DeletedByteCount uint64
    ReadOnly    bool
    Version     types.NeedleVersion
    CompactionRevision uint16
}

// VolumeLocationList holds all DataNode addresses that have a given volume.
type VolumeLocationList struct {
    mu        sync.RWMutex
    locations []*DataNode
}

// VolumeLayout tracks volumes for a specific (replication, ttl, diskType) tuple
// within a collection.
type VolumeLayout struct {
    rp               *types.ReplicaPlacement
    ttl              *types.TTL
    diskType         types.DiskType
    volumeSizeLimit  uint64
    replicationAsMin bool

    mu               sync.RWMutex
    vid2location     map[types.VolumeId]*VolumeLocationList
    writables        []types.VolumeId
    readonlyVolumes  map[types.VolumeId]bool
    oversizedVolumes map[types.VolumeId]bool
    crowded          map[types.VolumeId]struct{}
}

// Collection groups all VolumeLayouts for a named collection.
type Collection struct {
    mu      sync.RWMutex
    Name    string
    layouts map[layoutKey]*VolumeLayout
}

type layoutKey struct {
    replication string
    ttl         string
    diskType    types.DiskType
}

// ConcurrentReadMap is a thread-safe map optimized for high read concurrency.
type ConcurrentReadMap struct {
    mu sync.RWMutex
    m  map[string]interface{}
}
```

## 6. Public Interfaces

```go
package topology

// Topology API

// NewTopology creates a new Topology root node.
func NewTopology(id NodeId, seq sequence.Sequencer, volumeSizeLimit uint64, replicationAsMin bool) *Topology

// ProcessJoinMessage updates the topology from a volume server heartbeat.
// It creates or updates the DataCenter, Rack, DataNode, and Disk nodes,
// then registers/updates all volumes and EC shards reported.
func (t *Topology) ProcessJoinMessage(msg *HeartbeatMessage) (deletedVolumeIds []types.VolumeId)

// PickForWrite selects a writable volume for a new file upload.
// Returns: (fileId, count, url of primary volume server, error)
func (t *Topology) PickForWrite(count uint64, option *WriteOption) (types.FileId, uint64, types.ServerAddress, error)

// LookupVolumeId returns all server addresses holding the given volume.
func (t *Topology) LookupVolumeId(vid types.VolumeId) ([]*DataNode, error)

// RegisterVolumeLayout registers a volume across appropriate VolumeLayouts.
func (t *Topology) RegisterVolumeLayout(info VolumeInfo, dn *DataNode)

// UnRegisterVolumeLayout removes a volume from VolumeLayouts.
func (t *Topology) UnRegisterVolumeLayout(info VolumeInfo, dn *DataNode)

// GetOrCreateDataCenter returns the DataCenter node, creating it if needed.
func (t *Topology) GetOrCreateDataCenter(dcName string) *DataCenter

// GetOrCreateRack returns the Rack node within a DataCenter.
func (dc *DataCenter) GetOrCreateRack(rackName string) *Rack

// GetOrCreateDataNode returns or creates a DataNode within a Rack.
func (r *Rack) GetOrCreateDataNode(ip string, port int, publicUrl string) *DataNode

// VolumeLayout API

// PickForWrite selects a writable volume ID and its location list.
func (vl *VolumeLayout) PickForWrite(count uint64, option *WriteOption) (types.VolumeId, *VolumeLocationList, error)

// SetVolumeWritable marks a volume as writable (after growth completes).
func (vl *VolumeLayout) SetVolumeWritable(vid types.VolumeId)

// SetVolumeReadOnly marks a volume as no longer writable.
func (vl *VolumeLayout) SetVolumeReadOnly(vid types.VolumeId)

// WriteOption specifies constraints for volume selection.
type WriteOption struct {
    Collection         string
    ReplicaPlacement   types.ReplicaPlacement
    Ttl                types.TTL
    DiskType           types.DiskType
    // Preferred datacenter/rack for locality
    DataCenter string
    Rack       string
}
```

## 7. Internal Algorithms

### ProcessJoinMessage
```
ProcessJoinMessage(msg):
  dc = t.GetOrCreateDataCenter(msg.DataCenter)
  rack = dc.GetOrCreateRack(msg.Rack)
  dn = rack.GetOrCreateDataNode(msg.Ip, msg.Port, msg.PublicUrl)
  dn.lastSeen = now()

  // Update disk capacities
  for each diskMaxCount in msg.MaxVolumeCounts:
    disk = dn.GetOrCreateDisk(diskType)
    disk.SetMaxVolumeCount(count)

  // Apply volume deltas
  for each vol in msg.NewVolumes:
    t.RegisterVolumeLayout(vol, dn)
    sendToNewVolumesChan(vol.Id)

  for each vid in msg.DeletedVolumeIds:
    t.UnRegisterVolumeLayout(vid, dn)
    sendToDeletedVolumesChan(vid)

  // Update existing volumes (size, readonly status)
  for each vol in msg.Volumes:
    vl = t.getVolumeLayout(vol)
    vl.setVolumeInfo(vol.Id, dn, vol)
    if vol.Size >= volumeSizeLimit * overcrowdedFactor:
      chanCrowdedVolumes <- vol.Id
    if vol.ReadOnly:
      vl.SetVolumeReadOnly(vol.Id)

```

### PickForWrite
```
PickForWrite(count, option):
  collection = t.getOrCreateCollection(option.Collection)
  vl = collection.GetOrCreateVolumeLayout(option.ReplicaPlacement, option.Ttl, option.DiskType)

  vid, locationList, err = vl.PickForWrite(count, option)
  if err == ErrNoWritableVolume:
    t.chanGrowRequest <- VolumeGrowRequest{option: option}
    return 0, 0, "", ErrNoWritableVolume   // master retries after growth

  // Get the primary server address
  primaryDn = locationList.PickOne()
  fileId = types.FileId{VolumeId: vid, NeedleId: t.Sequence.NextFileId(count), Cookie: randomCookie()}
  return fileId, count, primaryDn.ServerAddress(), nil
```

### VolumeLayout.PickForWrite
```
PickForWrite(count, option):
  mu.RLock()
  defer mu.RUnlock()

  if len(writables) == 0:
    return 0, nil, ErrNoWritableVolume

  // Filter by DC/rack preference if specified
  candidates = filterWritablesByPreference(writables, option)

  // Remove crowded volumes if non-crowded alternatives exist
  nonCrowded = filterNonCrowded(candidates)
  if len(nonCrowded) > 0:
    candidates = nonCrowded

  // Random selection for load distribution
  vid = candidates[rand.Intn(len(candidates))]
  return vid, vid2location[vid], nil
```

### Capacity Reservation
```
ReserveOneVolume(option):
  // Walk the tree to find a suitable DataNode
  candidates = findDataNodesWithFreeSpace(option)
  for each dn in shuffle(candidates):
    resId, err = dn.reservations.add(option.DiskType)
    if err == nil:
      return dn, resId, nil
  return nil, 0, ErrNoCapacity

CapacityReservations.add(diskType):
  mu.Lock()
  defer mu.Unlock()
  reserved = byDisk[diskType]
  free = dn.FreeSpace(diskType) - reserved
  if free <= 0: return 0, ErrNoCapacity
  id = nextId++
  byDisk[diskType]++
  byId[id] = {diskType}
  return id, nil

CapacityReservations.release(id):
  mu.Lock()
  defer mu.Unlock()
  entry = byId[id]
  byDisk[entry.diskType]--
  delete(byId, id)
```

### VolumeLayout Full/Crowded Detection
Called in `ProcessJoinMessage` whenever a volume's reported size is updated:
```
overcrowdedThreshold = volumeSizeLimit * 0.7
crowdedThreshold = volumeSizeLimit * 0.9

if vol.Size > volumeSizeLimit:
    vl.SetVolumeReadOnly(vol.Id)
    chanFullVolumes <- vol
elif vol.Size > crowdedThreshold:
    vl.markCrowded(vol.Id)
    chanCrowdedVolumes <- vol
```

## 8. Persistence Model

The topology is **not persisted**. It is entirely rebuilt from volume server heartbeats after master restart. Typically within 1–2 heartbeat intervals (5–10 seconds), the topology is fully reconstructed.

The Raft log persists the max VolumeId (via `MaxVolumeIdCommand`) to prevent VolumeId reuse across restarts.

## 9. Concurrency Model

| Structure | Lock | Usage |
|-----------|------|-------|
| `NodeImpl.children` | `sync.RWMutex childrenLock` | RLock for reads; Lock for add/remove child |
| `VolumeLayout` | `sync.RWMutex mu` | RLock for PickForWrite; Lock for register/unregister |
| `VolumeLocationList` | `sync.RWMutex mu` | RLock for read; Lock for add/remove |
| `CapacityReservations` | `sync.Mutex mu` | Lock for all operations (short critical section) |
| `DiskUsages` | `sync.RWMutex mu` | RLock for read; Lock for update |

**Deadlock avoidance**: Lock ordering is strictly top-down (Topology → Collection → VolumeLayout → VolumeLocationList). No upward locks. CapacityReservations is only locked independently, never while holding other locks.

## 10. Configuration

```go
type TopologyConfig struct {
    // VolumeSizeLimit is the max volume size in bytes. Default: 30 GB.
    VolumeSizeLimit uint64
    // ReplicationAsMin treats replica count as minimum. Default: false.
    ReplicationAsMin bool
    // DataCenter label for this master node. Default: "".
    DataCenter string
    // Rack label. Default: "".
    Rack string
}
```

The volume growth thresholds (`copy_1`, `copy_2`, threshold = 0.9) are in `VolumeGrowthConfig` (see Volume Growth plan).

## 11. Observability

- `obs.MasterVolumeCount.WithLabelValues(collection, replication).Set(count)` updated in `ProcessJoinMessage`
- Topology updates logged at DEBUG level to avoid log spam (heartbeats arrive every 5s per volume server)
- Node join events logged at INFO: `"volume server joined DC=%s rack=%s addr=%s"`
- Node departure (last seen > 30s) logged at WARN

## 12. Testing Strategy

- **Unit tests**:
  - `TestProcessJoinMessage`: simulate heartbeat, assert DataNode exists in tree
  - `TestVolumeLayoutPickForWrite`: add 5 writable volumes, call PickForWrite 100 times, assert distribution is reasonably uniform
  - `TestVolumeLayoutReadOnly`: mark volume full, assert it's excluded from PickForWrite
  - `TestCapacityReservation`: reserve max capacity, assert next reservation fails
  - `TestTopologyLookupVolumeId`: register volume on 3 nodes, assert lookup returns all 3
  - `TestTopologyPickForWritePreference`: specify DC preference, assert selected volume is in that DC
- **Concurrent tests**:
  - `TestConcurrentHeartbeats`: 50 goroutines sending heartbeats simultaneously, assert no data races (`-race`)

## 13. Open Questions

None.
