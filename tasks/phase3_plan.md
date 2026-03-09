# Phase 3: Cluster Coordination Implementation Plan

## Overview
Phase 3 implements distributed coordination primitives for cluster orchestration: Raft consensus, file ID sequencing, topology management, volume growth strategy, and cluster registry.

---

## Task 1: Raft Consensus Layer (`goblob/raft`)

### Files to create:
- `goblob/raft/raft_server.go` - RaftServer interface and implementation
- `goblob/raft/fsm.go` - MasterFSM implementation
- `goblob/raft/raft_test.go` - Tests

### Key components:

**1. RaftServer interface:**
```go
type RaftServer interface {
    IsLeader() bool
    LeaderAddress() string
    Apply(cmd RaftCommand, timeout time.Duration) error
    Barrier(timeout time.Duration) error
    AddPeer(addr string) error
    RemovePeer(addr string) error
    Stats() map[string]string
    Shutdown() error
}
```

**2. RaftCommand types:**
- `MaxFileIdCommand` - advances sequencer's max file ID
- `MaxVolumeIdCommand` - records highest VolumeId allocated
- `TopologyIdCommand` - sets cluster UUID
- `LogEntry` - wraps command with type info

**3. MasterFSM:**
- `Apply(log *hraft.Log)` - processes committed log entries
- `Snapshot()` - creates point-in-time snapshot
- `Restore(io.ReadCloser)` - restores from snapshot
- Callbacks: `onMaxFileIdUpdate`, `onMaxVolumeIdUpdate`, `onTopologyIdSet`

**4. RaftConfig & NewRaftServer:**
- BoltDB log store: `{meta}/raft-log.bolt`
- BoltDB stable store: `{meta}/raft-stable.bolt`
- FileSnapshotStore: `{meta}/snapshots` (retain 3)
- TCP transport for Raft communication
- Bootstrap logic for single-node vs multi-node

**5. Helper:**
- `RedirectToLeader(rs RaftServer, w, r)` - HTTP redirect for followers

### Tests:
- `TestSingleModeStartup` - becomes leader immediately
- `TestFSMApplyMaxFileId` - callback invoked correctly
- `TestFSMSnapshotRestore` - state preserved
- `TestFollowerRedirect` - 307 redirect

---

## Task 2: File ID Sequencer (`goblob/sequence`)

### Files to create:
- `goblob/sequence/sequencer.go` - Interface
- `goblob/sequence/file_sequencer.go` - File-based implementation
- `goblob/sequence/raft_sequencer.go` - Raft wrapper
- `goblob/sequence/snowflake_sequencer.go` - Coordination-free IDs
- `goblob/sequence/sequencer_test.go` - Tests

### Key components:

**1. Sequencer interface:**
```go
type Sequencer interface {
    NextFileId(count uint64) uint64
    SetMax(maxId uint64)
    GetMax() uint64
}
```

**2. FileSequencer:**
- Persists to `{dir}/max_needle_id` (ASCII decimal + newline)
- Pre-allocation step size: 10000 (atomic write to temp + rename)
- Algorithm: lock → check saved → write if needed → increment → unlock

**3. RaftSequencer:**
- Wraps FileSequencer
- Submits `MaxFileIdCommand` to Raft log before incrementing

**4. SnowflakeSequencer:**
- 41-bit timestamp | 10-bit machineId | 12-bit sequence
- No coordination required

### Tests:
- `TestFileSequencerMonotonic` - strictly increasing
- `TestFileSequencerBatch` - correct batch ranges
- `TestFileSequencerRestart` - state recovered
- `TestFileSequencerSetMax` - SetMax advances correctly
- `BenchmarkFileSequencer` - concurrent load

---

## Task 3: Topology Manager (`goblob/topology`)

### Files to create:
- `goblob/topology/node.go` - Node interface and NodeImpl
- `goblob/topology/topology.go` - Topology root
- `goblob/topology/data_center.go` - DataCenter
- `goblob/topology/rack.go` - Rack
- `goblob/topology/data_node.go` - DataNode, DiskInfo
- `goblob/topology/volume_layout.go` - VolumeLayout, VolumeLocation
- `goblob/topology/topology_test.go` - Tests

### Key components:

**1. Node interface:**
```go
type Node interface {
    Id() string
    FreeSpace() int64
    ReserveOneVolume(r *VolumeGrowRequest) (*DataNode, error)
    Children() []Node
    Parent() Node
    SetParent(Node)
    IsDataNode() bool
}
```

**2. Topology hierarchy:**
- `Topology` → `DataCenter` → `Rack` → `DataNode` → `DiskInfo` → `[Volumes]`
- `collectionMap` - per-collection volume tracking
- `chanFullVolumes`, `chanCrowdedVolumes` - notification channels

**3. Topology methods:**
- `LookupVolume(vid)` - returns all VolumeLocations
- `GetOrCreateDataCenter(dcName)` - finds or creates
- `ProcessJoinMessage(heartbeat)` - handles volume server heartbeat
- `NextVolumeId()` - atomic volume ID generation

**4. DataNode:**
- `UpdateVolumes(msgs)` - returns added/deleted volume IDs
- `GetAddress()` - returns "ip:port"
- `IsAlive()` - true if `lastSeen` within 30s

**5. VolumeLayout:**
- Tracks writable/readonly/crowded volumes per (collection, replica, TTL, diskType)
- `PickForWrite(option)` - selects volume for new upload
- `SetVolumeWritable/ReadOnly` - moves volume between states

### Tests:
- `TestTopologyProcessJoinMessage` - heartbeat tracking
- `TestVolumeLayoutPickForWrite` - volume selection
- `TestTopologyLookupVolume` - location lookup
- `TestReplicaPlacementSelection` - DC/rack constraints

---

## Task 4: Volume Growth Strategy (`goblob/topology/volume_growth.go`)

### Files to create:
- `goblob/topology/volume_growth.go` - Growth logic
- Tests in `topology_test.go`

### Key components:

**1. Types:**
```go
type VolumeGrowOption struct {
    Collection       string
    ReplicaPlacement types.ReplicaPlacement
    Ttl              types.TTL
    DiskType         types.DiskType
    Preallocate      int64
}

type VolumeGrowRequest struct {
    Option VolumeGrowOption
    Count  int
}
```

**2. CapacityReservation:**
- `AddReservation(dt, count)` - temporarily reduces FreeSpace
- `RemoveReservation(id)` - releases reservation
- Prevents double-allocation under concurrent growth

**3. AutomaticGrowByType:**
- Determines count from config (copy_1=7, copy_2=6, copy_3=3)
- `findEmptySlots(option, topo, count)` - selects DataNodes satisfying ReplicaPlacement
- For each group: reserve → allocate VolumeId → gRPC AllocateVolume → register

**4. ProcessGrowRequest goroutine:**
- Listens on growthChan
- Deduplicates in-flight requests
- Calls AutomaticGrowByType

**5. Threshold monitoring:**
- Trigger growth when `writableCount < 0.9 * totalCount`

### Tests:
- `TestGrowthThresholdTrigger` - growth at 90% threshold
- `TestCapacityReservationPreventsDoubleAlloc` - concurrent safety
- `TestGrowthWithInsufficientNodes` - error on replication failure
- `TestReservationRollbackOnFailure` - cleanup on gRPC error

---

## Task 5: Cluster Registry (`goblob/cluster`)

### Files to create:
- `goblob/cluster/registry.go` - ClusterRegistry
- `goblob/cluster/registry_test.go` - Tests

### Key components:

**1. ClusterNode:**
```go
type ClusterNode struct {
    ClientType    string  // "filer", "s3"
    ClientAddress string  // "host:port"
    Version       string
    DataCenter    string
    Rack          string
    GrpcPort      string
    lastSeen      time.Time
}
```

**2. ClusterRegistry:**
- `Register(req)` - adds/updates node
- `Unregister(addr)` - removes node
- `ListNodes(clientType)` - returns nodes by type
- `ExpireDeadNodes()` - removes stale entries (30s timeout)

**3. HandleKeepConnected:**
- Bidirectional stream handler
- Registers on heartbeat
- Sends volume location updates
- Unregisters on stream close

**4. Background goroutine:**
- Calls `ExpireDeadNodes()` every 30s

### Tests:
- `TestRegistryRegisterAndList` - registration by type
- `TestRegistryExpireDeadNodes` - expiration logic

---

## Dependencies (already present):
- `github.com/hashicorp/raft` - Raft consensus
- `github.com/syndtr/goleveldb/leveldb` - BoltDB alternative (if needed)
- `github.com/gorilla/mux` - HTTP routing (Phase 4)
- `goblob/util/concurrent_read_map.go` - for VolumeLayout
- Protobuf definitions in `goblob/pb/master_pb/`

---

## Implementation Order:
1. **Task 1 (Raft)** - Foundation for distributed state
2. **Task 2 (Sequencer)** - Independent, can parallelize with Task 3
3. **Task 3 (Topology)** - Core data structure
4. **Task 4 (Volume Growth)** - Depends on Topology
5. **Task 5 (Cluster Registry)** - Independent, can parallelize

---

## Phase 3 Checkpoint Verification:
1. Raft single-mode startup → leader within 2s
2. Sequencer concurrent test → 100 unique monotonic IDs
3. Topology heartbeat → correct tree structure
4. Volume growth → triggers at 90% threshold
5. Full test suite: `go test ./goblob/{topology,sequence,raft,cluster}/... -race`
