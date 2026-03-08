# Feature: Client SDK

## 1. Purpose

The Client SDK provides Go library functions for client programs to interact with a GoBlob cluster. It encapsulates the full write and read paths: assigning a file ID from the master, uploading blob data to a volume server, downloading blob data, and deleting blobs. It also provides a volume location cache to amortize repeated lookup RPCs and handles retries transparently.

The SDK is the building block used by the filer server (for chunk uploads), the S3 gateway (for multipart part uploads), and end-user Go programs that want direct blob access without going through the filer.

## 2. Responsibilities

- **File ID assignment**: POST `/dir/assign` to master with collection, replication, TTL, and data center hints
- **Blob upload**: PUT `http://{volumeUrl}/{fid}` with request body; attach JWT if configured
- **Blob download**: GET `http://{volumeUrl}/{fid}`; validate ETag/checksum if present
- **Blob delete**: DELETE `http://{volumeUrl}/{fid}` with JWT
- **Volume location lookup**: GET `/dir/lookup?volumeId=N` from master; cache results per VolumeId
- **Retry logic**: Retry transient errors (connection refused, 503) with exponential backoff
- **JWT attachment**: Include `Authorization: Bearer <jwt>` on volume server requests when a signing key is configured
- **Multipart support**: Helper to split a large reader into chunks, upload each chunk, and return the `[]FileChunk` slice for filer entry creation

## 3. Non-Responsibilities

- Does not manage filer metadata (that is the filer server's job)
- Does not implement S3 protocol (that is the S3 gateway's job)
- Does not run any server-side logic or background goroutines beyond the cache TTL reaper
- Does not handle compaction (that is the storage engine's job)
- Does not manage volume growth (that is the master's job)

## 4. Architecture Design

```
+--------------------------------------------------------------+
|                        Client SDK                             |
+--------------------------------------------------------------+
|                                                               |
|  Assigner                  Uploader / Downloader             |
|  POST /dir/assign          PUT/GET/DELETE /{fid}             |
|       |                          |                           |
|  +----+----+             +-------+-------+                   |
|  |  Master |             | VolumeLocation|                   |
|  |  Client |             |     Cache     |                   |
|  | (HTTP)  |             | (VolumeId ->  |                   |
|  +---------+             |  []ServerAddr)|                   |
|                          +-------+-------+                   |
|                                  |                           |
|                          Volume Server HTTP                  |
|                          (direct TCP connection)             |
|                                                              |
|  ChunkUploader                                               |
|  (splits reader, calls Uploader per chunk)                   |
+--------------------------------------------------------------+
```

### Request Flow: Write

```
client.AssignAndUpload(ctx, r io.Reader, opt UploadOption):
  1. POST master /dir/assign -> {fid, url, jwt}
  2. PUT http://{url}/{fid} with r as body, Authorization: Bearer {jwt}
  3. return AssignedFileId{Fid, Size, ETag}
```

### Request Flow: Read

```
client.Download(ctx, fid string, w io.Writer):
  1. Parse VolumeId from fid
  2. Cache lookup for VolumeId -> [{addr, publicUrl}]
     If miss: GET master /dir/lookup?volumeId=N -> cache result with TTL
  3. GET http://{addr}/{fid}
  4. io.Copy(w, resp.Body)
```

## 5. Core Data Structures (Go)

```go
package operation

import (
    "sync"
    "time"
    "goblob/core/types"
)

// AssignedFileId is the result of a successful file ID assignment from the master.
type AssignedFileId struct {
    // Fid is the assigned file ID in "vid,needleIdHexCookieHex" format.
    Fid string

    // Url is the volume server HTTP address for the upload (internal address).
    Url string

    // PublicUrl is the publicly accessible address (may differ from Url).
    PublicUrl string

    // Count is the number of file IDs reserved (always 1 for single uploads).
    Count uint64

    // Auth is the JWT for writing to the assigned volume server.
    Auth string
}

// UploadOption controls the parameters of a file ID assignment request.
type UploadOption struct {
    // Master is the master HTTP address (e.g., "localhost:9333").
    Master string

    // Collection groups volumes for isolation.
    Collection string

    // Replication is the replica placement string (e.g., "001").
    Replication string

    // Ttl is the time-to-live string (e.g., "3d", "1w", "").
    Ttl string

    // DataCenter constrains assignment to a specific data center.
    DataCenter string

    // Rack constrains assignment to a specific rack.
    Rack string

    // DiskType selects a specific disk tier (e.g., "ssd", "hdd", "").
    DiskType string

    // Count is the number of file IDs to reserve in one assignment call.
    // Default 1. Values > 1 are for batch pre-allocation.
    Count uint64

    // Replication Preallocate bytes on the selected volume (prevents fragmentation).
    Preallocate int64
}

// UploadResult is returned after a successful blob upload.
type UploadResult struct {
    // Name is the filename extracted from the upload (if multipart form).
    Name string

    // Size is the number of bytes written to the volume server.
    Size uint32

    // ETag is the content MD5 or CRC hash returned by the volume server.
    ETag string

    // Mime is the detected or declared MIME type.
    Mime string

    // Fid is the file ID of the stored blob.
    Fid string

    // Error is non-empty if the upload was accepted but the server reported a logical error.
    Error string
}

// VolumeLocationCache caches master lookup results to reduce RPC load.
type VolumeLocationCache struct {
    // locations maps VolumeId to its cached server list.
    locations map[types.VolumeId]*locationEntry
    mu        sync.RWMutex

    // ttl is how long a cache entry is valid before re-querying the master.
    ttl time.Duration
}

type locationEntry struct {
    servers   []VolumeLocation
    expiresAt time.Time
}

// VolumeLocation is one server that holds a given volume.
type VolumeLocation struct {
    Url       string // internal HTTP address
    PublicUrl string // external HTTP address
}

// LookupResult is the response from the master /dir/lookup endpoint.
type LookupResult struct {
    VolumeOrFileId string           `json:"volumeOrFileId"`
    Locations      []VolumeLocation `json:"locations"`
    Error          string           `json:"error,omitempty"`
}

// ChunkUploadResult holds the result of uploading one chunk of a large file.
type ChunkUploadResult struct {
    // FileChunk contains the fid, offset, size, and ETag for filer metadata.
    FileId       string
    Offset       int64
    Size         int64
    ModifiedTsNs int64
    ETag         string
}

// Assigner assigns file IDs from the master.
type Assigner struct {
    masterAddrs []string
    httpClient  *http.Client
}

// Uploader uploads blobs to volume servers.
type Uploader struct {
    locationCache *VolumeLocationCache
    signingKey    []byte // empty = no JWT
    httpClient    *http.Client
}
```

## 6. Public Interfaces

```go
package operation

// Assign requests a new file ID from the master.
// Returns the assignment including the volume server URL and a write JWT.
func Assign(ctx context.Context, masterAddr string, opt *UploadOption) (*AssignedFileId, error)

// Upload sends blob data to a volume server using a pre-assigned file ID.
// It attaches the JWT from the assignment if non-empty.
func Upload(ctx context.Context, uploadUrl string, filename string, data io.Reader, size int64, isGzip bool, mimeType string, pairMap map[string]string, jwt string) (*UploadResult, error)

// Delete removes a blob from its volume server.
func Delete(ctx context.Context, url string, jwt string) error

// LookupVolumeId returns the server addresses for a given volume ID.
// Results are cached for the VolumeLocationCache's TTL duration.
func LookupVolumeId(ctx context.Context, masterAddr string, vid types.VolumeId, cache *VolumeLocationCache) ([]VolumeLocation, error)

// UploadWithRetry wraps Upload with exponential backoff retry on transient errors.
// Retries up to maxRetries times with initial backoff of 500ms, doubling each attempt.
func UploadWithRetry(ctx context.Context, uploadUrl string, filename string, data []byte, mimeType string, jwt string, maxRetries int) (*UploadResult, error)

// ChunkUpload splits data from reader into chunks of chunkSizeBytes, uploads each
// chunk to the cluster, and returns the list of ChunkUploadResults for filer metadata.
func ChunkUpload(ctx context.Context, masterAddr string, reader io.Reader, totalSize int64, chunkSizeBytes int64, opt *UploadOption) ([]*ChunkUploadResult, error)

// NewVolumeLocationCache creates a cache with the given TTL.
// Pass ttl=0 to use the default of 10 minutes.
func NewVolumeLocationCache(ttl time.Duration) *VolumeLocationCache

// Get returns cached locations for a VolumeId, or (nil, false) if not cached or expired.
func (c *VolumeLocationCache) Get(vid types.VolumeId) ([]VolumeLocation, bool)

// Set stores locations for a VolumeId.
func (c *VolumeLocationCache) Set(vid types.VolumeId, locs []VolumeLocation)

// Invalidate removes a VolumeId from the cache (called on 404 responses).
func (c *VolumeLocationCache) Invalidate(vid types.VolumeId)
```

## 7. Internal Algorithms

### Assign
```
Assign(ctx, masterAddr, opt):
  url = "http://" + masterAddr + "/dir/assign"
  params = buildQueryParams(opt)
  resp, err = httpClient.PostForm(url + "?" + params, nil)
  if err: return nil, wrapRetryable(err)

  if resp.StatusCode == 503:
    return nil, ErrNoWritableVolumes  // client should retry after delay

  if resp.StatusCode != 200:
    return nil, fmt.Errorf("assign failed: HTTP %d", resp.StatusCode)

  result = &AssignedFileId{}
  json.NewDecoder(resp.Body).Decode(result)
  if result.Error != "":
    return nil, errors.New(result.Error)
  return result, nil
```

### Upload
```
Upload(ctx, uploadUrl, filename, data, size, isGzip, mimeType, pairMap, jwt):
  body, contentType = buildMultipartBody(filename, data, mimeType, pairMap)
  // For raw binary uploads, send body directly with Content-Type

  req = http.NewRequestWithContext(ctx, "PUT", uploadUrl, body)
  req.Header.Set("Content-Type", contentType)
  req.ContentLength = size
  if isGzip:
    req.Header.Set("Content-Encoding", "gzip")
  if jwt != "":
    req.Header.Set("Authorization", "Bearer " + jwt)

  resp, err = httpClient.Do(req)
  if err: return nil, err
  if resp.StatusCode != 201:
    return nil, fmt.Errorf("upload failed: HTTP %d", resp.StatusCode)

  result = &UploadResult{}
  json.NewDecoder(resp.Body).Decode(result)
  result.Fid = parseFidFromUrl(uploadUrl)
  return result, nil
```

### LookupVolumeId
```
LookupVolumeId(ctx, masterAddr, vid, cache):
  // Check cache first
  if locs, ok = cache.Get(vid); ok:
    return locs, nil

  // Query master
  url = fmt.Sprintf("http://%s/dir/lookup?volumeId=%d", masterAddr, vid)
  resp, err = httpClient.Get(url)
  if err: return nil, err

  result = &LookupResult{}
  json.NewDecoder(resp.Body).Decode(result)
  if result.Error != "": return nil, errors.New(result.Error)
  if len(result.Locations) == 0:
    return nil, ErrVolumeNotFound

  cache.Set(vid, result.Locations)
  return result.Locations, nil
```

### UploadWithRetry
```
UploadWithRetry(ctx, uploadUrl, filename, data, mimeType, jwt, maxRetries):
  backoff = 500 * time.Millisecond
  for attempt = 0; attempt <= maxRetries; attempt++:
    result, err = Upload(ctx, uploadUrl, filename, bytes.NewReader(data), int64(len(data)), false, mimeType, nil, jwt)
    if err == nil: return result, nil

    if !isRetryable(err): return nil, err
    if attempt == maxRetries: return nil, err

    select:
    case <-time.After(backoff):
    case <-ctx.Done():
      return nil, ctx.Err()
    backoff *= 2
    if backoff > 30*time.Second: backoff = 30*time.Second
```

### ChunkUpload
```
ChunkUpload(ctx, masterAddr, reader, totalSize, chunkSizeBytes, opt):
  chunks = []*ChunkUploadResult{}
  offset = int64(0)
  buf = make([]byte, chunkSizeBytes)

  for:
    n, err = io.ReadFull(reader, buf)
    if n == 0 and err == io.EOF: break

    chunkData = buf[:n]

    // Assign file ID for this chunk
    assigned, err = Assign(ctx, masterAddr, opt)
    if err: return nil, fmt.Errorf("assign chunk at offset %d: %w", offset, err)

    // Upload chunk
    result, err = UploadWithRetry(ctx, "http://"+assigned.Url+"/"+assigned.Fid,
        "", chunkData, "application/octet-stream", assigned.Auth, 3)
    if err: return nil, fmt.Errorf("upload chunk at offset %d: %w", offset, err)

    chunks = append(chunks, &ChunkUploadResult{
        FileId:       assigned.Fid,
        Offset:       offset,
        Size:         int64(n),
        ModifiedTsNs: time.Now().UnixNano(),
        ETag:         result.ETag,
    })
    offset += int64(n)

    if err == io.ErrUnexpectedEOF or err == io.EOF: break

  return chunks, nil
```

### VolumeLocationCache TTL Reaper
```
// The cache does lazy expiry: entries are checked on Get, not proactively reaped.
// This avoids a background goroutine and is sufficient for the cache's small size.

VolumeLocationCache.Get(vid):
  c.mu.RLock()
  entry = c.locations[vid]
  c.mu.RUnlock()

  if entry == nil: return nil, false
  if time.Now().After(entry.expiresAt):
    c.Invalidate(vid)
    return nil, false
  return entry.servers, true
```

### isRetryable
```
isRetryable(err):
  // Retry on: connection refused, temporary network errors, HTTP 503
  var netErr net.Error
  if errors.As(err, &netErr) and netErr.Timeout(): return true
  if strings.Contains(err.Error(), "connection refused"): return true
  if strings.Contains(err.Error(), "503"): return true
  return false
```

## 8. Persistence Model

The Client SDK is stateless. The `VolumeLocationCache` lives in memory and is discarded on process exit. There is no disk persistence in the SDK layer.

The cache size is bounded by the number of distinct VolumeIds accessed, which is typically small (thousands, not millions) for a single client process.

## 9. Concurrency Model

| Mechanism | Usage |
|-----------|-------|
| `VolumeLocationCache.mu sync.RWMutex` | Concurrent reads from multiple goroutines; exclusive on Set/Invalidate |
| `http.Client` (shared) | Go's `http.Client` is safe for concurrent use; connection pool managed internally |
| `ChunkUpload` chunks | Uploaded sequentially by default; callers may parallelize by calling `Assign`+`Upload` concurrently |
| Context cancellation | All HTTP calls respect `ctx.Done()`; in-flight requests are aborted on cancel |

## 10. Configuration

```go
// UploadOption (per-call configuration, see §5)
// No global configuration struct — all options are passed per call.

// Sensible defaults applied internally:
const (
    defaultVolumeLocationCacheTTL = 10 * time.Minute
    defaultHTTPTimeout            = 30 * time.Second
    defaultMaxRetries             = 3
    defaultInitialBackoff         = 500 * time.Millisecond
    defaultMaxBackoff             = 30 * time.Second
    defaultChunkSizeMB            = 4 // 4 MB per chunk
)
```

The HTTP client used internally is constructed once with a 30-second timeout and a connection pool. Callers may inject a custom `*http.Client` for testing or to configure TLS certificates.

## 11. Observability

- `obs.ClientAssignLatency.Observe(duration)` per `/dir/assign` call
- `obs.ClientUploadBytes.Add(bytes)` per blob uploaded
- `obs.ClientUploadLatency.Observe(duration)` per blob upload
- `obs.ClientDownloadBytes.Add(bytes)` per blob downloaded
- `obs.ClientRetryCount.WithLabelValues(operation).Add(n)` per retry
- `obs.ClientCacheHit.WithLabelValues("location").Inc()` / `obs.ClientCacheMiss...` per lookup
- Assign errors logged at WARN with master address
- Upload errors logged at WARN with volume URL and file ID
- All retries logged at DEBUG with attempt number and backoff duration

## 12. Testing Strategy

- **Unit tests**:
  - `TestAssignSuccess`: mock master returns valid assignment JSON, assert fields parsed correctly
  - `TestAssignMasterUnavailable`: mock master returns 503, assert `ErrNoWritableVolumes`
  - `TestUploadSuccess`: mock volume server returns 201, assert `UploadResult.Size` correct
  - `TestUploadRetry`: mock returns 503 twice then 201, assert success on third attempt
  - `TestUploadRetryExhausted`: mock always returns 503, assert error after `maxRetries`
  - `TestUploadContextCancelled`: cancel context mid-retry, assert `ctx.Err()` returned
  - `TestLookupVolumeIdCacheHit`: populate cache, assert no HTTP call on second lookup
  - `TestLookupVolumeIdCacheExpiry`: populate cache with past TTL, assert re-fetch on Get
  - `TestLookupVolumeIdInvalidate`: invalidate entry, assert next Get fetches from master
  - `TestChunkUploadSmall`: reader smaller than chunkSize, assert single chunk produced
  - `TestChunkUploadLarge`: reader 3× chunkSize, assert 3 chunks with correct offsets
  - `TestChunkUploadMidFailure`: second chunk upload fails, assert error returned and first chunk not leaked (caller is responsible for cleanup)
  - `TestDeleteSuccess`: mock volume server returns 200, assert no error
  - `TestIsRetryable`: table-driven test for retryable vs non-retryable errors

- **Integration tests**:
  - `TestClientEndToEnd`: start master + volume server in-process, assign → upload → lookup → download, assert downloaded bytes match uploaded bytes
  - `TestClientChunkEndToEnd`: upload 12 MB file in 4 MB chunks, assert 3 chunks returned with correct offsets

## 13. Open Questions

None.
