# WebDAV Gateway

### Purpose

Expose a WebDAV interface that can operate on a local directory or on filer metadata/content through filer gRPC, enabling WebDAV clients to manage files via standard DAV methods.

### Scope

**In scope:**
- CLI startup command `blob webdav` in `goblob/command/webdav.go`.
- HTTP server wrapper in `goblob/webdav/webdav_server.go`.
- WebDAV filesystem adapter in `goblob/webdav/filesystem.go`.

**Out of scope:**
- S3 protocol behavior.
- Core filer storage implementation details.

### Primary User Flow

1. Operator starts `blob webdav` with either local `-dir` mode or remote `-filer` mode.
2. WebDAV client connects and sends DAV operations (`PROPFIND`, `GET`, `PUT`, `DELETE`, `MOVE`, etc.).
3. Gateway maps operations to local filesystem calls or filer gRPC calls.
4. Optional basic auth and hardening middleware gate requests.

### System Flow

1. `WebDAVCommand.Run` creates a `FilerFileSystem`:
   - local mode: `NewFilerFileSystem(rootDir)` delegates to `x/net/webdav.Dir`.
   - filer mode: `NewFilerFileSystemFromAddress(filerAddr,filerRoot)` opens gRPC client and ensures root path exists.
2. Server creation: `webdav.NewServer(Address(host,port), fs, username, password, harden)` wraps `xwebdav.Handler`.
3. Middleware chain:
   - optional basic auth (`wrapAuth`).
   - security hardening (`security.ApplyHardening`) from command layer.
4. Filer-backed filesystem operations:
   - `OpenFile` maps to `LookupDirectoryEntry` + `CreateEntry/UpdateEntry`.
   - `Mkdir` ensures parents and creates directory entries.
   - `RemoveAll` recursively lists and deletes entries.
   - `Rename` uses `AtomicRenameEntry`.

```
WebDAV client request
  -> webdav.Server handler
     -> [basic auth optional]
     -> FilerFileSystem method
        -> [local] os-backed webdav.Dir
        -> [remote] filer gRPC CRUD/list/rename
```

### Data Model

- `FilerFileSystem` state:
  - local delegate (`xwebdav.FileSystem`) OR
  - remote filer client (`filerAPI`) + optional `grpc.ClientConn`.
- Remote file state maps directly to `filer_pb.Entry` and `filer_pb.FuseAttributes` fields (`fileMode`, `mtime`, `fileSize`, etc.).
- Runtime write buffer model for remote writes:
  - `filerFile` keeps in-memory `data []byte`, `offset`, `dirty` flag; persists on `Close()`.

### Interfaces and Contracts

- CLI contract: `blob webdav -ip <host> -port <p> [-filer <addr> -filer.path /root] [-dir <localDir>] [-username u -password p]`.
- HTTP/WebDAV contract:
  - Endpoint listens at `<host>:<port>` with DAV semantics provided by `x/net/webdav`.
  - When `-username` is set, every request requires matching basic auth credentials.
- Filer contract (remote mode): requires filer gRPC APIs `LookupDirectoryEntry`, `ListEntries`, `CreateEntry`, `UpdateEntry`, `DeleteEntry`, `AtomicRenameEntry`.

### Dependencies

**Internal modules:**
- `goblob/webdav` for server/filesystem adapter.
- `goblob/security` for hardening middleware.
- `goblob/pb/filer_pb` for remote filesystem ops.

**External services/libraries:**
- `golang.org/x/net/webdav` for DAV protocol behavior.
- Remote mode depends on filer gRPC availability.

### Failure Modes and Edge Cases

- Invalid or unreachable `-filer` address: startup fails before serving.
- Root rename protection: renaming root path returns error (`renaming root is not supported`).
- Remote missing entry maps to `os.ErrNotExist` for compatibility.
- Remote writes buffer content in memory until file close; abrupt disconnect before `Close()` can drop pending changes.
- Basic auth configured without matching password causes `401` with `WWW-Authenticate` header.

### Observability and Debugging

- Startup log: `obs.New("webdav").Info("webdav server starting", "addr", ...)`.
- Health/metrics endpoint is not dedicated; debug via process logs and filer-side request tracing.
- Entry debug points:
  - `filesystem.go:OpenFile` for create/update behavior.
  - `filesystem.go:removeAllPath` for recursive deletions.

### Risks and Notes

- Remote file writes are whole-object rewrites (buffer then update), not streaming chunk updates.
- Local and remote modes share the same interface but differ in consistency/performance characteristics.
- No dedicated WebDAV-specific Prometheus metrics are currently emitted.

Changes:

