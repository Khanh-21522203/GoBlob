# Go Client Operations API

### Purpose

Provide a Go package-level client API for assign/upload/lookup/delete/chunk-upload workflows against GoBlob master and volume endpoints.

### Scope

**In scope:**
- HTTP client helpers in `goblob/operation/*.go`.
- Shared retry/backoff and connection pooling behavior.
- Upload option and response models.

**Out of scope:**
- Server-side validation and persistence logic.
- Admin shell orchestration commands.

### Primary User Flow

1. Caller requests a file assignment from master (`operation.Assign`).
2. Caller uploads payload to assigned volume URL (`operation.Upload` or `UploadWithRetry`).
3. Caller optionally looks up volume locations (`operation.LookupVolumeId`) and caches results.
4. For large streams, caller uses `operation.ChunkUpload` to parallelize assign+upload chunk workflow.
5. Caller can delete by URL using `operation.Delete`.

### System Flow

1. `Assign` builds `/dir/assign` URL with query params from `UploadOption` and executes `POST`.
2. `Upload` builds multipart form body (`file` field + optional custom fields), sends `PUT` to volume URL, decodes JSON result.
3. `LookupVolumeId` sends `GET /dir/lookup?volumeId=<id>` and can store result in `VolumeLocationCache` with TTL.
4. `ChunkUpload`:
   - reads source stream into chunk jobs,
   - each worker calls `Assign`, then `UploadWithRetry` to assigned URL,
   - results are sorted by chunk index and returned.
5. `UploadWithRetry` applies exponential backoff for retryable network/HTTP status errors.

```
Assign -> fid/url/auth
      -> Upload(url/fid, payload)
      -> result (etag,size)

ChunkUpload(reader)
  -> jobs -> workers
     -> Assign + UploadWithRetry per chunk
  -> ordered []ChunkUploadResult
```

### Data Model

- `UploadOption` fields:
  - `Master`, `Collection`, `Replication`, `Ttl`, `DataCenter`, `Rack`, `DiskType`, `Count`, `Preallocate`.
- `AssignedFileId` JSON model:
  - `fid`, `url`, `publicUrl`, `count`, `auth`, optional `error`.
  - backward-compatible unmarshal supports `public_url`.
- `UploadResult`:
  - `name`, `size`, `eTag`, `mime`, `fid`, optional `error`.
- `ChunkUploadResult`:
  - `FileId`, `Offset`, `Size`, `ModifiedTsNs`, `ETag`.
- `VolumeLocationCache`:
  - map `VolumeId -> locations[] + expiresAt` with default TTL 10 minutes.

### Interfaces and Contracts

- Exported functions:
  - `Assign(ctx, masterAddr, opt) (*AssignedFileId, error)`.
  - `Upload(ctx, uploadURL, filename, data, size, isGzip, mimeType, pairMap, jwt) (*UploadResult, error)`.
  - `LookupVolumeId(ctx, masterAddr, vid, cache...) ([]VolumeLocation, error)`.
  - `UploadWithRetry(ctx, uploadURL, filename, data, mimeType, jwt, maxRetries)`.
  - `ChunkUpload(ctx, masterAddr, reader, totalSize, chunkSizeBytes, opt)`.
  - `Delete(ctx, url, jwt)`.
- Error contracts:
  - `ErrNoWritableVolumes`, `ErrVolumeNotFound` sentinel errors.
  - non-2xx responses return `*HTTPStatusError{Op,URL,StatusCode,Body}`.

### Dependencies

**Internal modules:**
- `goblob/core/types` for typed volume IDs.
- `goblob/operation` subfiles for option models and cache.

**External services/libraries:**
- HTTP endpoints for master and volume servers.
- Depends on caller-provided reachable addresses and valid auth tokens.

### Failure Modes and Edge Cases

- Missing master address returns local validation error in `Assign`.
- Assign `503` maps to `ErrNoWritableVolumes`.
- Upload/lookup/delete non-2xx responses return `HTTPStatusError` with truncated response body.
- `ChunkUpload` stops on first worker error and returns that error.
- Retry logic only retries retryable network errors and specific status codes (`429`, `500`, `502`, `503`, `504`).

### Observability and Debugging

- Package itself does not emit metrics/logging.
- Debug through returned errors and `HTTPStatusError` contents.
- Primary debug points:
  - `assign.go:Assign` for query construction.
  - `upload.go:Upload` for multipart payload generation.
  - `client.go:ChunkUpload` for worker orchestration.

### Risks and Notes

- `Upload` builds multipart payload fully in memory before sending.
- `ChunkUpload` can produce partial uploaded chunks before returning an error.
- Shared HTTP client is package-global; caller cannot directly inject custom transport per call without modifying package code.

Changes:

