# Feature: Filer Server

## 1. Purpose

The Filer Server provides a filesystem-like metadata layer on top of GoBlob volume storage. It maps full file paths to lists of data chunks stored on volume servers. The filer supports POSIX-like operations (create, read, update, delete, list, rename) and exposes both HTTP and gRPC interfaces.

Multiple filer instances can run concurrently, sharing metadata via a pluggable backend store and synchronizing changes via the `MetaAggregator`.

## 2. Responsibilities

- **File metadata CRUD**: Create, read, update, delete file entries with full path support
- **Directory listing**: List directory contents with pagination and prefix filtering
- **Chunk management**: Track file-to-chunk mappings; split large files into multiple chunks
- **Volume assignment**: Request file IDs from master for new chunks
- **Chunk read coordination**: Look up volume locations and stream chunk data to clients
- **Metadata notification**: Broadcast metadata changes via a log buffer for gRPC subscribers
- **Cross-filer synchronization**: `MetaAggregator` subscribes to peer filers to stay in sync
- **Background deletion**: Asynchronously delete chunks from volume servers when entries are deleted
- **Path-specific config**: Per-path replication, collection, TTL via `FilerConf`
- **Distributed lock**: `DistributedLockManager` for cross-filer coordination
- **IAM gRPC**: Credential management co-hosted on gRPC port

## 3. Non-Responsibilities

- Does not store blob data directly (delegates to volume servers)
- Does not implement S3 authentication (handled in S3 gateway)
- Does not manage EC encoding (handled by admin shell)
- Does not run Raft consensus (master handles that)

## 4. Architecture Design

```
+--------------------------------------------------------------+
|                       FilerServer                             |
+--------------------------------------------------------------+
|                                                               |
|  HTTP :8888              gRPC :18888                          |
|  (file ops + UI)         (metadata + IAM)                    |
|       |                       |                              |
|  mux.Router             FilerServiceServer                    |
|  GET /path              LookupDirectoryEntry                  |
|  POST /path             ListEntries (stream)                  |
|  DELETE /path           CreateEntry                           |
|  /healthz               UpdateEntry                           |
|                         DeleteEntry                           |
|                         AtomicRenameEntry                     |
|                         SubscribeMetadata (stream)            |
|                         AssignVolume                          |
|                         LookupVolume                          |
|                         KvGet/KvPut/KvDelete                  |
|       |                       |                              |
|  +----+----+           +------+------+                        |
|  | Guard   |           |    Filer    |                        |
|  | (JWT)   |           | (core logic)|                        |
|  +---------+           +------+------+                        |
|                               |                              |
|                    +----------+----------+                    |
|                    |                     |                    |
|               FilerStore          MetaAggregator              |
|               (pluggable)         (peer sync)                 |
+--------------------------------------------------------------+
```

## 5. Core Data Structures (Go)

```go
package filerserver

import (
    "sync"
    "goblob/filer"
    "goblob/security"
    "goblob/pb"
    "goblob/cluster"
)

// FilerServer is the top-level struct for the filer server process.
type FilerServer struct {
    option *FilerOption

    // filer is the core metadata engine.
    filer *Filer

    // filerGuard enforces JWT on filer-level operations.
    filerGuard *security.Guard
    // volumeGuard is used when generating volume-access tokens for chunk reads.
    volumeGuard *security.Guard

    grpcDialOption grpc.DialOption
    secret         security.SigningKey

    // knownListeners tracks active SubscribeMetadata gRPC streams.
    knownListeners  map[int32]int32
    listenersMu     sync.Mutex
    // listenersCond is broadcast when new metadata events arrive.
    listenersCond   *sync.Cond

    // inFlightDataSize tracks current in-flight upload bytes.
    inFlightDataSize int64 // atomic
    inFlightDataLimitCond *sync.Cond

    credentialManager *credential.CredentialManager

    ctx    context.Context
    cancel context.CancelFunc

    logger *slog.Logger
}

// Filer is the core metadata engine (independent of HTTP/gRPC).
type Filer struct {
    UniqueFilerId int32  // random per-instance ID (prevents cross-filer subscribe loops)

    Store filer.VirtualFilerStore

    MasterClient *pb.MasterClient

    // fileIdDeletionQueue buffers chunk IDs to delete from volume servers.
    fileIdDeletionQueue *util.UnboundedQueue

    DirBucketsPath string // default: "/buckets"
    Cipher         bool   // volume-level encryption

    // LocalMetaLogBuffer buffers metadata change events for subscribers.
    LocalMetaLogBuffer *log_buffer.LogBuffer

    // MetaAggregator subscribes to peer filers.
    MetaAggregator *MetaAggregator

    FilerConf    *FilerConf
    Dlm          *DistributedLockManager

    MaxFilenameLength uint32  // default: 255

    DeletionRetryQueue  *DeletionRetryQueue
    EmptyFolderCleaner  *EmptyFolderCleaner

    logger *slog.Logger
}

// MetaAggregator subscribes to peer filers and replays their metadata events locally.
// This keeps multiple filer instances in sync without a central coordinator.
type MetaAggregator struct {
    filer          *Filer
    self           types.ServerAddress
    // peerFilers contains other filer addresses discovered via master.
    peerFilers     map[types.ServerAddress]struct{}
    peerFilersMu   sync.RWMutex
    logger *slog.Logger
}

// FilerConf holds per-path configuration overrides.
type FilerConf struct {
    // rules maps path prefixes to their configuration overrides.
    rules []FilerConfRule
    mu    sync.RWMutex
}

// FilerConfRule applies a storage policy to all entries under a path prefix.
type FilerConfRule struct {
    PathPrefix  string
    Collection  string
    Replication string
    Ttl         string
    DiskType    string
    MaxMB       int32
    Fsync       bool
}

// FilerOption holds all runtime configuration for the filer server.
type FilerOption struct {
    Masters                 []string
    Host                    string
    Port                    int
    GRPCPort                int
    DefaultStoreDir         string
    MaxFileSizeMB           int
    EncryptVolumeData       bool
    MaxFilenameLength       uint32
    BucketsFolder           string
    DefaultReplication      string
    DefaultCollection       string
    LogFlushIntervalSeconds int
    ConcurrentUploadLimitMB int64
    SecurityConfig          *security.SecurityConfig
}

// DeletionRetryQueue handles retrying failed chunk deletions.
type DeletionRetryQueue struct {
    queue    chan DeletionTask
    maxRetry int
}

type DeletionTask struct {
    FileId  string
    Retries int
}
```

## 6. Public Interfaces

```go
package filerserver

// NewFilerServer creates and initializes a FilerServer.
func NewFilerServer(defaultMux, readonlyMux *http.ServeMux, opt *FilerOption) (*FilerServer, error)

// Shutdown gracefully stops the filer server.
func (fs *FilerServer) Shutdown()

// Filer core operations (used by HTTP handlers and gRPC service)

func (f *Filer) CreateEntry(ctx context.Context, entry *filer.Entry, exclusive bool, skipCheckParentDir bool, so *operation.AssignedFileId) error
func (f *Filer) UpdateEntry(ctx context.Context, oldEntry *filer.Entry, entry *filer.Entry) (err error)
func (f *Filer) FindEntry(ctx context.Context, p filer.FullPath) (entry *filer.Entry, err error)
func (f *Filer) DeleteEntry(ctx context.Context, p filer.FullPath, isDeleteData bool, skippedChunks []*filer.FileChunk, recursive bool) (err error)
func (f *Filer) ListDirectoryEntries(ctx context.Context, p filer.FullPath, startFileName string, inclusive bool, limit int64, prefix string, eachEntryFn func(*filer.Entry) bool) (string, error)
func (f *Filer) DoRename(ctx context.Context, oldPath filer.FullPath, newPath filer.FullPath) error
func (f *Filer) AssignVolume(ctx context.Context, req *filer_pb.AssignVolumeRequest) (*filer_pb.AssignVolumeResponse, error)
```

## 7. Internal Algorithms

### File Upload (POST /path/to/file)
```
handleFileUpload(w, r):
  // 1. Throttle in-flight upload data
  if limitEnabled:
    cond.L.Lock()
    for inFlightDataSize + contentLength > limit:
      cond.Wait()
    atomic.AddInt64(&inFlightDataSize, contentLength)
    cond.L.Unlock()
    defer: atomic.AddInt64(&inFlightDataSize, -contentLength); cond.Signal()

  // 2. Determine path-specific config
  rule = filerConf.MatchRule(r.URL.Path)

  // 3. Chunk large files
  chunkSize = rule.MaxMB * 1024 * 1024  // default: 4MB
  chunks = []

  for each chunkData in splitBySize(r.Body, chunkSize):
    // 3a. Assign file ID from master
    assignResp = f.AssignVolume(ctx, &filer_pb.AssignVolumeRequest{
      Count:       1,
      Collection:  rule.Collection,
      Replication: rule.Replication,
      Ttl:         rule.Ttl,
    })

    // 3b. Upload chunk to volume server
    PUT http://assignResp.Url/assignResp.FileId with chunkData

    chunks = append(chunks, &filer.FileChunk{
      FileId: assignResp.FileId,
      Offset: offset,
      Size:   int64(len(chunkData)),
    })
    offset += int64(len(chunkData))

  // 4. Save entry metadata to filer store
  entry = &filer.Entry{
    FullPath: filer.FullPath(r.URL.Path),
    Attr: filer.Attr{
      Mode:   0644,
      Mtime:  time.Now(),
      Mime:   detectedMime,
      FileSize: totalSize,
    },
    Chunks: chunks,
  }
  f.CreateEntry(ctx, entry, false, false, nil)

  // 5. Notify metadata subscribers
  f.LocalMetaLogBuffer.AppendEntry(entry)

  w.WriteHeader(201)
```

### File Download (GET /path/to/file)
```
handleFileDownload(w, r):
  // 1. Find entry
  entry, err = f.FindEntry(ctx, filer.FullPath(r.URL.Path))
  if err == filer_pb.ErrNotFound: http.NotFound(w, r); return

  // 2. Handle inline content
  if len(entry.Content) > 0:
    w.Header().Set("Content-Type", entry.Attr.Mime)
    w.Write(entry.Content); return

  // 3. Stream chunks
  for each chunk in entry.Chunks:
    // Look up volume server addresses (cache first)
    addrs = f.lookupVolumeId(chunk.Fid().VolumeId)
    primary = addrs[0]

    // Generate read JWT
    jwt = security.GenJwtForVolumeServer(volumeGuard.ReadSigningKey, ..., chunk.FileId)

    // Proxy chunk bytes from volume server to client
    resp = http.Get("http://" + primary + "/" + chunk.FileId, Authorization: "Bearer "+jwt)
    io.Copy(w, resp.Body)
```

### Metadata Change Notification (LocalMetaLogBuffer)
```
LogBuffer.AppendEntry(entry):
  // 1. Serialize event to protobuf
  event = &filer_pb.SubscribeMetadataResponse{
    Directory:  string(entry.FullPath.Dir()),
    EventNotification: &filer_pb.EventNotification{NewEntry: entryToProto(entry)},
    TsNs:       time.Now().UnixNano(),
  }
  buf.append(event)

  // 2. Notify waiting gRPC stream goroutines
  filerServer.listenersMu.Lock()
  if len(filerServer.knownListeners) > 0:
    filerServer.listenersCond.Broadcast()
  filerServer.listenersMu.Unlock()

  // 3. Flush buffer to volume server every LogFlushIntervalSeconds
  // (persists log as chunks so subscribers can catch up after gaps)
```

### SubscribeMetadata gRPC Handler
```
SubscribeMetadata(req, stream):
  clientId = req.ClientId
  pathPrefix = req.PathPrefix
  sinceNs = req.SinceNs

  // Register this listener
  filerServer.listenersMu.Lock()
  filerServer.knownListeners[clientId] = 1
  filerServer.listenersMu.Unlock()
  defer unregister(clientId)

  for:
    // Wait for new events or timeout
    filerServer.listenersMu.Lock()
    filerServer.listenersCond.WaitWithTimeout(1 * time.Second)
    filerServer.listenersMu.Unlock()

    // Drain buffered events since sinceNs
    events = buf.ReadAfter(sinceNs)
    for each event in events:
      if !strings.HasPrefix(event.Directory, pathPrefix): continue
      stream.Send(event)
      sinceNs = event.TsNs
```

### Background Chunk Deletion
```
loopProcessingDeletion():
  for:
    task = fileIdDeletionQueue.Dequeue()
    err = deleteFromVolumeServer(task.FileId)
    if err != nil:
      if task.Retries < maxRetry:
        task.Retries++
        deletionRetryQueue.Add(task, delay=exponentialBackoff(task.Retries))
      else:
        log.Error("giving up on deletion", "fid", task.FileId)
```

### MetaAggregator
```
AggregateFromPeers():
  for each peer in knownPeers:
    go subscribeToFiler(peer)

subscribeToFiler(peer):
  for:
    // Subscribe to peer's SubscribeLocalMetadata stream
    pb.WithFilerClient(peer, func(client):
      stream = client.SubscribeLocalMetadata(ctx, &SubscribeMetadataRequest{
        PathPrefix: "/",
        ClientId:   f.UniqueFilerId,
        SinceNs:    lastProcessedNs,
      })
      for:
        event = stream.Recv()
        // Apply event to local store
        applyRemoteEvent(event)
        lastProcessedNs = event.TsNs
    )
    time.Sleep(1 * time.Second)  // reconnect backoff
```

## 8. Persistence Model

The filer server persists only through the `FilerStore` backend (see Filer Metadata Store plan).

The `LocalMetaLogBuffer` flushes metadata events to volume servers periodically (as needle writes). These log chunks allow new filer subscribers to replay history up to `LogFlushInterval` ago.

## 9. Concurrency Model

| Mechanism | Usage |
|-----------|-------|
| `listenersMu sync.Mutex` + `listenersCond *sync.Cond` | Wakes all SubscribeMetadata goroutines when new events arrive |
| `inFlightDataLimitCond *sync.Cond` | Blocks uploads when in-flight limit exceeded |
| `atomic.Int64 inFlightDataSize` | Lock-free tracking of in-flight bytes |
| `UnboundedQueue` (goroutine-safe) | Deletion queue: producers are HTTP handlers; consumer is `loopProcessingDeletion` |
| `FilerConf.mu sync.RWMutex` | Protects concurrent config reads and hot-reload writes |

## 10. Configuration

```go
type FilerOption struct {
    Masters                 []string      // required
    Host                    string
    Port                    int           `default:"8888"`
    GRPCPort                int           `default:"18888"`
    DefaultStoreDir         string        `default:"./filemetadir"`
    MaxFileSizeMB           int           `default:"4"`
    EncryptVolumeData       bool          `default:"false"`
    MaxFilenameLength       uint32        `default:"255"`
    BucketsFolder           string        `default:"/buckets"`
    DefaultReplication      string        `default:"000"`
    DefaultCollection       string        `default:""`
    LogFlushIntervalSeconds int           `default:"60"`
    ConcurrentUploadLimitMB int64         `default:"0"` // 0 = unlimited
}
```

## 11. Observability

- `obs.FilerRequestsTotal.WithLabelValues(method, status).Inc()` per HTTP request
- `obs.FilerStoreLatency.WithLabelValues(operation, backend).Observe(duration)` per store op
- Metadata subscriber count: `len(filerServer.knownListeners)` exported via `expvar`
- Deletion queue depth: `fileIdDeletionQueue.Len()` exported via `expvar`
- Bootstrap and peer sync events logged at INFO

Debug endpoints:
- `GET /healthz` — returns "OK" if server is healthy
- `GET /stats` — JSON with queue depths, subscriber counts

## 12. Testing Strategy

- **Unit tests**:
  - `TestHandleFileUploadSmall`: POST small file, assert inline storage in entry
  - `TestHandleFileUploadLarge`: POST file > maxMB, assert split into chunks
  - `TestHandleFileDownload`: create entry with chunks, GET, assert correct bytes returned
  - `TestHandleDeleteEntry`: create then DELETE, assert entry gone, deletion task queued
  - `TestHandleDirectoryList`: create 10 files, list with limit=5, resume with cursor
  - `TestMetadataSubscription`: subscribe, write entry, assert subscriber receives event
  - `TestMetaAggregatorSync`: start two filers, write to one, assert second syncs via MetaAggregator
  - `TestLoopProcessingDeletion`: enqueue deletion, mock volume server 500, assert retry
- **Integration tests**:
  - `TestFilerServerEndToEnd`: start filer + mock volume server + mock master, upload file, download, delete

## 13. Open Questions

None.
