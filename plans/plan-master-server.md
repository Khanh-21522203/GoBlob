# Feature: Master Server

## 1. Purpose

The Master Server is the central coordinator of the GoBlob cluster. It runs the Raft consensus layer for leader election, manages the cluster topology tree, assigns globally unique file IDs to clients, orchestrates volume growth, processes heartbeats from volume servers, and tracks connected filer and gateway nodes.

In a production deployment, three or five master nodes form a Raft quorum; one node is the leader and handles all write operations; followers proxy write requests to the leader and can answer read-only requests (volume lookups).

## 2. Responsibilities

- **Raft consensus**: Run as a Raft cluster for leader election and log replication
- **File ID assignment** (`/dir/assign`): Return `{fid, url}` for a client upload
- **Volume location lookup** (`/dir/lookup`): Return server addresses for a given VolumeId
- **Heartbeat processing**: Receive and process gRPC `SendHeartbeat` from volume servers
- **Volume growth**: Trigger and coordinate new volume creation when capacity is low
- **Sequencer**: Generate globally unique, monotonically increasing NeedleIds
- **Cluster registry**: Track connected filers and S3 gateways via `KeepConnected`
- **Admin maintenance**: Run periodic shell scripts (vacuum, balance) on the leader
- **Redirect non-leaders**: Follower masters reverse-proxy requests requiring leadership to the leader
- **VolumeId max tracking**: Persist the highest VolumeId via Raft for crash recovery

## 3. Non-Responsibilities

- Does not store blob data (volume servers do that)
- Does not store file metadata (filer servers do that)
- Does not serve direct file reads/writes to clients (only the assignment endpoint)
- Does not run compaction directly (triggered via admin shell vacuum command)

## 4. Architecture Design

```
+--------------------------------------------------------------+
|                     MasterServer                              |
+--------------------------------------------------------------+
|                                                               |
|  HTTP :9333             gRPC :19333                           |
|  (admin + /dir/*)       (heartbeat, KeepConnected)           |
|       |                       |                              |
|  mux.Router             gRPC service impl                    |
|  /dir/assign            MasterService                        |
|  /dir/lookup            SendHeartbeat (bidi stream)          |
|  /dir/status            KeepConnected (bidi stream)          |
|  /vol/vacuum            Assign, LookupVolume (unary)         |
|  /cluster/*                                                  |
|       |                       |                              |
+-------+-----------------------+------------------------------+
        |                       |
   +----+----+             +----+----+
   |Topology |             |RaftSvr  |
   |Manager  |             |(leader  |
   | + VG    |             | elect.) |
   +----+----+             +---------+
        |
   +----+----+
   |Sequencer|
   |(file ID |
   | gen)    |
   +---------+
```

### Request Routing
```
/dir/assign
    |
    +-- IsLeader? No --> proxyToLeader (HTTP reverse proxy)
    |   Yes --> Topology.PickForWrite() --> return {fid, url, jwt}

/dir/lookup?volumeId=5
    |
    +-- Topology.LookupVolumeId(5) --> return [{addr, publicUrl}, ...]
    (can be answered by followers; topology is eventually consistent)

/{fileId}
    |
    +-- Lookup volume location --> 302 redirect to volume server URL
```

## 5. Core Data Structures (Go)

```go
package master

import (
    "sync"
    "net/http"
    "goblob/core/types"
    "goblob/topology"
    "goblob/raft"
    "goblob/sequencer"
    "goblob/security"
    "goblob/pb"
    "goblob/volumegrowth"
    "goblob/cluster"
)

// MasterServer is the top-level struct for the master server process.
type MasterServer struct {
    option *MasterOption

    // Guard enforces IP whitelist and JWT on volume write operations.
    guard  *security.Guard

    // Topo is the live cluster topology tree.
    Topo   *topology.Topology

    // VG coordinates volume growth (creates new volumes).
    VG     *volumegrowth.VolumeGrowth

    // volumeGrowthRequestChan is sent growth requests from the topology.
    volumeGrowthRequestChan chan volumegrowth.VolumeGrowRequest

    // MasterClient discovers and connects to peer masters.
    MasterClient *pb.MasterClient

    // Cluster tracks connected filers and S3 gateways.
    Cluster *cluster.Cluster

    // Raft is the consensus layer.
    Raft raft.RaftServer

    grpcDialOption grpc.DialOption

    // adminLocks prevents concurrent maintenance script runs.
    adminLocks *AdminLocks

    // Context and cancel for lifecycle management.
    ctx    context.Context
    cancel context.CancelFunc

    logger *slog.Logger
}

// MasterOption holds all runtime configuration for the master server.
type MasterOption struct {
    Host                string
    Port                int
    MetaDir             string
    Peers               []string
    VolumeSizeLimitMB   uint32
    DefaultReplication  string
    GarbageThreshold    float64
    VolumeGrowthConfig  volumegrowth.VolumeGrowthStrategy
    MaintenanceConfig   MaintenanceConfig
    DataCenter          string
    Rack                string
    ReplicationAsMin    bool
}

// MaintenanceConfig holds the automated maintenance settings.
type MaintenanceConfig struct {
    Scripts      string // newline-separated shell commands
    SleepMinutes int
}

// AdminLocks prevents concurrent maintenance operations.
type AdminLocks struct {
    mu      sync.Mutex
    lockedBy string // empty = unlocked
}

// AssignRequest is the parsed request body for /dir/assign.
type AssignRequest struct {
    Count       uint64
    Collection  string
    Replication string // e.g. "001"
    Ttl         string // e.g. "3d"
    DataCenter  string
    Rack        string
    DiskType    string
}

// AssignResponse is the JSON response for /dir/assign.
type AssignResponse struct {
    Fid       string `json:"fid"`
    Url       string `json:"url"`
    PublicUrl string `json:"publicUrl"`
    Count     uint64 `json:"count"`
    Error     string `json:"error,omitempty"`
}

// VolumeLocationResponse is the JSON response for /dir/lookup.
type VolumeLocationResponse struct {
    VolumeOrFileId string              `json:"volumeOrFileId"`
    Locations      []VolumeLocation   `json:"locations"`
    Error          string             `json:"error,omitempty"`
}

type VolumeLocation struct {
    Url        string `json:"url"`
    PublicUrl  string `json:"publicUrl"`
}
```

## 6. Public Interfaces

```go
package master

// NewMasterServer creates and initializes a MasterServer.
// It registers HTTP routes, starts background goroutines, and connects to peers.
func NewMasterServer(mux *http.ServeMux, opt *MasterOption, peers []string) (*MasterServer, error)

// SetRaftServer attaches the Raft consensus layer after startup.
// This must be called after NewMasterServer and before serving requests.
func (ms *MasterServer) SetRaftServer(raft raft.RaftServer)

// Shutdown gracefully stops the master server.
func (ms *MasterServer) Shutdown()

// HTTP handlers (registered internally, but documented for clarity):
//
// POST /dir/assign      -> handleAssign
// GET  /dir/lookup      -> handleLookup
// GET  /dir/status      -> handleStatus
// GET  /vol/vacuum      -> handleVacuum (orchestrate compaction)
// GET  /cluster/status  -> handleClusterStatus
// GET  /raft/stats      -> handleRaftStats
```

## 7. Internal Algorithms

### handleAssign
```
handleAssign(w, r):
  if !ms.Raft.IsLeader():
    proxyToLeader(w, r, ms.Raft.LeaderAddress())
    return

  req = parseAssignRequest(r)
  rp = parseReplicaPlacement(req.Replication, ms.option.DefaultReplication)
  ttl = parseTTL(req.Ttl)

  opt = topology.WriteOption{
    Collection:       req.Collection,
    ReplicaPlacement: rp,
    Ttl:              ttl,
    DiskType:         types.DiskType(req.DiskType),
    DataCenter:       req.DataCenter,
    Rack:             req.Rack,
  }

  fid, count, serverAddr, err = ms.Topo.PickForWrite(req.Count, opt)
  if err == topology.ErrNoWritableVolume:
    // Trigger growth and tell client to retry
    ms.volumeGrowthRequestChan <- VolumeGrowRequest{Option: opt}
    http.Error(w, "no writable volumes", http.StatusServiceUnavailable)
    return

  // Generate JWT for volume server
  jwt = security.GenJwtForVolumeServer(ms.guard.SigningKey, ms.guard.ExpiresAfterSec, fid.String())

  resp = AssignResponse{
    Fid:       fid.String(),
    Url:       serverAddr.ToHttpAddress(),
    PublicUrl: getPublicUrl(serverAddr),
    Count:     count,
  }
  writeJSON(w, resp)
  obs.MasterAssignRequests.WithLabelValues("ok").Inc()
```

### handleLookup
```
handleLookup(w, r):
  // Followers can answer lookups (read-only, eventual consistency)
  vidStr = r.URL.Query().Get("volumeId")
  vid = parseVolumeId(vidStr)

  nodes, err = ms.Topo.LookupVolumeId(vid)
  if err != nil:
    writeJSON(w, VolumeLocationResponse{Error: err.Error()})
    return

  locations = []VolumeLocation{}
  for each dn in nodes:
    locations = append(locations, VolumeLocation{dn.HttpUrl(), dn.PublicUrl()})

  writeJSON(w, VolumeLocationResponse{
    VolumeOrFileId: vidStr,
    Locations:      locations,
  })
```

### proxyToLeader
```
proxyToLeader(w, r, leaderAddr):
  if leaderAddr == "":
    http.Error(w, "no leader elected", http.StatusServiceUnavailable)
    return
  target = "http://" + leaderAddr + r.URL.RequestURI()
  proxy = httputil.NewSingleHostReverseProxy(parseURL(target))
  proxy.ServeHTTP(w, r)
```

### Heartbeat Processing (gRPC SendHeartbeat)
```
SendHeartbeat(stream):
  for:
    msg, err = stream.Recv()
    if err: return

    // Update topology
    deletedVids = ms.Topo.ProcessJoinMessage(msg)

    // Send response with current leader and size limit
    stream.Send(&master_pb.HeartbeatResponse{
      VolumeSizeLimit: ms.option.VolumeSizeLimitMB * 1024 * 1024,
      Leader:          ms.Raft.LeaderAddress(),
      MetricsAddress:  ms.metricsAddr,
    })
```

### Volume Growth Background Goroutine
```
go ProcessGrowRequest(ctx, volumeGrowthRequestChan, topo, strategy):
  // See Volume Growth plan for full algorithm.
  // Channel capacity: 64. Excess requests are dropped (deduplication by design).
```

### startAdminScripts (leader only)
```
startAdminScripts():
  scripts = ms.option.MaintenanceConfig.Scripts
  sleepMinutes = ms.option.MaintenanceConfig.SleepMinutes

  // Wrap with lock/unlock if not already present
  if !strings.Contains(scripts, "lock"):
    scripts = "lock\n" + scripts + "\nunlock"

  go func():
    ticker = time.NewTicker(sleepMinutes * time.Minute)
    for:
      select:
      case <-ticker.C:
        if !ms.Raft.IsLeader(): continue
        if ms.adminLocks.IsLocked(): continue  // admin shell connected
        runMaintenanceScripts(scripts)
      case <-ms.ctx.Done():
        return
```

### KeepConnected (gRPC)
Filer and S3 gateways call `KeepConnected` to register with the master:
```
KeepConnected(stream):
  for:
    req, err = stream.Recv()
    if err: return

    // Register client in Cluster registry
    ms.Cluster.Register(req.ClientType, req.ClientAddress)

    // Stream volume location updates to client
    go streamVolumeUpdates(stream, ms.Topo)
```

## 8. Persistence Model

The Master Server persists:
1. **Raft log and snapshots** (via the Raft layer, in `MetaDir/raft-log.db`, `raft-stable.db`, `snapshots/`)
2. **Sequencer state** (in `MetaDir/max_needle_id`)
3. **Topology ID** (via Raft `TopologyIdCommand`)

The topology tree itself is **not** persisted — it is rebuilt from heartbeats on restart within 1–2 heartbeat intervals (~10 seconds).

## 9. Concurrency Model

| Component | Mechanism |
|-----------|-----------|
| HTTP handlers | Multiple goroutines (Go HTTP server default) |
| `volumeGrowthRequestChan` | Buffered channel (capacity 64); producer = topology; consumer = ProcessGrowRequest goroutine |
| Topology tree | Lock-per-node (see Topology plan) |
| Raft consensus | Raft library internal locks; `Apply` blocks until consensus |
| Admin scripts | `AdminLocks.mu` prevents concurrent runs |
| Cluster registry | `sync.RWMutex` inside `cluster.Cluster` |

## 10. Configuration

```go
type MasterOption struct {
    Host                string        // bind address; default: ""
    Port                int           // HTTP port; default: 9333
    GRPCPort            int           // gRPC port; default: 19333
    MetaDir             string        // Raft + sequencer state dir; required
    Peers               []string      // other master addresses (odd count)
    VolumeSizeLimitMB   uint32        // max volume size in MB; default: 30000
    DefaultReplication  string        // default "000"
    GarbageThreshold    float64       // vacuum trigger; default: 0.3
    VolumeGrowthConfig  VolumeGrowthStrategy
    MaintenanceConfig   MaintenanceConfig
    DataCenter          string
    Rack                string
    ReplicationAsMin    bool
    WhiteList           []string      // IP whitelist for writes
    SecurityConfig      *security.SecurityConfig
}
```

## 11. Observability

- `obs.MasterLeadershipGauge.Set(1/0)` on leader change
- `obs.MasterAssignRequests.WithLabelValues("ok"/"error").Inc()` per assign
- `obs.MasterVolumeCount.WithLabelValues(collection, replication).Set(count)` updated per heartbeat
- Leader election events logged at INFO with Raft term
- Heartbeat processing errors logged at WARN
- Admin script start/completion logged at INFO

Debug HTTP endpoints:
- `GET /raft/stats` — Raft state (term, commit index, applied index, state)
- `GET /cluster/status` — list of connected nodes
- `GET /dir/status` — topology summary

## 12. Testing Strategy

- **Unit tests**:
  - `TestHandleAssignLeader`: mock topology.PickForWrite, assert correct JSON response
  - `TestHandleAssignFollower`: set IsLeader=false, assert redirect to leader
  - `TestHandleAssignNoWritableVolume`: topology returns ErrNoWritableVolume, assert 503
  - `TestHandleLookup`: mock topology.LookupVolumeId, assert location list
  - `TestHeartbeatProcessing`: send mock heartbeat, assert topology updated
  - `TestProxyToLeaderNoLeader`: LeaderAddress="", assert 503
- **Integration tests** (in-process, single-master):
  - `TestMasterStartup`: start master, assert `/dir/status` returns OK
  - `TestMasterAssignAndLookup`: assign file ID, look up volume, assert matching volume server
- **3-node cluster tests**:
  - `TestMasterRaftLeaderElection`: start 3 masters, assert one becomes leader
  - `TestMasterFollowerProxiesAssign`: POST /dir/assign to follower, assert proxied to leader

## 13. Open Questions

None.
