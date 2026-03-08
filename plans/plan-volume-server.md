# Feature: Volume Server

## 1. Purpose

The Volume Server is the data plane of GoBlob. It stores blob data as needles inside volume files on local disks and serves read/write requests directly from clients. It communicates with the master server via gRPC streaming heartbeats and executes storage operations (compact, allocate, delete) on master instruction.

Every file upload and download ultimately passes through a volume server. The volume server's throughput and latency directly determine the system's end-user performance.

## 2. Responsibilities

- **Blob writes**: Accept PUT requests, write needle to the local volume, replicate to peers
- **Blob reads**: Accept GET requests, look up needle by FileId, stream data to client
- **Blob deletes**: Accept DELETE requests, write tombstone, mark needle deleted
- **Heartbeat**: Send periodic gRPC streaming heartbeats to master with volume status
- **Volume allocation**: Create new volumes on master instruction (`AllocateVolume` gRPC)
- **Volume compaction**: Execute vacuum (compaction) on master instruction
- **Volume copy**: Copy a volume from another volume server (for rebalancing)
- **Concurrency throttling**: Limit in-flight upload and download data sizes
- **Read fallback**: Proxy or redirect reads for volumes not held locally

## 3. Non-Responsibilities

- Does not store file metadata (path-to-FileId mapping); that is the filer's job
- Does not assign FileIds (that is the master's job)
- Does not run the Raft consensus layer
- Does not manage the topology tree

## 4. Architecture Design

```
+--------------------------------------------------------------+
|                       VolumeServer                            |
+--------------------------------------------------------------+
|                                                               |
|  HTTP :8080              gRPC :18080        Public :optional  |
|  (write + read)          (internal ops)     (read-only)       |
|       |                       |                  |            |
|  mux.Router            VolumeService         read mux         |
|  PUT /{fid}            AllocateVolume        GET /{fid}       |
|  GET /{fid}            VolumeDelete                           |
|  DELETE /{fid}         VolumeCopy                             |
|  GET /status           VolumeCompact                          |
|       |                ReadAllNeedles                         |
|       |                       |                              |
|  +----+----+           +-------+------+                       |
|  | Guard   |           |   Store      |                       |
|  | (JWT +  |           | (storage     |                       |
|  |  whitlst|           |  engine)     |                       |
|  +---------+           +-------+------+                       |
|                                |                              |
|                  +-------------+-------------+               |
|                  |             |             |               |
|             DiskLoc[0]   DiskLoc[1]   DiskLoc[N]            |
+--------------------------------------------------------------+
```

## 5. Core Data Structures (Go)

```go
package volume

import (
    "sync"
    "goblob/core/types"
    "goblob/storage"
    "goblob/security"
    "goblob/replication"
    "goblob/pb"
)

// VolumeServer is the top-level struct for the volume server process.
type VolumeServer struct {
    // SeedMasterNodes are the master addresses used for initial discovery.
    SeedMasterNodes []types.ServerAddress
    // currentMaster is the current master leader address.
    currentMaster   types.ServerAddress
    masterMu        sync.RWMutex

    // store manages all local volumes.
    store *storage.Store

    // guard enforces JWT and IP whitelist on write/read requests.
    guard *security.Guard

    grpcDialOption grpc.DialOption

    // replicator sends writes to replica servers.
    replicator replication.Replicator

    // replicaLocations caches VolumeId -> [replica addresses].
    replicaLocations *replication.ReplicaLocations

    option *VolumeServerOption

    // Concurrency limiters
    inFlightUploadBytes   int64 // atomic
    inFlightDownloadBytes int64 // atomic
    uploadLimitCond       *sync.Cond
    downloadLimitCond     *sync.Cond

    // isHeartbeating tracks whether the heartbeat goroutine is running.
    isHeartbeating bool
    stopChan       chan struct{}

    ctx    context.Context
    cancel context.CancelFunc

    logger *slog.Logger
}

// VolumeServerOption holds all runtime configuration for the volume server.
type VolumeServerOption struct {
    Host                     string
    Port                     int
    GRPCPort                 int
    PublicPort               int // 0 = disabled
    Masters                  []string
    DataCenter               string
    Rack                     string
    Directories              []storage.DiskDirectoryConfig
    NeedleMapKind            storage.NeedleMapKind
    ReadMode                 ReadMode // local | proxy | redirect
    WhiteList                []string
    ConcurrentUploadLimitMB  int64
    ConcurrentDownloadLimitMB int64
    FileSizeLimitMB          int64
    HeartbeatInterval        time.Duration
    PreStopSeconds           int
    CompactionBytesPerSecond int64
    LevelDBTimeoutHours      int
    FixJpgOrientation        bool
    HasSlowRead              bool
    ReadBufferSizeMB         int
    SecurityConfig           *security.SecurityConfig
}

// ReadMode controls behavior when the requested volume is not on this server.
type ReadMode string
const (
    ReadModeLocal    ReadMode = "local"    // return 404
    ReadModeProxy    ReadMode = "proxy"    // proxy request to correct server
    ReadModeRedirect ReadMode = "redirect" // 302 redirect to correct server
)
```

## 6. Public Interfaces

```go
package volume

// NewVolumeServer creates and initializes a VolumeServer.
func NewVolumeServer(adminMux *http.ServeMux, publicMux *http.ServeMux, opt *VolumeServerOption) (*VolumeServer, error)

// Shutdown gracefully stops the volume server.
// It stops heartbeats first, waits PreStopSeconds, then shuts down HTTP/gRPC.
func (vs *VolumeServer) Shutdown()

// LoadNewVolumes scans for newly-added volume files and loads them.
// Called on SIGHUP (hot reload).
func (vs *VolumeServer) LoadNewVolumes()

// HTTP handlers (registered internally):
//
// PUT  /{vid},{key}{cookie}    -> handleWrite
// GET  /{vid},{key}{cookie}    -> handleRead
// DELETE /{vid},{key}{cookie}  -> handleDelete
// GET  /status                 -> handleStatus
// GET  /vol/vacuum/check       -> handleVacuumCheck
// GET  /vol/vacuum/needle      -> handleVacuumNeedle
// GET  /vol/vacuum/commit      -> handleVacuumCommit
```

## 7. Internal Algorithms

### handleWrite (PUT /{fid})
```
handleWrite(w, r):
  // 1. Throttle: check in-flight upload bytes
  if vs.option.ConcurrentUploadLimitMB > 0:
    vs.uploadLimitCond.L.Lock()
    for atomic.LoadInt64(&vs.inFlightUploadBytes) > limit:
      vs.uploadLimitCond.Wait()  // block until space available
    atomic.AddInt64(&vs.inFlightUploadBytes, r.ContentLength)
    vs.uploadLimitCond.L.Unlock()
    defer: atomic.AddInt64(&vs.inFlightUploadBytes, -r.ContentLength); vs.uploadLimitCond.Signal()

  // 2. Guard check
  if err = vs.guard.CheckRequest(r); err != nil:
    http.Error(w, "Unauthorized", 401); return

  // 3. Parse FileId from URL
  fid, err = types.ParseFileId(r.URL.Path[1:])

  // 4. Parse needle from request body
  n, originalSize, contentMD5, err = needle.CreateNeedleFromRequest(r, vs.option.FixJpgOrientation, fileSizeLimit)
  n.Id = fid.NeedleId
  n.Cookie = fid.Cookie

  // 5. Is this a replica write?
  isReplication = r.Header.Get("X-Replication") == "true"

  // 6. Write locally
  result, err = vs.store.WriteVolumeNeedle(storage.WriteRequest{
    VolumeId:      fid.VolumeId,
    Needle:        n,
    IsReplication: isReplication,
  })
  if err != nil:
    handleWriteError(w, err); return

  // 7. Replicate (if primary write)
  if !isReplication:
    replicaAddrs = vs.replicaLocations.Get(fid.VolumeId)
    myAddr = vs.store.ServerAddress()
    peerAddrs = filterSelf(replicaAddrs, myAddr)
    if len(peerAddrs) > 0:
      err = vs.replicator.ReplicatedWrite(r.Context(), replication.ReplicateRequest{
        VolumeId:    fid.VolumeId,
        Needle:      n,
        NeedleBytes: serializeNeedle(n),
        JWTToken:    r.Header.Get("Authorization"),
      }, peerAddrs)
      if err != nil:
        http.Error(w, "replication failed: "+err.Error(), 500); return

  // 8. Respond
  w.Header().Set("ETag", contentMD5)
  w.WriteHeader(201)
  writeJSON(w, map[string]interface{}{"size": result.Size, "eTag": contentMD5})
```

### handleRead (GET /{fid})
```
handleRead(w, r):
  // 1. Throttle downloads
  if vs.option.ConcurrentDownloadLimitMB > 0:
    // same pattern as upload throttle

  // 2. Parse FileId
  fid, err = types.ParseFileId(r.URL.Path[1:])

  // 3. Check if volume is local
  vol = vs.store.FindVolume(fid.VolumeId)

  if vol == nil:
    switch vs.option.ReadMode:
    case ReadModeLocal:
      http.NotFound(w, r); return
    case ReadModeRedirect:
      url = lookupVolumeOnMaster(fid.VolumeId)
      http.Redirect(w, r, url, 302); return
    case ReadModeProxy:
      proxyReadRequest(w, r, fid.VolumeId); return

  // 4. Read needle
  n = &needle.Needle{Id: fid.NeedleId, Cookie: fid.Cookie}
  count, err = vs.store.ReadVolumeNeedle(fid.VolumeId, n, storage.ReadOption{})
  if err != nil:
    handleReadError(w, r, err); return

  // 5. Serve data
  w.Header().Set("Content-Type", string(n.Mime))
  w.Header().Set("ETag", fmt.Sprintf("%x", n.Checksum))
  if n.LastModified > 0:
    w.Header().Set("Last-Modified", httpDate(n.LastModified))

  // Handle Range requests, If-None-Match, etc.
  serveContent(w, r, n.Data, n.LastModified)
```

### Heartbeat Loop
```
heartbeat():
  vs.isHeartbeating = true
  defer func() { vs.isHeartbeating = false }()

  masterClient = pb.NewMasterClient(vs.masterDiscovery, pb.VolumeServerType, vs.addr, ...)

  for:
    err = pb.WithMasterStreamingClient(vs.currentMaster, vs.grpcDialOption, func(client):
      stream, err = client.SendHeartbeat(ctx)
      if err: return err

      // Send initial full heartbeat
      stream.Send(vs.buildFullHeartbeat())

      // Periodic incremental heartbeats
      ticker = time.NewTicker(vs.option.HeartbeatInterval)
      for:
        select:
        case <-ticker.C:
          delta = vs.buildDeltaHeartbeat()
          stream.Send(delta)
          resp, err = stream.Recv()
          if err: return err
          vs.store.SetVolumeSizeLimit(resp.VolumeSizeLimit)
          vs.masterMu.Lock()
          vs.currentMaster = types.ServerAddress(resp.Leader)
          vs.masterMu.Unlock()
          vs.replicaLocations.UpdateFromResponse(resp)
        case <-vs.stopChan:
          return nil
    )
    if err: log.Warn("heartbeat disconnected", "err", err)
    time.Sleep(1 * time.Second)  // retry backoff
```

### buildFullHeartbeat
```
buildFullHeartbeat():
  hb = &master_pb.Heartbeat{
    Ip:        vs.store.Ip,
    Port:      vs.store.Port,
    GrpcPort:  vs.store.GrpcPort,
    PublicUrl: vs.store.PublicUrl,
    DataCenter: vs.option.DataCenter,
    Rack:       vs.option.Rack,
  }
  for each loc in vs.store.Locations:
    hb.MaxVolumeCounts = append(hb.MaxVolumeCounts, {diskType: loc.DiskType, count: loc.MaxVolumeCount})
    for each vol in loc.volumes:
      hb.Volumes = append(hb.Volumes, volumeToProto(vol))
    return hb
```

### Graceful Shutdown
```
Shutdown():
  // 1. Stop sending heartbeats (master will reroute traffic)
  close(vs.stopChan)
  time.Sleep(vs.option.PreStopSeconds * time.Second)

  // 2. Stop accepting new connections
  vs.httpServer.Shutdown(context.Background())
  vs.grpcServer.GracefulStop()

  // 3. Close storage
  vs.store.Close()
```

## 8. Persistence Model

The volume server's persistence is managed entirely by the Storage Engine (see that plan). The volume server itself persists nothing additional; it is the pass-through layer between HTTP/gRPC and the storage engine.

State that survives restart:
- All `.dat` and `.idx` files (loaded by Store at startup)
- `state.pb` (persistent VolumeId→DiskIndex mapping)
- `.uuid` files (directory identity)

## 9. Concurrency Model

| Mechanism | Usage |
|-----------|-------|
| `sync.Cond uploadLimitCond` | Blocks writes when in-flight upload bytes exceed limit |
| `sync.Cond downloadLimitCond` | Blocks reads when in-flight download bytes exceed limit |
| `atomic.Int64 inFlightUploadBytes` | Lock-free tracking of current upload bytes |
| `atomic.Int64 inFlightDownloadBytes` | Lock-free tracking of current download bytes |
| `sync.RWMutex masterMu` | Protects `currentMaster` field |
| Storage Engine locks | `dataFileAccessLock` per volume (see Storage Engine plan) |

**No goroutine leak**: All background goroutines (heartbeat, disk space check, async write worker) are started at startup and stopped cleanly via `stopChan` or `ctx.Done()`.

## 10. Configuration

```go
type VolumeServerOption struct {
    Host                      string
    Port                      int           `default:"8080"`
    GRPCPort                  int           `default:"18080"` // 0 = HTTP+10000
    PublicPort                int           `default:"0"`
    Masters                   []string      // required
    DataCenter                string        `default:""`
    Rack                      string        `default:""`
    Directories               []DiskDirectoryConfig // required, at least 1
    NeedleMapKind             storage.NeedleMapKind `default:"NeedleMapLevelDB"`
    ReadMode                  ReadMode      `default:"redirect"`
    WhiteList                 []string
    ConcurrentUploadLimitMB   int64         `default:"0"` // 0 = unlimited
    ConcurrentDownloadLimitMB int64         `default:"0"`
    FileSizeLimitMB           int64         `default:"256"`
    HeartbeatInterval         time.Duration `default:"5s"`
    PreStopSeconds            int           `default:"10"`
    CompactionBytesPerSecond  int64         `default:"0"`
    LevelDBTimeoutHours       int           `default:"0"`
    FixJpgOrientation         bool          `default:"false"`
    HasSlowRead               bool          `default:"false"`
    ReadBufferSizeMB          int           `default:"4"`
}
```

## 11. Observability

- `obs.VolumeServerNeedleWriteBytes.WithLabelValues(vid).Add(bytes)` per write
- `obs.VolumeServerNeedleReadBytes.WithLabelValues(vid).Add(bytes)` per read
- `obs.VolumeServerNeedleWriteLatency.WithLabelValues(vid).Observe(duration)` per write
- `obs.VolumeServerNeedleReadLatency.WithLabelValues(vid).Observe(duration)` per read
- `obs.VolumeServerDiskFreeBytes.WithLabelValues(dir).Set(free)` per minute
- Heartbeat disconnect events logged at WARN
- Volume loading at startup logged at INFO per volume
- Compaction start/end logged at INFO

Debug endpoints:
- `GET /status` — JSON with all volumes, sizes, and read/write status
- `GET /stats/counter` — operation counters

## 12. Testing Strategy

- **Unit tests**:
  - `TestHandleWrite`: valid PUT, assert 201 and volume contains needle
  - `TestHandleWriteOversized`: PUT with body > fileSizeLimitMB, assert 413
  - `TestHandleWriteReplication`: X-Replication header, assert no outbound replication
  - `TestHandleRead`: write then read, assert identical data
  - `TestHandleReadVolumeNotFoundLocal`: ReadModeLocal, volume absent, assert 404
  - `TestHandleReadVolumeNotFoundRedirect`: ReadModeRedirect, assert 302 with correct location
  - `TestHandleDelete`: write then delete, assert subsequent read returns 404
  - `TestUploadThrottle`: set limit, send request over limit, assert it blocks
  - `TestGracefulShutdown`: assert heartbeat stops before http server closes
- **Integration tests**:
  - `TestVolumeServerEndToEnd`: start master + volume server, upload blob via HTTP, read it back

## 13. Open Questions

None.
