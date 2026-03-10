# Filer Server

## Assumptions

- The Filer is stateless in terms of data; all metadata is persisted in a pluggable backend store.
- If no `filer.toml` is found, an embedded LevelDB2 store is created automatically.
- Multiple Filer instances can run concurrently; they synchronize metadata via `MetaAggregator`.

## Code Files / Modules Referenced

- `goblob/command/filer.go` - CLI entry, `runFiler()`, `startFiler()`
- `goblob/server/filer_server.go` - `FilerServer` struct, `NewFilerServer()`
- `goblob/server/filer_server_handlers*.go` - HTTP handlers (GET, POST, DELETE, etc.)
- `goblob/server/filer_grpc_server*.go` - gRPC service handlers
- `goblob/filer/filer.go` - `Filer` struct, core metadata operations
- `goblob/filer/filerstore.go` - `FilerStore` interface
- `goblob/filer/filerstore_wrapper.go` - Store wrapper with path translation
- `goblob/filer/entry.go` - `Entry` struct (file/directory metadata)
- `goblob/filer/filechunks*.go` - Chunk management, manifest, intervals
- `goblob/filer/filer_conf.go` - Path-specific configuration (`FilerConf`)
- `goblob/filer/filer_deletion.go` - Background chunk deletion
- `goblob/filer/filer_notify.go` - Metadata change notification (log buffer)
- `goblob/filer/meta_aggregator.go` - Cross-filer metadata synchronization
- `goblob/filer/reader_at.go` - Random-access file reader
- `goblob/filer/stream.go` - Streaming file read/write
- `goblob/filer/leveldb2/` - Default embedded LevelDB2 store
- `goblob/filer/redis3/`, `postgres/`, `mysql/`, `cassandra/`, ... - 20+ backend stores
- `goblob/credential/` - Credential management for IAM gRPC

## Overview

The Filer Server provides a file-system-like metadata layer on top of the GoBlob volume storage. It maps full file paths to lists of data chunks stored on volume servers. The Filer supports POSIX-like operations (create, read, update, delete, list, rename) and exposes both HTTP and gRPC interfaces. Its metadata is stored in a pluggable backend (LevelDB, Redis, PostgreSQL, Cassandra, etc.).

## Responsibilities

- **File metadata CRUD**: Create, read, update, delete file entries with full path support
- **Directory listing**: List directory entries with pagination and prefix filtering
- **Chunk management**: Track file-to-chunk mappings; files are split into chunks stored on volumes
- **Metadata notification**: Broadcast metadata changes via a log buffer for subscribers
- **Cross-filer sync**: Aggregate metadata changes from peer filers via `MetaAggregator`
- **Background deletion**: Asynchronous chunk deletion queue with retry
- **Path-specific config**: Per-path replication, collection, TTL settings via `FilerConf`
- **Distributed lock**: `DistributedLockManager` for cross-filer coordination
- **IAM gRPC**: Credential management service co-hosted on filer gRPC port

## Architecture Role

```
+------------------------------------------------------------------+
|                         Filer Server                              |
+------------------------------------------------------------------+
|                                                                   |
|  HTTP :8888              gRPC :18888          Public :optional    |
|  (file ops + UI)         (metadata + IAM)     (read-only)         |
|       |                       |                    |              |
|  +----+------+          +-----+------+       +-----+-----+       |
|  | Handler   |          | gRPC Svc   |       | ReadOnly  |       |
|  | Mux       |          | FilerService|      | Mux       |       |
|  +----+------+          | + IAM gRPC |       +-----+-----+       |
|       |                 +-----+------+             |              |
|       +----------+------------+-----------+--------+              |
|                  |                        |                       |
|            +-----+------+          +------+------+                |
|            |   Filer     |          | MetaAggregator|             |
|            +-----+------+          +------+------+                |
|                  |                        |                       |
|            +-----+------+          (peer filer sync)              |
|            | FilerStore  |                                        |
|            | (pluggable) |                                        |
|            +-----+------+                                         |
|                  |                                                |
|    +-------------+-------------+                                  |
|    |             |             |                                   |
| LevelDB2    Redis3     PostgreSQL  ... (20+ backends)             |
+------------------------------------------------------------------+
         |                              |
         v                              v
+------------------+          +-------------------+
|  Volume Servers  |          |  Master Server    |
|  (data chunks)   |          |  (via MasterClient|
+------------------+          |   KeepConnected)  |
                              +-------------------+
```

## Component Structure Diagram

```
+---------------------------------------------------------------+
|                        FilerServer                             |
+---------------------------------------------------------------+
| option          *FilerOption                                   |
| filer           *filer.Filer           # core metadata engine  |
| filerGuard      *security.Guard        # filer JWT             |
| volumeGuard     *security.Guard        # volume JWT            |
| grpcDialOption  grpc.DialOption                                |
| secret          security.SigningKey                             |
| knownListeners  map[int32]int32        # metadata subscribers  |
| listenersCond   *sync.Cond             # notify on changes     |
| inFlightDataLimitCond *sync.Cond       # upload throttle       |
| CredentialManager *credential.CredentialManager                |
+---------------------------------------------------------------+
          |
          v
+---------------------------------------------------------------+
|                          Filer                                 |
+---------------------------------------------------------------+
| UniqueFilerId    int32                 # random instance ID    |
| Store            VirtualFilerStore     # wrapped store         |
| MasterClient     *wdclient.MasterClient # master discovery      |
| fileIdDeletionQueue *util.UnboundedQueue # async deletions     |
| DirBucketsPath   string                # "/buckets"            |
| Cipher           bool                  # encrypt volume data   |
| LocalMetaLogBuffer *log_buffer.LogBuffer # change log          |
| MetaAggregator   *MetaAggregator       # peer sync            |
| FilerConf        *FilerConf            # path-specific config  |
| Dlm              *DistributedLockManager                       |
| MaxFilenameLength uint32               # default 255           |
| DeletionRetryQueue *DeletionRetryQueue                         |
| EmptyFolderCleaner *EmptyFolderCleaner                         |
+---------------------------------------------------------------+
          |
          v
+---------------------------------------------------------------+
|                    FilerStore Interface                        |
+---------------------------------------------------------------+
| GetName() string                                               |
| Initialize(config, prefix) error                               |
| InsertEntry(ctx, *Entry) error                                 |
| UpdateEntry(ctx, *Entry) error                                 |
| FindEntry(ctx, FullPath) (*Entry, error)                       |
| DeleteEntry(ctx, FullPath) error                               |
| DeleteFolderChildren(ctx, FullPath) error                      |
| ListDirectoryEntries(ctx, dirPath, ...) (string, error)        |
| ListDirectoryPrefixedEntries(ctx, ...) (string, error)         |
| BeginTransaction / CommitTransaction / RollbackTransaction     |
| KvPut / KvGet / KvDelete                                       |
| Shutdown()                                                     |
+---------------------------------------------------------------+
```

## Control Flow

### Startup Sequence

```
runFiler() / startFiler()
    |
    +--> LoadSecurityConfiguration()
    +--> StartMetricsServer()
    |
    +--> NewFilerServer(defaultMux, readonlyMux, option)
    |       |
    |       +--> Load JWT signing keys from viper
    |       +--> option.Masters.RefreshBySrvIfAvailable()
    |       +--> LoadConfiguration("filer")
    |       |    (if no filer.toml: default to LevelDB2)
    |       +--> LoadConfiguration("notification")
    |       |
    |       +--> filer.NewFiler(masters, grpcDialOption, ...)
    |       |       +--> NewMasterClient(...)
    |       |       +--> NewUnboundedQueue()  (deletion queue)
    |       |       +--> NewLogBuffer("local", LogFlushInterval)
    |       |       +--> NewDistributedLockManager()
    |       |       +--> go loopProcessingDeletion()
    |       |
    |       +--> NewGuard() for filer + volume
    |       +--> checkWithMaster()  (get metrics config)
    |       +--> go MasterClient.KeepConnectedToMaster()
    |       |
    |       +--> filer.LoadConfiguration(viper)
    |       |    (initialize FilerStore backend)
    |       |
    |       +--> Register HTTP routes:
    |       |    /healthz, / (filerHandler)
    |       |
    |       +--> MaybeBootstrapFromOnePeer()
    |       +--> AggregateFromPeers()  (start MetaAggregator)
    |       +--> LoadFilerConf()
    |       +--> Initialize CredentialManager
    |
    +--> Start gRPC server:
    |    filer_pb.RegisterFilerServiceServer
    |    iam_pb.RegisterIAMServiceServer
    |
    +--> Start unix socket listener (Linux/Mac)
    +--> Start HTTP server (with optional TLS/mTLS)
    +--> SIGHUP handler: reload security config / ctx.Done(): graceful shutdown
```

## Runtime Sequence Flow

### File Upload (POST)

```
Client                 FilerServer              Filer              VolumeServer
  |                        |                      |                     |
  |-- POST /path/to/file ->|                      |                     |
  |   (multipart body)     |                      |                     |
  |                        |-- filerGuard check -->|                     |
  |                        |                      |                     |
  |                        |-- assign file ID --->| (via MasterClient)  |
  |                        |                      |-- /dir/assign ----->|
  |                        |                      |<-- {fid, url} ------|
  |                        |                      |                     |
  |                        |-- upload chunks ---->|                     |
  |                        |                      |-- PUT to volume --->|
  |                        |                      |<-- 201 ------------|
  |                        |                      |                     |
  |                        |-- create Entry ------>|                     |
  |                        |   (path, chunks,      |                     |
  |                        |    metadata)           |                     |
  |                        |                      |--> Store.InsertEntry()
  |                        |                      |--> logFlush (notify)
  |                        |                      |                     |
  |<-- 201 Created --------|                      |                     |
```

### File Download (GET)

```
Client                 FilerServer              Filer              VolumeServer
  |                        |                      |                     |
  |-- GET /path/to/file -->|                      |                     |
  |                        |-- FindEntry(path) -->|                     |
  |                        |                      |--> Store.FindEntry()
  |                        |<-- Entry (chunks) ---|                     |
  |                        |                      |                     |
  |                        |-- for each chunk:    |                     |
  |                        |   lookup volume loc  |                     |
  |                        |   via vidMap cache   |                     |
  |                        |                      |                     |
  |                        |-- stream chunks ---->|-- GET from vol --->|
  |                        |                      |<-- chunk data -----|
  |                        |                      |                     |
  |<-- 200 + file data ----|                      |                     |
```

### Metadata Change Notification

```
FilerServer                 Filer               Subscribers
    |                        |                      |
    | (on InsertEntry)       |                      |
    |                        |-- LogBuffer.append() |
    |                        |                      |
    |                        |-- if listenersWaits>0|
    |                        |   listenersCond.     |
    |                        |   Broadcast()        |
    |                        |                      |
    |                        |                  +---+---+
    |                        |                  | gRPC  |
    |                        |                  |stream |
    |                        |                  |client |
    |                        |                  +-------+
    |                        |
    |                        |-- LogFlushInterval (1min)
    |                        |   flush to volume as
    |                        |   meta log chunks
```

## Data Flow Diagram

```
+--------+        +-----------+       +-----------+
| Entry  | -----> | FilerStore| ----> | Backend   |
| (proto)|        | Wrapper   |       | (LevelDB/ |
+--------+        |           |       |  Redis/   |
| FullPath        | translate |       |  PG/...)  |
| Attr{}          | path      |       +-----------+
| Chunks[]        | prefix    |
| Extended        +-----------+
| HardLink        
| Remote          
+--------+        

Entry.Chunks[] --> []*FileChunk
    |
    +--> ChunkId (fid on volume server)
    +--> Offset, Size
    +--> ModifiedTsNs
    +--> ETag, CipherKey
    +--> IsCompressed
```

## Dependencies

| Dependency | Purpose |
|---|---|
| `goblob/filer` | Core Filer, FilerStore, Entry, Chunks |
| `goblob/security` | JWT guard, TLS |
| `goblob/pb/filer_pb` | gRPC service definition |
| `goblob/pb/iam_pb` | IAM gRPC service |
| `goblob/wdclient` | Master client for volume lookups |
| `goblob/credential` | Credential manager (filer_etc, memory, postgres) |
| `goblob/notification` | External notification plugins |
| `goblob/filer/empty_folder_cleanup` | Background empty folder cleaner |
| 20+ store backends | LevelDB2, Redis3, PostgreSQL, MySQL, Cassandra, etc. |

## Error Handling

- **Store not found**: If `filer.toml` missing, defaults to embedded LevelDB2
- **Master unreachable**: `checkWithMaster()` retries every 7 seconds until connected
- **Entry not found**: Returns `filer_pb.ErrNotFound` (mapped to HTTP 404)
- **Deletion failures**: Retried via `DeletionRetryQueue` with bounded retries
- **Concurrent upload limit**: Blocks on `inFlightDataLimitCond` with configurable limit

## Transactions

- `FilerStore` interface supports `BeginTransaction`, `CommitTransaction`, `RollbackTransaction`
- Backend implementations decide whether to use real DB transactions (e.g., PostgreSQL) or no-ops (e.g., Redis)

## Async / Background Behavior

| Goroutine | Purpose |
|---|---|
| `loopProcessingDeletion()` | Drains `fileIdDeletionQueue`, deletes chunks from volumes |
| `MetaAggregator` | Subscribes to peer filers, replays metadata changes |
| `LocalMetaLogBuffer` | Buffers metadata changes, flushes to volume every `LogFlushInterval` (1 min) |
| `MasterClient.KeepConnectedToMaster()` | Master discovery and leader tracking |
| `EmptyFolderCleaner` | Periodic cleanup of empty directories |
| `LoopPushingMetric()` | Prometheus metrics push |

## Security Considerations

- **Filer JWT**: Separate signing keys (`jwt.filer_signing.key`) from volume JWT
- **Volume JWT**: Filer generates volume access tokens for chunk reads/writes
- **TLS**: HTTPS with auto-refreshing certificates (`pemfile.NewProvider`)
- **mTLS**: Optional client certificate verification (`https.filer.ca`)
- **Unix socket**: Local socket for co-located gateways (S3) to bypass network
- **CORS**: Configurable allowed origins for browser access

## Configuration

- **`filer.toml`**: Backend store configuration (searched in `.`, `$HOME/.goblob/`, etc.)
- **Key CLI flags**: `-master`, `-port`, `-defaultStoreDir`, `-maxMB`, `-encryptVolumeData`
- **Path-specific config**: `FilerConf` supports per-path replication, collection, TTL, disk type
- **Notification config**: `notification.toml` for external event sinks
- **Viper defaults**: `filer.options.buckets_folder=/buckets`, `max_file_name_length=255`

## Edge Cases

- **Bootstrap from peer**: Fresh filer can bootstrap metadata from an existing peer via `MaybeBootstrapFromOnePeer()`
- **Large files**: Files larger than `maxMB` (default 4MB) are split into multiple chunks
- **Save to filer limit**: Small files below `saveToFilerLimit` can be inlined in the metadata store
- **Filename length**: Enforced via `MaxFilenameLength` (default 255); configurable
- **Recursive delete**: Controlled by `filer.options.recursive_delete` in config
- **Bucket awareness**: Some stores implement `BucketAware` interface for per-bucket optimizations
