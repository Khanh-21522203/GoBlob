# WebDAV Interface

Phase 8 adds a WebDAV server package and command.

## What is implemented

- `goblob/webdav/filesystem.go`: `FilerFileSystem` WebDAV filesystem adapter.
- `goblob/webdav/webdav_server.go`: WebDAV HTTP server with optional Basic auth.
- CLI command:
  - Local mode: `blob webdav -ip 127.0.0.1 -port 4333 -dir ./tmp/webdav`
  - Filer-backed mode: `blob webdav -ip 127.0.0.1 -port 4333 -filer 127.0.0.1:8888 -filer.path /webdav`

## Basic auth

Set `-username` and `-password` to require Basic auth.

```bash
blob webdav -ip 127.0.0.1 -port 4333 -dir ./tmp/webdav -username admin -password secret
```

## Backend Modes

- `-dir`: local filesystem adapter (default)
- `-filer` + `-filer.path`: filer gRPC-backed adapter
