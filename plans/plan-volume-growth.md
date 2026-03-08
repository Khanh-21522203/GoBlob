# Feature: Volume Growth & Allocation

## 1. Purpose

The Volume Growth subsystem is responsible for creating new volumes on volume servers when the existing writable volume pool is running low. It is triggered by the Topology Manager (when writable volumes are crowded or depleted) and runs on the Master Server leader.

Its job is to:
1. Determine how many new volumes are needed and where to place them
2. Reserve capacity on target DataNodes to prevent race conditions
3. Issue `AllocateVolume` gRPC calls to selected volume servers
4. Release capacity reservations on completion

## 2. Responsibilities

- Listen for growth requests from the topology (`volumeGrowthRequestChan`)
- Determine volume count per growth event based on replication type and strategy config
- Select target DataNodes satisfying the requested ReplicaPlacement
- Reserve capacity on selected nodes before allocation (prevent double-allocation)
- Issue `AllocateVolume` gRPC calls to volume servers
- Release reservations on success or failure
- Throttle concurrent growth operations (one active growth at a time per layout)
- Log allocation outcomes for debugging

## 3. Non-Responsibilities

- Does not actually write to disk (volume servers do that on `AllocateVolume`)
- Does not update topology state (volumes appear in the next heartbeat)
- Does not compact or delete volumes
- Does not rebalance volumes (that is the Admin Shell's `volume.balance` command)

## 4. Architecture Design

```
Topology                VolumeGrowth             Volume Servers
    |                        |                        |
    | writables running low  |                        |
    |----------------------->|                        |
    | VolumeGrowRequest      |                        |
    |                        |                        |
    |                        | find empty slots       |
    |                        | (topology tree walk)   |
    |                        |                        |
    |                        | ReserveOneVolume(dc,rack,dn)
    |                        |                        |
    |                        |-- AllocateVolume() --->|
    |                        |   (gRPC)               |
    |                        |<-- ok -----------------|
    |                        |                        |
    |                        | ReleaseReservation()   |
    |                        |                        |
    | (next heartbeat from   |                        |
    |  VS reports new vol)   |<-- Heartbeat(new vol) -|
    |<-- RegisterVol --------|                        |
```

## 5. Core Data Structures (Go)

```go
package volumegrowth

import (
    "sync"
    "goblob/core/types"
    "goblob/topology"
)

// VolumeGrowOption specifies what kind of volume to create.
type VolumeGrowOption struct {
    Collection       string
    ReplicaPlacement types.ReplicaPlacement
    Ttl              types.TTL
    DiskType         types.DiskType
    DataCenter       string  // preferred DC (optional)
    Rack             string  // preferred rack (optional)
    // Count is the number of replica sets (volumes) to create.
    // Each replica set has TotalCopies volumes (e.g., 3 for "011").
    Count int
    // Preallocate is optional disk preallocation in bytes (0 = disabled).
    Preallocate int64
}

// VolumeGrowRequest is sent on the growth request channel.
type VolumeGrowRequest struct {
    Option  VolumeGrowOption
    ErrChan chan error // optional: caller waits for result
}

// VolumeGrowth orchestrates volume creation.
type VolumeGrowth struct {
    // accessLock serializes concurrent growth operations for the same layout.
    accessLock sync.Mutex
    grpcDialOption grpc.DialOption
    logger *slog.Logger
}

// VolumeGrowthStrategy defines how many volumes to grow per replication type.
// These match the master.volume_growth.* configuration keys.
type VolumeGrowthStrategy struct {
    // Copy1Count: volumes to create for "000" replication. Default: 7.
    Copy1Count int
    // Copy2Count: volumes for "001". Default: 6.
    Copy2Count int
    // Copy3Count: volumes for "010" or "100". Default: 3.
    Copy3Count int
    // CopyOtherCount: volumes for higher replication types. Default: 1.
    CopyOtherCount int
    // Threshold: grow when this fraction of writables are full. Default: 0.9.
    Threshold float64
}

// AllocationResult is the outcome of a single volume allocation attempt.
type AllocationResult struct {
    DataNodeAddr types.ServerAddress
    VolumeId     types.VolumeId
    Err          error
}
```

## 6. Public Interfaces

```go
package volumegrowth

// NewVolumeGrowth creates a VolumeGrowth coordinator.
func NewVolumeGrowth(grpcDialOption grpc.DialOption) *VolumeGrowth

// ProcessGrowRequest is the main background goroutine.
// It drains requestChan and processes each growth request.
// It runs until ctx is cancelled.
func (vg *VolumeGrowth) ProcessGrowRequest(
    ctx context.Context,
    requestChan <-chan VolumeGrowRequest,
    topo *topology.Topology,
    strategy VolumeGrowthStrategy,
)

// AutomaticGrowByType executes a single growth operation for the given option.
// Returns the number of volumes successfully created.
func (vg *VolumeGrowth) AutomaticGrowByType(
    option VolumeGrowOption,
    topo *topology.Topology,
) (int, error)

// GrowByCountAndType creates exactly `count` volumes matching the option.
func (vg *VolumeGrowth) GrowByCountAndType(
    count int,
    option VolumeGrowOption,
    topo *topology.Topology,
) (int, error)
```

## 7. Internal Algorithms

### ProcessGrowRequest goroutine
```
ProcessGrowRequest(ctx, requestChan, topo, strategy):
  for:
    select:
    case req := <-requestChan:
      option = req.Option
      // Determine count from strategy
      option.Count = strategy.countForReplication(option.ReplicaPlacement)

      n, err = vg.AutomaticGrowByType(option, topo)
      if req.ErrChan != nil:
        req.ErrChan <- err

    case <-ctx.Done():
      return
```

### countForReplication (strategy)
```
total = rp.TotalCopies()  // 1 + DC + rack + server copies
switch total:
case 1: return Copy1Count   // "000"
case 2: return Copy2Count   // "001"
case 3: return Copy3Count   // "010", "100", "011"
default: return CopyOtherCount
```

### AutomaticGrowByType
```
AutomaticGrowByType(option, topo):
  vg.accessLock.Lock()
  defer vg.accessLock.Unlock()

  return vg.grow(topo, option)
```

### grow (core allocation logic)
```
grow(topo, option):
  servers, err = findEmptySlots(topo, option)
  if err: return 0, err

  // Assign a new VolumeId
  vid = topo.NextVolumeId()  // increments max volume ID, replicated via Raft

  results = make([]AllocationResult, len(servers))
  reservations = make([]ReservationId, len(servers))

  // Reserve capacity on all target servers before allocating
  for i, dn in servers:
    resId, err = dn.ReserveOneVolume(option.DiskType)
    if err:
      // Release already-acquired reservations
      releaseAll(servers[:i], reservations[:i])
      return 0, ErrInsufficientCapacity
    reservations[i] = resId

  // Allocate volumes on reserved servers in parallel
  var wg sync.WaitGroup
  for i, dn in servers:
    wg.Add(1)
    go func(idx int, dataNode *topology.DataNode):
      defer wg.Done()
      err = allocateVolume(dataNode, vid, option)
      results[idx] = AllocationResult{dataNode.ServerAddress(), vid, err}
    (i, dn)
  wg.Wait()

  // Release all reservations
  for i, dn in servers:
    dn.ReleaseReservation(reservations[i])

  // Count successes
  successCount = 0
  for _, r in results:
    if r.Err == nil: successCount++
    else: log.Warn("volume allocation failed", "addr", r.DataNodeAddr, "err", r.Err)

  return successCount, nil
```

### findEmptySlots
```
findEmptySlots(topo, option):
  rp = option.ReplicaPlacement
  totalCopies = rp.TotalCopies()

  // Walk the topology tree finding DataNodes with free capacity
  // Respect DC and rack constraints:
  //   - rp.DifferentDataCenterCount replicas must be in distinct DCs
  //   - rp.DifferentRackCount replicas in distinct racks within same DC
  //   - rp.SameRackCount replicas in distinct servers within same rack
  //   - 1 primary replica on any server

  candidates = topo.PickNodesByWeight(totalCopies, func(dn *DataNode) bool {
    return dn.FreeSpace(option.DiskType) > 0 &&
           matchesDCPreference(dn, option.DataCenter) &&
           matchesRackPreference(dn, option.Rack)
  })

  if len(candidates) < totalCopies:
    return nil, ErrNotEnoughNodes

  // Ensure replication placement is satisfied
  selected = selectByReplicaPlacement(candidates, rp)
  return selected, nil
```

### allocateVolume (gRPC call)
```
allocateVolume(dn *DataNode, vid VolumeId, option VolumeGrowOption):
  return pb.WithVolumeServerClient(dn.ServerAddress(), grpcDialOption, func(client pb.VolumeServerClient) error:
    _, err = client.AllocateVolume(ctx, &pb.AllocateVolumeRequest{
      VolumeId:   uint32(vid),
      Collection: option.Collection,
      Replication: option.ReplicaPlacement.String(),
      Ttl:         option.Ttl.String(),
      DiskType:    string(option.DiskType),
      Preallocate: option.Preallocate,
    })
    return err
  )
```

### Threshold-based Growth Trigger
The topology monitors writable volumes per `VolumeLayout`. When the fraction of full writables exceeds `strategy.Threshold`:
```
StartRefreshWritableVolumes(ctx, topo, strategy, requestChan):
  ticker = time.NewTicker(30s)
  for:
    select:
    case <-ticker.C:
      for each VolumeLayout in topo.collectionMap:
        if vl.WritableFraction() > strategy.Threshold:
          requestChan <- VolumeGrowRequest{
            Option: VolumeGrowOption{
              Collection: vl.Collection,
              ReplicaPlacement: vl.ReplicaPlacement,
              ...
            }
          }
    case <-ctx.Done():
      return
```

Channel capacity is 64. If the channel is full, new requests are dropped (de-duplicated naturally since growth catches up).

## 8. Persistence Model

Volume growth itself has no persistence. The side effect of growth (a new volume on a volume server) is persisted by the volume server and reported in the next heartbeat. The max `VolumeId` is replicated via Raft (`MaxVolumeIdCommand`) so it survives master restarts.

## 9. Concurrency Model

`VolumeGrowth.accessLock` is a `sync.Mutex` that serializes concurrent growth operations for the same layout. This prevents two simultaneous growths from picking overlapping DataNodes.

Within a single growth call, the per-volume `allocateVolume` gRPC calls are parallelized using `sync.WaitGroup` for efficiency (e.g., creating 7 volumes in parallel).

Capacity reservations (`CapacityReservations` in the topology) protect against races between parallel growth calls and between growth and request routing.

## 10. Configuration

```go
type VolumeGrowthConfig struct {
    Copy1Count     int     `mapstructure:"copy_1"     default:"7"`
    Copy2Count     int     `mapstructure:"copy_2"     default:"6"`
    Copy3Count     int     `mapstructure:"copy_3"     default:"3"`
    CopyOtherCount int     `mapstructure:"copy_other" default:"1"`
    Threshold      float64 `mapstructure:"threshold"  default:"0.9"`
}
```

## 11. Observability

- Allocation attempts and successes logged at INFO: `"allocated volume vid=%d on %d/%d servers"`
- Allocation failures logged at WARN per server: `"volume allocation failed addr=%s err=%v"`
- Insufficient nodes logged at WARN: `"not enough nodes for replication placement=%s needed=%d got=%d"`
- `requestChan` depth exposed via `expvar` at `"growth.queue_depth"`

## 12. Testing Strategy

- **Unit tests**:
  - `TestCountForReplication`: assert correct count for "000", "001", "010", "100" placements
  - `TestFindEmptySlotsSimple`: 3-node topology, "001" placement, assert 2 nodes selected
  - `TestFindEmptySlotsInsufficientNodes`: request "100" with 1 DC, assert ErrNotEnoughNodes
  - `TestGrowReservationReleased`: mock gRPC that fails, assert reservations released
  - `TestGrowParallelAllocation`: mock gRPC, assert AllocateVolume called once per selected node
- **Integration tests**:
  - `TestEndToEndVolumeGrowth`: mock volume servers (gRPC stubs), trigger growth, assert volumes appear in topology after simulated heartbeat

## 13. Open Questions

None.
