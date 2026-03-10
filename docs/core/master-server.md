# Master Server

## Assumptions

- Raft consensus requires an odd number of master nodes (1, 3, 5).
- Single-master mode (`-peers=none`) skips quorum wait for instant startup.
- The default Raft implementation is `goblob/raft`; Hashicorp Raft is opt-in via `-raftHashicorp`.

## Code Files / Modules Referenced

- `goblob/command/master.go` - CLI entry, `runMaster()`, `startMaster()`
- `goblob/command/server.go` - All-in-one launcher integration
- `goblob/server/master_server.go` - `MasterServer` struct, `NewMasterServer()`
- `goblob/server/master_grpc_server.go` - gRPC service handlers
- `goblob/server/raft_server.go` - Raft server (goblob/raft)
- `goblob/server/raft_hashicorp.go` - Hashicorp Raft adapter
- `goblob/topology/topology.go` - `Topology` struct
- `goblob/topology/volume_growth.go` - `VolumeGrowth`, `VolumeGrowRequest`
- `goblob/sequence/` - `Sequencer` interface (file ID generation)
- `goblob/wdclient/masterclient.go` - `MasterClient` (peer discovery)
- `goblob/cluster/cluster.go` - `Cluster` (filer/S3 gateway registry)

## Overview

The Master Server is the central coordinator of a GoBlob cluster. It manages the topology of all volume servers, assigns file IDs (via a sequencer), tracks volume locations, orchestrates volume growth and compaction, and provides leader election through Raft consensus.

## Responsibilities

- **Raft consensus**: Leader election and log replication across master peers
- **Topology management**: Track DataCenters, Racks, DataNodes, Disks, Volumes
- **File ID assignment**: Generate globally unique, monotonically increasing file IDs via `Sequencer`
- **Volume assignment**: Direct clients to writable volumes via `/dir/assign`
- **Volume growth**: Automatically create new volumes when capacity is low
- **Volume location lookup**: Map volume IDs to volume server addresses via `/dir/lookup`
- **Garbage collection**: Orchestrate vacuum (compaction) across volume servers
- **Cluster registry**: Track connected filers and S3 gateways
- **Heartbeat processing**: Receive periodic heartbeats from volume servers with volume status
- **Admin maintenance**: Run periodic shell scripts for rebalancing, vacuum, and replication repair
- **Telemetry**: Optional usage statistics reporting

## Architecture Role

```
+-----------------------------------------------------------+
|                     Master Server Cluster                  |
|                                                            |
|  +----------+    +----------+    +----------+              |
|  | Master 1 |<-->| Master 2 |<-->| Master 3 |   (Raft)    |
|  | (Leader) |    | Follower |    | Follower |              |
|  +----+-----+    +----------+    +----------+              |
|       |                                                    |
+-------+----------------------------------------------------+
        |
        |  gRPC: KeepConnected (heartbeat streaming)
        |
   +----+----+----+----+----+
   |    |    |    |    |    |
   v    v    v    v    v    v
 +--+ +--+ +--+ +--+ +--+ +--+
 |VS| |VS| |VS| |FL| |FL| |S3|
 +--+ +--+ +--+ +--+ +--+ +--+
  Volume     Volume   Filer   S3
  Servers    Servers  Servers  Gateways
```

## Component Structure Diagram

```
+---------------------------------------------------------------+
|                        MasterServer                            |
+---------------------------------------------------------------+
| option         *MasterOption                                   |
| guard          *security.Guard        # JWT + whitelist        |
| Topo           *topology.Topology     # cluster topology tree  |
| vg             *topology.VolumeGrowth # volume allocation      |
| volumeGrowthRequestChan  chan *VolumeGrowRequest                |
| clientChans    map[string]chan *KeepConnectedResponse           |
| MasterClient   *wdclient.MasterClient  # peer discovery       |
| adminLocks     *AdminLocks             # distributed locks     |
| Cluster        *cluster.Cluster        # filer/S3 registry     |
| grpcDialOption grpc.DialOption         # TLS config            |
| telemetryCollector *telemetry.Collector                        |
+---------------------------------------------------------------+
         |                    |                    |
         v                    v                    v
+----------------+  +------------------+  +----------------+
|   Topology     |  |   RaftServer     |  |   Sequencer    |
| (tree of nodes)|  | (consensus)      |  | (ID generation)|
+----------------+  +------------------+  +----------------+
| collectionMap  |  | raftServer       |  | (file-based or |
| RaftServer     |  |   (goblob/       |  |  raft-based or |
| HashicorpRaft  |  |    raft)         |  |  snowflake)    |
| chanFullVols   |  | OR               |  +----------------+
| chanCrowdedVols|  | RaftHashicorp    |
|                |  |   (hashicorp/    |
|                |  |    raft)         |
+----------------+  +------------------+
```

## Control Flow

### Startup Sequence

```
runMaster() or startMaster()
    |
    +--> LoadSecurityConfiguration()
    +--> LoadConfiguration("master")
    +--> backend.LoadConfiguration()
    +--> checkPeers() --> resolve master addresses
    |
    +--> NewMasterServer(router, option, peers)
    |       |
    |       +--> createSequencer()
    |       +--> NewTopology("topo", seq, volumeSizeLimit)
    |       +--> NewDefaultVolumeGrowth()
    |       +--> Register HTTP routes (mux.Router)
    |       +--> StartRefreshWritableVolumes()
    |       +--> ProcessGrowRequest()    (background goroutine)
    |       +--> startAdminScripts()     (periodic maintenance)
    |
    +--> NewRaftServer() or NewHashicorpRaftServer()
    +--> ms.SetRaftServer(raftServer)
    |       +--> Register leader change event listener
    |       +--> ensureTopologyId() if leader
    |
    +--> Start gRPC server (master_pb.RegisterGoBlob Server)
    +--> Start HTTP server (with optional TLS)
    +--> ms.MasterClient.KeepConnectedToMaster()  (background)
    +--> block on ctx.Done() / graceful shutdown via ms.Shutdown()
```

## Runtime Sequence Flow

### File ID Assignment (`/dir/assign`)

```
Client                  Master (Leader)              Topology
  |                          |                          |
  |-- POST /dir/assign ----->|                          |
  |                          |-- Assign(option) ------->|
  |                          |                          |-- pick writable volume
  |                          |                          |   (VolumeLayout.PickForWrite)
  |                          |                          |
  |                          |                          |-- if no writable volume:
  |                          |                          |   volumeGrowthRequestChan <-
  |                          |                          |   (triggers VolumeGrowth)
  |                          |                          |
  |                          |<-- (fid, volumeId, url)--|
  |<-- {fid, url, count} ----|                          |
  |                          |                          |

  Client then uploads file directly to the Volume Server at `url`.
```

### Volume Server Heartbeat

```
VolumeServer                   Master (gRPC streaming)
    |                               |
    |-- SendHeartbeat ------------->|
    |   (volumes,                   |
    |    disk status, state)        |
    |                               |--> Topology.ProcessJoinMessage()
    |                               |    - register/update DataNode
    |                               |    - update volume locations
    |                               |    - detect new/deleted volumes
    |                               |
    |<-- KeepConnectedResponse -----|
    |   (volumeSizeLimit,           |
    |    leader address, etc.)      |
    |                               |
    |   ... repeats every 5s ...    |
```

### Volume Growth

```
ProcessGrowRequest() goroutine
    |
    +--> reads from volumeGrowthRequestChan
    |
    +--> vg.AutomaticGrowByType(option, grpcDialOption, topo)
    |       |
    |       +--> findEmptySlots()  (find nodes with capacity)
    |       +--> grow(topo, option, ...)
    |       |       |
    |       |       +--> ReserveOneVolume() per data node
    |       |       |    (capacity reservation to prevent races)
    |       |       +--> AllocateVolume() via gRPC to volume server
    |       |       +--> Release reservations on success/failure
    |       |
    |       +--> returns count of new volumes created
```

## Data Flow

### Request Routing

```
HTTP Request --> mux.Router
                    |
     +--------------+--------------+
     |              |              |
  /dir/assign    /dir/lookup    /{fileId}
     |              |              |
  proxyToLeader  dirLookupHandler redirectHandler
  (if follower,     |              |
   reverse proxy    |          lookup volume
   to leader)    Topology.Lookup  location, 302
                                  redirect to
                                  volume server
```

## Data Flow Diagram

```
+----------+     +------------+     +----------+
| Client   |---->| /dir/assign|---->| Topology |
| (write)  |     | (Master)   |     | .Assign()|
+----------+     +------+-----+     +----+-----+
                        |                 |
                  {fid, url}        pick volume
                        |           from layout
                        v
                 +------+------+
                 | Volume Srv  |
                 | PUT /{fid}  |
                 +-------------+

+----------+     +------------+     +----------+
| Client   |---->| /dir/lookup|---->| Topology |
| (read)   |     | (Master)   |     | .Lookup()|
+----------+     +------+-----+     +----+-----+
                        |                 |
                  {volumeId:             find volume
                   [locations]}          locations
                        |
                        v
                 +------+------+
                 | Volume Srv  |
                 | GET /{fid}  |
                 +-------------+
```

## Dependencies

| Dependency | Purpose |
|---|---|
| `github.com/goblob/raft` | Default Raft consensus implementation |
| `github.com/hashicorp/raft` | Alternative Raft (opt-in via `-raftHashicorp`) |
| `github.com/gorilla/mux` | HTTP request router |
| `github.com/spf13/viper` | Configuration management |
| `google.golang.org/grpc` | gRPC framework |
| `goblob/topology` | Cluster topology management |
| `goblob/sequence` | File ID sequencer |
| `goblob/security` | JWT signing, TLS, IP whitelist Guard |
| `goblob/wdclient` | Master client for peer discovery |
| `goblob/cluster` | Filer/S3 gateway cluster registry |

## Error Handling

- **Raft failures**: If Raft bootstrap fails, the master fatally exits (`glog.Fatalf`)
- **Peer validation**: Odd number of peers required; even count causes fatal exit
- **Meta folder**: Write permission checked at startup; failure is fatal
- **Proxy to leader**: Non-leader masters reverse-proxy write requests to the current leader
- **Volume growth failures**: Logged and retried; backoff via `cenkalti/backoff`

## Transactions

- File ID assignment is atomic via the Raft-replicated `Sequencer`
- Volume location updates are applied through Raft log entries (`MaxVolumeIdCommand`)
- Topology ID is ensured via a Raft barrier command after leader election

## Async / Background Behavior

| Goroutine | Purpose |
|---|---|
| `ProcessGrowRequest()` | Drains `volumeGrowthRequestChan`, creates volumes |
| `StartRefreshWritableVolumes()` | Periodically checks volume health |
| `MasterClient.KeepConnectedToMaster()` | Peer discovery and leader tracking |
| `startAdminScripts()` | Periodic maintenance (vacuum, balance, replication repair) |
| `LoopPushingMetric()` | Prometheus metrics push |
| `telemetryCollector.StartPeriodicCollection()` | 24-hour telemetry reporting |

## Security Considerations

- **JWT signing**: Write (`jwt.signing.key`) and read (`jwt.signing.read.key`) keys for volume access tokens
- **IP whitelist**: `-whiteList` restricts write access to specific IPs
- **Guard**: `security.Guard` wraps handlers with whitelist + JWT validation
- **TLS**: Optional HTTPS and mTLS via `security.toml` (`https.master.cert/key/ca`)
- **gRPC TLS**: Separate TLS config for gRPC (`grpc.master` section in security.toml)

## Configuration

- **`security.toml`**: Read from `.`, `$HOME/.goblob/`, `/usr/local/etc/goblob/`, `/etc/goblob/`
- **`master.toml`**: Volume growth strategy, maintenance scripts, replication settings
- **Viper defaults**: `master.volume_growth.copy_1=7`, `copy_2=6`, `copy_3=3`, `threshold=0.9`
- **Key CLI flags**: `-port`, `-mdir`, `-peers`, `-volumeSizeLimitMB`, `-defaultReplication`, `-garbageThreshold`

## Edge Cases

- **Split brain**: Raft quorum prevents split-brain; requires majority of masters alive
- **Single-master mode**: `-peers=none` initializes immediately without waiting for quorum
- **Leader proxy latency**: Follower requests are reverse-proxied; adds one network hop
- **Volume growth contention**: `volumeGrowthRequestChan` has capacity 64; excess requests are deduplicated
- **Topology ID generation**: Uses Raft barrier to ensure log replay completes before generating ID
