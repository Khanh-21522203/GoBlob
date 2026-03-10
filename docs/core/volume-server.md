# Volume Server

## Assumptions

- Each volume server manages one or more local disk directories (`DiskLocation`).
- gRPC port defaults to HTTP port + 10000 (e.g., 8080 -> 18080).
- Volume servers discover the master via seed addresses and maintain a streaming heartbeat.

## Code Files / Modules Referenced

- `goblob/command/volume.go` - CLI entry, `runVolume()`, `startVolumeServer()`
- `goblob/server/volume_server.go` - `VolumeServer` struct, `NewVolumeServer()`
- `goblob/server/volume_server_handlers*.go` - HTTP request handlers
- `goblob/server/volume_grpc_*.go` - gRPC service handlers
- `goblob/storage/store.go` - `Store` struct (per volume server)
- `goblob/storage/disk_location.go` - `DiskLocation` (per directory)
- `goblob/storage/volume.go` - `Volume` struct (.dat + .idx pair)
- `goblob/storage/volume_read.go` - Needle read path
- `goblob/storage/volume_write.go` - Needle write path
- `goblob/storage/volume_vacuum.go` - Compaction logic

## Overview

The Volume Server is the data plane of GoBlob. It stores actual file data as "needles" inside volume files. Each volume server manages multiple volumes across one or more disk directories. It communicates with the master server via gRPC heartbeats and serves read/write requests directly from clients.

## Responsibilities

- **Store file data**: Write and read needles (blobs) in volume `.dat` files
- **Maintain needle index**: Track needle offsets via in-memory or LevelDB index (`.idx`)
- **Heartbeat to master**: Report volume status and disk usage periodically
- **Serve HTTP/gRPC**: Handle direct client uploads and downloads
- **Volume lifecycle**: Load, create, compact, close, and delete volumes
- **Replication**: Replicate writes to peer volume servers (synchronous)
- **Compaction**: Vacuum deleted needles to reclaim disk space
- **Concurrency control**: Limit concurrent upload/download data sizes

## Architecture Role

```
+-----------------------------------------------------------------+
|                       Volume Server                              |
+-----------------------------------------------------------------+
|                                                                  |
|  HTTP :8080         gRPC :18080         Public :8080             |
|  (admin + data)     (internal ops)      (read-only, optional)    |
|       |                  |                     |                 |
|  +----+--+          +----+----+          +-----+------+          |
|  |Handler|          |gRPC Svc |          |Public Mux  |          |
|  | Mux   |          |VolumeServer|       |(read only) |          |
|  +---+---+          +----+----+          +-----+------+          |
|      |                   |                     |                 |
|      +-------------------+---------------------+                 |
|                          |                                       |
|                    +-----+------+                                |
|                    |   Store    |                                 |
|                    +-----+------+                                |
|                          |                                       |
|          +---------------+---------------+                       |
|          |               |               |                       |
|    +-----+-----+  +-----+-----+  +------+----+                  |
|    |DiskLoc[0] |  |DiskLoc[1] |  |DiskLoc[N] |                  |
|    | /data/ssd |  | /data/hdd |  | /mnt/ext  |                  |
|    +-----+-----+  +-----+-----+  +-----+-----+                  |
|          |               |              |                        |
|     +----+----+     +----+----+    +----+----+                   |
|     |Vol 1    |     |Vol 5    |    |Vol 9    |                   |
|     |Vol 2    |     |Vol 6    |    |Vol 10   |                   |
|     |Vol 3    |     |Vol 7    |    +---------+                   |
|     +---------+     +---------+                                  |
+-----------------------------------------------------------------+
```

## Component Structure Diagram

```
+--------------------------------------------------------------+
|                      VolumeServer                             |
+--------------------------------------------------------------+
| SeedMasterNodes  []pb.ServerAddress                           |
| currentMaster    pb.ServerAddress                             |
| store            *storage.Store          # all volumes        |
| guard            *security.Guard         # JWT + whitelist    |
| grpcDialOption   grpc.DialOption                              |
| needleMapKind    storage.NeedleMapKind   # memory|leveldb     |
| pulsePeriod      time.Duration           # heartbeat interval |
| dataCenter       string                                       |
| rack             string                                       |
| whiteList        []string                                     |
| ReadMode         string                  # local|proxy|redirect|
| FixJpgOrientation bool                                        |
| compactionBytePerSecond  int64           # throttle compaction|
| fileSizeLimitBytes       int64           # max upload size    |
| concurrentUploadLimit    int64                                |
| concurrentDownloadLimit  int64                                |
| inFlightUploadDataLimitCond   *sync.Cond                      |
| inFlightDownloadDataLimitCond *sync.Cond                      |
| hasSlowRead      bool                   # experimental        |
| readBufferSizeMB int                                          |
| isHeartbeating   bool                                         |
| stopChan         chan bool                                     |
+--------------------------------------------------------------+
          |
          v
+--------------------------------------------------------------+
|                         Store                                 |
+--------------------------------------------------------------+
| Ip, Port, GrpcPort, PublicUrl, Id                             |
| Locations           []*DiskLocation                           |
| volumeSizeLimit     uint64               # from master        |
| NeedleMapKind       NeedleMapKind                             |
| State               *State               # persisted state    |
| StateUpdateChan     chan *VolumeServerState                    |
| NewVolumesChan      chan VolumeShortInformationMessage         |
| DeletedVolumesChan  chan VolumeShortInformationMessage         |
+--------------------------------------------------------------+
          |
          v
+--------------------------------------------------------------+
|                      DiskLocation                             |
+--------------------------------------------------------------+
| Directory          string                # e.g. /data/ssd     |
| DirectoryUuid      string                # unique per dir     |
| IdxDirectory       string                # .idx file location |
| DiskType           types.DiskType        # hdd|ssd|<tag>      |
| Tags               []string              # custom tags        |
| MaxVolumeCount     int32                                      |
| MinFreeSpace       util.MinFreeSpace                          |
| volumes            map[VolumeId]*Volume  # loaded volumes     |
| isDiskSpaceLow     bool                  # checked per minute |
+--------------------------------------------------------------+
```

## Control Flow

### Startup Sequence

```
runVolume() / startVolumeServer()
    |
    +--> Parse folder paths, max counts, disk types, tags
    +--> TestFolderWritable() for each directory
    |
    +--> NewVolumeServer(adminMux, publicMux, ...)
    |       |
    |       +--> LoadClientTLS("grpc.volume")
    |       +--> checkWithMaster()  (get volume size limit)
    |       +--> storage.NewStore(...)
    |       |       |
    |       |       +--> for each directory:
    |       |       |       NewDiskLocation(dir, maxCount, ...)
    |       |       |       go loadExistingVolumesWithId()
    |       |       |           +--> scan .dat files
    |       |       |           +--> Volume.load() for each
    |       |       +--> wg.Wait()  (parallel loading)
    |       |       +--> NewState(idxFolder)
    |       |
    |       +--> security.NewGuard(whiteList, signingKey, ...)
    |       +--> Register HTTP routes
    |       +--> go vs.heartbeat()
    |
    +--> startGrpcService(volumeServer)
    +--> startPublicHttpService()  (if separate public port)
    +--> startClusterHttpService()
    +--> SIGHUP handler: volumeServer.LoadNewVolumes + ReloadSecurityConfig
    +--> block on ctx.Done()
```

## Runtime Sequence Flow

### Write Path (Upload)

```
Client                 VolumeServer               Volume            Replica
  |                        |                        |                  |
  |-- PUT /vid,fid,cookie->|                        |                  |
  |                        |-- guard.WhiteList() -->|                  |
  |                        |-- parse multipart ---->|                  |
  |                        |                        |                  |
  |                        |-- needle.CreateNeedleFromRequest()        |
  |                        |                        |                  |
  |                        |-- store.WriteVolumeNeedle(vid, needle)    |
  |                        |       |                |                  |
  |                        |       +--> vol.writeNeedle(needle)        |
  |                        |       |       |        |                  |
  |                        |       |       +--> dataFileAccessLock.Lock()
  |                        |       |       +--> append to .dat file    |
  |                        |       |       +--> update .idx (NeedleMap)|
  |                        |       |       +--> dataFileAccessLock.Unlock()
  |                        |       |                |                  |
  |                        |       +--> ReplicatedWrite() ----------->|
  |                        |       |    (sync replicate to peers)      |
  |                        |       |                |                  |
  |<-- 201 {size, eTag} ---|       |                |                  |
```

### Read Path (Download)

```
Client                 VolumeServer               Volume
  |                        |                        |
  |-- GET /vid,fid,cookie->|                        |
  |                        |                        |
  |                        |-- store.ReadVolumeNeedle(vid, needle, readOption)
  |                        |       |                |
  |                        |       +--> vol.readNeedle(needle, readOption)
  |                        |       |       |        |
  |                        |       |       +--> nm.Get(needleId)
  |                        |       |       |    (lookup offset+size in index)
  |                        |       |       |        |
  |                        |       |       +--> ReadNeedleBlob() from .dat
  |                        |       |       |    (pread at offset)
  |                        |       |       |        |
  |                        |       |       +--> needle.ReadData()
  |                        |       |       |    (parse header, verify CRC)
  |                        |       |                |
  |<-- 200 + file data ----|                        |
  |                        |                        |

  If ReadMode == "proxy" and volume not local:
      VolumeServer proxies request to the volume server that has the data.
  If ReadMode == "redirect":
      VolumeServer returns 301 redirect to the correct volume server.
```

### Heartbeat Loop

```
vs.heartbeat()
    |
    +--> connect to master via gRPC streaming
    |    (master_pb.MasterServiceClient.SendHeartbeat)
    |
    +--> send initial heartbeat:
    |    - all volumes (id, size, collection, replica, ttl, ...)
    |    - ip, port, publicUrl, dataCenter, rack
    |    - maxVolumeCounts per disk location
    |
    +--> enter loop:
    |    +-- wait for pulsePeriod (5s default)
    |    +-- collect delta: NewVolumesChan, DeletedVolumesChan
    |    +-- send incremental heartbeat
    |    +-- receive response:
    |    |   - volumeSizeLimit (update store)
    |    |   - leader address
    |    |   - metrics config
    |    +-- loop
    |
    +--> on disconnect: retry with backoff
```

## Data Flow Diagram

```
+----------+                    +-----------------+
|  Client  |                    |  Master Server  |
+----+-----+                    +--------+--------+
     |                                   |
     |  1. POST /dir/assign              |
     |---------------------------------->|
     |  <-- {fid:"3,01234...", url:"vs1:8080"}
     |                                   |
     |  2. PUT http://vs1:8080/3,01234... |
     |---------------------------------->|
     |        VolumeServer               |
     |        +----------+               |
     |        | Volume 3 |               |
     |        | .dat: append needle      |
     |        | .idx: update index       |
     |        +----------+               |
     |  <-- 201 Created                  |
     |                                   |
     |  3. GET http://vs1:8080/3,01234...|
     |---------------------------------->|
     |        | Volume 3 |               |
     |        | .idx: lookup offset      |
     |        | .dat: pread needle       |
     |  <-- 200 + file bytes             |
```

## Dependencies

| Dependency | Purpose |
|---|---|
| `goblob/storage` | `Store`, `DiskLocation`, `Volume`, `NeedleMap` |
| `goblob/security` | JWT guard, TLS loading |
| `goblob/pb/volume_server_pb` | gRPC service definition |
| `goblob/pb/master_pb` | Heartbeat message types |
| `goblob/util/httpdown` | Graceful HTTP server shutdown |
| `google.golang.org/grpc` | gRPC framework |
| `github.com/spf13/viper` | Configuration |

## Error Handling

- **Disk full**: `DiskLocation.CheckDiskSpace()` runs every minute; marks volumes ReadOnly when below `MinFreeSpace`
- **Volume not found**: Returns HTTP 404 for reads, error for writes
- **CRC mismatch**: Needle checksum verified on read; returns error if corrupt
- **Heartbeat disconnect**: Retries with backoff; volume server continues serving local data
- **Concurrent limits**: Upload/download throttled via `sync.Cond`; blocks with timeout (`inflightUploadDataTimeout`)

## Async / Background Behavior

| Goroutine | Purpose |
|---|---|
| `vs.heartbeat()` | Streaming gRPC heartbeat to master |
| `obs.StartPushgateway()` | Prometheus metrics push (optional) |
| `DiskLocation.CheckDiskSpace()` | Per-minute disk space monitoring |
| `Volume.startWorker()` | Async needle write worker (channel-based) |
| Compaction | Triggered by master via gRPC; throttled by `compactionBytePerSecond` |

## Security Considerations

- **JWT tokens**: Write/read tokens signed by master; verified by `security.Guard`
- **IP whitelist**: `-whiteList` restricts write operations
- **TLS**: Optional HTTPS (`https.volume.cert/key`) and mTLS (`https.volume.ca`)
- **gRPC TLS**: `grpc.volume` section in `security.toml`
- **Public port separation**: Optionally expose read-only public port separate from admin port

## Configuration

- **Key CLI flags**: `-dir`, `-max`, `-port`, `-master`, `-index`, `-disk`, `-readMode`
- **Index types**: `memory` (fast, high RAM), `leveldb` (low RAM), `leveldbMedium`, `leveldbLarge`
- **Read modes**: `local` (404 if not local), `proxy` (forward to remote), `redirect` (302 to remote)
- **Disk types**: `hdd`, `ssd`, or custom tags for tiered storage
- **Concurrency**: `-concurrentUploadLimitMB`, `-concurrentDownloadLimitMB`
- **Slow read**: `-hasSlowRead=true` uses per-request read buffers to prevent blocking

## Edge Cases

- **Volume loading parallelism**: Each `DiskLocation` loads volumes in a separate goroutine; `sync.WaitGroup` ensures all loaded before serving
- **LevelDB timeout**: `-index.leveldbTimeout` offloads idle LevelDB instances to reduce memory
- **Pre-stop delay**: `-preStopSeconds` (default 10) stops heartbeat before shutting down, giving master time to reroute traffic
- **Separate idx directory**: `-dir.idx` allows storing index files on a different (faster) disk
- **SIGHUP reload**: SIGHUP signal triggers `LoadNewVolumes()` and `ReloadSecurityConfig()` to pick up new volumes without restart
