# Filer Metadata Service

### Purpose

Provide the filesystem namespace and metadata layer for GoBlob, including HTTP file APIs, gRPC metadata APIs, KV access, distributed locks, and chunked file indirection to volume servers.

### Scope

**In scope:**
- Filer server bootstrap and HTTP handlers in `goblob/server/filer_server.go`.
- Filer gRPC APIs in `goblob/server/filer_grpc_server.go`.
- IAM gRPC bridge hosted on filer (`goblob/server/iam_grpc_server.go`).
- Metadata model (`goblob/filer/entry.go`) and core operations (`goblob/filer/filer.go`).
- Distributed lock manager (`goblob/filer/lock_manager.go`).

**Out of scope:**
- Concrete backend persistence engines (`goblob/filer/*/*_store.go`, documented separately).
- S3 protocol translation (documented in S3 feature).

### Primary User Flow

1. Caller writes `POST /path/to/file` to filer.
2. Filer stores small payload inline or chunk-uploads large payload to volume servers.
3. Metadata entry is created under filer store and change event is appended to log buffer.
4. Reader calls `GET /path/to/file`; filer returns inline bytes or reassembles chunks by resolving volume IDs via master.
5. gRPC clients manage entries (`CreateEntry`, `ListEntries`, `LookupDirectoryEntry`, `DeleteEntry`) and optionally subscribe to metadata events.

### System Flow

1. Entry point: `NewFilerServer` initializes guards, log buffer, distributed lock manager, and registers HTTP + gRPC + IAM services.
2. Store injection: `SetStore` binds concrete `filer.FilerStore` and wires `masterUploader`/`masterResolver` when master addresses exist.
3. Upload path (`handleFileUpload`):
   - `requireFiler` verifies store readiness.
   - `filerGuard.FilerAllowed` enforces whitelist/JWT-filer key.
   - For `Content-Length > 64KB` (or unknown), `filer.Uploader.ChunkUpload` writes chunks to volume servers; metadata stores chunk list.
   - For small files, bytes are stored in `Entry.Content` inline.
   - Parent directories are created via `filer.MkdirAll`.
   - Entry persisted by `filer.CreateEntry` and `logBuffer.AppendEntry("create:<path>")`.
4. Download path (`handleFileDownload`):
   - Load entry via `filer.FindEntry`.
   - Inline content: return directly.
   - Chunked content: sort chunks by offset, resolve volume URL via master, stream each chunk `GET`.
5. gRPC path (`FilerGRPCServer`): CRUD entry operations, list/prefix list, rename, metadata subscriptions, KV APIs, distributed lock/unlock.
6. IAM path (`IAMGRPCServer`): validates and persists S3 IAM config to filer KV through `s3iam.IdentityAccessManagement`.

```
POST /<path>
  -> requireFiler + auth
  -> [small] inline Entry.Content
  -> [large] ChunkUpload -> Entry.Chunks
  -> CreateEntry + append log event

GET /<path>
  -> FindEntry
  -> [inline] return content
  -> [chunked] lookup volume per chunk -> stream chunks in order
```

### Data Model

- `filer.Entry` (`goblob/filer/entry.go`):
  - identity: `FullPath (string-like)`.
  - attrs: `Mode`, `Uid`, `Gid`, `Mtime`, `Crtime`, `Mime`, `Replication`, `Collection`, `TtlSec`, `DiskType`, `FileSize`, `INode`, `SymlinkTarget`.
  - payload forms: `Content []byte` (inline), or `Chunks []*FileChunk` with fields `FileId`, `Offset`, `Size`, `ModifiedTsNs`, `ETag`, `IsCompressed`.
  - extensibility: `Extended map[string][]byte`, hardlink fields, optional `RemoteEntry`.
- Distributed lock KV payload (`lockEntry` in `goblob/filer/lock_manager.go`):
  - `owner (string)`, `expireAtNs (int64)`, `renewToken (string)` stored under key `__lock__:<name>`.
- Log buffer events are encoded as simple bytes like `create:/path`, `delete:/path`, `rename:/old->/new` and converted for subscribe streams.

### Interfaces and Contracts

- HTTP:
  - `POST /{path...}` upload/create.
  - `GET /{path...}` download/read.
  - `DELETE /{path...}` delete.
  - `GET /healthz|/health|/ready`.
- gRPC `filer_pb.FilerService` implemented methods include:
  - metadata CRUD: `LookupDirectoryEntry`, `CreateEntry`, `UpdateEntry`, `DeleteEntry`, `AppendToEntry`, `AtomicRenameEntry`, `StreamRenameEntry`.
  - listing: `ListEntries`.
  - volume helper calls: `AssignVolume`, `LookupVolume`.
  - stream: `SubscribeMetadata`, `SubscribeLocalMetadata`.
  - KV APIs: `KvGet`, `KvPut`, `KvDelete`.
  - lock APIs: `DistributedLock`, `DistributedUnlock`.
- gRPC `iam_pb.IAMService` hosted on same server:
  - `GetS3ApiConfiguration`, `PutS3ApiConfiguration`, `GetS3ApiConfigurations`.

### Dependencies

**Internal modules:**
- `goblob/filer` core namespace abstraction and lock manager.
- `goblob/log_buffer` for event buffering/stream wakeups.
- `goblob/operation` for chunk upload and volume lookup helpers.
- `goblob/s3api/iam` for IAM validation and filer-KV persistence.

**External services/libraries:**
- Volume server HTTP for chunk retrieval and chunk upload targets.
- Master HTTP/gRPC for assign and volume location resolution.
- Behavior degrades when master or volume servers are unreachable (chunk path failures).

### Failure Modes and Edge Cases

- Store not initialized: HTTP returns `503 {"error":"filer store not initialized"}`, gRPC returns `Unavailable`.
- Auth failure: `401` on HTTP paths guarded by `FilerAllowed`.
- Large upload without configured uploader/master: `500` `no master addresses configured`.
- Chunk download with malformed `FileId` or missing volume locations returns `500`.
- Metadata stream waits on condition variable; if event format is unrecognized, event is silently skipped.
- Distributed lock renewal with wrong token returns `ErrNotLockOwner` mapped to response error.

### Observability and Debugging

- Metrics:
  - `goblob_filer_requests_total{method,status}`
  - `goblob_filer_store_latency_seconds{op}`.
- Debug entry points:
  - `handleFileUpload` for inline-vs-chunk branching.
  - `FilerGRPCServer.SubscribeMetadata` for event delivery issues.
  - `IAMGRPCServer.newIAMManager` for filer/IAM initialization failures.
- Health checks: `/healthz`, `/ready`.

### Risks and Notes

- Log-buffer events use compact string markers (`create:/x`), not strongly typed persisted records; malformed strings are dropped during subscribe conversion.
- `DeleteEntry` HTTP does not hard-fail if underlying chunk data is orphaned; metadata deletion can succeed while data cleanup is incomplete.
- Chunked read path performs sequential HTTP fetches per chunk with no parallel prefetch.

Changes:

