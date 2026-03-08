# Feature: S3 API Gateway

## 1. Purpose

The S3 API Gateway provides an Amazon S3-compatible REST interface to GoBlob. It translates S3 API calls into Filer Server operations, mapping S3 buckets to directories under `/buckets` and S3 objects to files. Clients using any S3-compatible SDK (AWS SDK, boto3, s3cmd, rclone, etc.) can interact with GoBlob without modification.

The gateway is stateless: it holds no persistent state itself. All state is in the Filer and Volume Servers.

## 2. Responsibilities

- **S3 REST API**: Implement bucket CRUD, object CRUD, list objects (v1/v2), multipart upload, object copy
- **Authentication**: AWS Signature V4, Signature V2, presigned URLs, anonymous access
- **Authorization**: IAM identity evaluation, bucket policies, ACLs
- **Server-Side Encryption**: SSE-S3 (server-managed keys), SSE-C (customer keys), SSE-KMS
- **Object Versioning**: Per-bucket versioning with version IDs
- **Object Lock**: Governance and compliance mode retention, legal hold
- **Multipart Upload**: Initiate, upload parts, complete, abort
- **Circuit Breaker**: Per-bucket request rate limiting and concurrent upload throttling
- **Bucket Config Cache**: Cache per-bucket settings with event-driven invalidation
- **Embedded IAM API**: Optional IAM management API (`/iam/`) on the same port
- **CORS**: Per-bucket CORS headers
- **Bucket Policies**: JSON policy document evaluation

## 3. Non-Responsibilities

- Does not store any data (all writes go through Filer → Volume Servers)
- Does not run Raft or maintain cluster state
- Does not implement the full AWS S3 API surface (e.g., replication, analytics, Object Lambda)
- Does not implement the IAM control plane (user/policy creation is via embedded IAM API, not S3)
- **Apache Iceberg REST Catalog** (`s3api/iceberg/`): out of scope for GoBlob v1; niche analytics table format integration, can be added as a separate extension later
- **S3 Tables** (`s3api/s3tables/`): out of scope for GoBlob v1; AWS-specific managed tables feature, not part of the standard S3 API that most clients use

## 4. Architecture Design

```
+--------------------------------------------------------------+
|                      S3 API Gateway                           |
+--------------------------------------------------------------+
|                                                               |
|  HTTP :8333               gRPC :18333                         |
|  (S3 REST API)            (IAM cache sync)                   |
|       |                         |                            |
|  gorilla/mux Router       S3IamCache gRPC service            |
|  /{bucket}                (sync IAM config to peers)         |
|  /{bucket}/{key+}                                            |
|  /                                                           |
|       |                                                      |
|  +----+------+  +----------+  +---------+  +-------+         |
|  | Auth      |  |Circuit   |  |Bucket   |  |IAM    |         |
|  | Middleware|  |Breaker   |  |Config   |  |Embed  |         |
|  | (SigV4/V2)|  |(rate lim)|  |Cache    |  |(opt)  |         |
|  +----+------+  +----------+  +---------+  +-------+         |
|       |                                                      |
|  +----+----+  +--------+  +----------+                        |
|  |Bucket   |  |Object  |  |Multipart |                        |
|  |Handlers |  |Handlers|  |Handler   |                        |
|  +----+----+  +--------+  +----------+                        |
|       |                                                      |
|  +----+---------+                                            |
|  | FilerClient  |  (HA multi-filer, failover)               |
|  +--------------+                                            |
+--------------------------------------------------------------+
        |
    Filer Server(s)
        |
    Volume Server(s)
```

### Bucket/Object Mapping
```
S3 namespace             Filer namespace
s3://my-bucket/          /buckets/my-bucket/
s3://my-bucket/dir/obj   /buckets/my-bucket/dir/obj
s3://my-bucket/?uploads  /buckets/my-bucket/.uploads/
```

## 5. Core Data Structures (Go)

```go
package s3gateway

import (
    "sync"
    "goblob/filer"
    "goblob/security"
)

// S3ApiServer is the top-level struct for the S3 gateway.
type S3ApiServer struct {
    option             *S3ApiServerOption

    // iam handles authentication and authorization.
    iam                *IdentityAccessManagement

    // cb is the circuit breaker for rate limiting.
    cb                 *CircuitBreaker

    filerGuard         *security.Guard
    filerClient        *FilerClient

    // bucketConfigCache caches per-bucket settings with 60-minute TTL.
    bucketConfigCache  *BucketConfigCache

    policyEngine       *BucketPolicyEngine

    // embeddedIam is the optional IAM management API.
    embeddedIam        *EmbeddedIamApi

    cipher             bool

    inFlightDataSize   int64  // atomic
    inFlightDataLimitCond *sync.Cond

    logger *slog.Logger
}

// S3ApiServerOption holds S3 gateway configuration.
type S3ApiServerOption struct {
    Filers           []types.ServerAddress
    Masters          []types.ServerAddress
    Port             int
    DomainName       string  // for virtual-hosted style: bucket.domain.com
    BucketsPath      string  // default: "/buckets"
    AllowEmptyFolder bool
    EnableIam        bool
    IamConfigFile    string
    ExternalUrl      string  // for SigV4 verification behind reverse proxy
    Cipher           bool
    ConcurrentUploadLimitMB  int64
    ConcurrentFileUploadLimit int
    SecurityConfig   *security.SecurityConfig
}

// Identity represents an IAM identity (access key + secret + policies).
type Identity struct {
    Name        string
    AccessKeys  []AccessKey
    Actions     []string // S3 actions this identity can perform
    Credentials []S3Credential
}

type AccessKey struct {
    AccessKeyId     string
    SecretAccessKey string
}

// IdentityAccessManagement manages IAM identities and verifies signatures.
type IdentityAccessManagement struct {
    identities   []*Identity
    identityLock sync.RWMutex
    domain       string
    filerClient  *FilerClient
    policyEngine *BucketPolicyEngine
    // hashes is a pool of HMAC hash objects per access key (reuse for performance).
    hashes       map[string]*sync.Pool
    hashCounters map[string]*int32
}

// CircuitBreaker limits request rates and concurrent uploads per bucket.
type CircuitBreaker struct {
    enabled     bool
    // counters tracks current in-flight request counts per bucket.
    counters    map[string]*int64
    // limitations maps bucket name to its configured max concurrent count.
    limitations map[string]int64
    mu          sync.Mutex
}

// BucketConfigCache stores per-bucket configuration with TTL-based expiry.
type BucketConfigCache struct {
    // cache maps bucket name to its config.
    cache  map[string]*BucketConfig
    mu     sync.RWMutex
    ttl    time.Duration // default: 60 minutes
}

// BucketConfig holds per-bucket settings.
type BucketConfig struct {
    Versioning   VersioningState
    ObjectLock   *ObjectLockConfig
    CORS         []CORSRule
    Policy       []byte  // raw JSON bucket policy
    ACL          *BucketACL
    ExpiresAt    time.Time  // when this cache entry expires
}

type VersioningState string
const (
    VersioningUnset    VersioningState = ""
    VersioningEnabled  VersioningState = "Enabled"
    VersioningSuspended VersioningState = "Suspended"
)

type ObjectLockConfig struct {
    ObjectLockEnabled bool
    Rule              *ObjectLockRule
}

type ObjectLockRule struct {
    DefaultRetention *DefaultRetention
}

type DefaultRetention struct {
    Mode  string  // "GOVERNANCE" or "COMPLIANCE"
    Days  int
    Years int
}

// MultipartUpload tracks an in-progress multipart upload.
type MultipartUpload struct {
    UploadId string
    Bucket   string
    Key      string
    Parts    []*UploadedPart
}

type UploadedPart struct {
    PartNumber int
    ETag       string
    Size       int64
}
```

## 6. Public Interfaces

```go
package s3gateway

// NewS3ApiServer creates and initializes the S3 API gateway.
func NewS3ApiServer(router *mux.Router, opt *S3ApiServerOption) (*S3ApiServer, error)

// Shutdown gracefully stops the S3 gateway.
func (s3a *S3ApiServer) Shutdown()

// Auth verification
func (iam *IdentityAccessManagement) AuthRequest(
    r *http.Request,
    action string,
) (*Identity, s3err.ErrorCode)

// IAM config reload
func (iam *IdentityAccessManagement) LoadS3ApiConfigurationFromFiler(filerClient *FilerClient) error

// S3 error responses
func WriteErrorResponse(w http.ResponseWriter, r *http.Request, errCode s3err.ErrorCode)

// Key S3-compatible error codes (in s3err package):
const (
    ErrNone              s3err.ErrorCode = iota
    ErrAccessDenied
    ErrBucketNotEmpty
    ErrBucketAlreadyExists
    ErrBucketAlreadyOwnedByYou
    ErrNoSuchBucket
    ErrNoSuchKey
    ErrNoSuchUpload
    ErrInvalidPart
    ErrEntityTooLarge
    ErrSignatureDoesNotMatch
    ErrMethodNotAllowed
    ErrSlowDown           // circuit breaker triggered
)
```

## 7. Internal Algorithms

### Request Routing
```
gorilla/mux routes (registered in order):
  GET    /                          -> listBuckets
  PUT    /{bucket}                  -> createBucket
  DELETE /{bucket}                  -> deleteBucket
  GET    /{bucket}                  -> listObjects (if ?list-type= param)
  HEAD   /{bucket}                  -> headBucket
  PUT    /{bucket}?versioning       -> setBucketVersioning
  GET    /{bucket}?versioning       -> getBucketVersioning
  PUT    /{bucket}?policy           -> putBucketPolicy
  GET    /{bucket}?policy           -> getBucketPolicy
  DELETE /{bucket}?policy           -> deleteBucketPolicy
  PUT    /{bucket}?cors             -> putBucketCORS
  GET    /{bucket}?cors             -> getBucketCORS
  PUT    /{bucket}?object-lock      -> putObjectLock
  GET    /{bucket}?object-lock      -> getObjectLock
  POST   /{bucket}?delete           -> deleteObjects (batch)
  POST   /{bucket}/{key+}?uploads   -> initiateMultipart
  PUT    /{bucket}/{key+}?partNumber -> uploadPart
  POST   /{bucket}/{key+}?uploadId  -> completeMultipart
  DELETE /{bucket}/{key+}?uploadId  -> abortMultipart
  PUT    /{bucket}/{key+}           -> putObject
  GET    /{bucket}/{key+}           -> getObject
  HEAD   /{bucket}/{key+}           -> headObject
  DELETE /{bucket}/{key+}           -> deleteObject
```

### SigV4 Authentication
```
AuthRequest(r, action):
  // 1. Detect auth type
  authType = detectAuthType(r)

  switch authType:
  case AuthTypeSigV4:
    // Parse Authorization header:
    // AWS4-HMAC-SHA256 Credential=<key>/<date>/<region>/s3/aws4_request, ...
    accessKeyId, signedHeaders, signature = parseV4Header(r.Header.Get("Authorization"))

    // Lookup identity
    identity = iam.lookupByAccessKey(accessKeyId)
    if identity == nil: return nil, ErrInvalidAccessKeyId

    // Derive signing key
    signingKey = deriveSigningKey(identity.secretKey, date, region, "s3")

    // Compute canonical request
    canonicalRequest = buildCanonicalRequest(r, signedHeaders)
    stringToSign = buildStringToSign(r, canonicalRequest)
    expectedSig = hmacHex(signingKey, stringToSign)

    if signature != expectedSig: return nil, ErrSignatureDoesNotMatch

  case AuthTypePresigned:
    // Check X-Amz-Expires not exceeded
    // Verify signature against query params

  case AuthTypeAnonymous:
    // Check if bucket allows anonymous access via bucket policy

  // 2. Check IAM permissions
  if !iam.isAllowed(identity, action, bucket, key):
    return nil, ErrAccessDenied

  return identity, ErrNone
```

### PutObject Handler
```
putObject(w, r, bucket, key):
  // 1. Auth
  identity, errCode = iam.AuthRequest(r, "s3:PutObject")
  if errCode != ErrNone: WriteErrorResponse(w, r, errCode); return

  // 2. Circuit breaker check
  if !s3a.cb.Allow(bucket): WriteErrorResponse(w, r, ErrSlowDown); return

  // 3. Map to filer path
  filerPath = s3a.option.BucketsPath + "/" + bucket + "/" + key

  // 4. Handle SSE
  var cipherKey []byte
  if r.Header.Get("x-amz-server-side-encryption") == "AES256":
    cipherKey = generatePerObjectKey()

  // 5. Proxy upload to filer
  filerUrl = s3a.filerClient.PickOne() + filerPath
  req = newUploadRequest(filerUrl, r.Body, r.Header)
  resp, err = s3a.httpClient.Do(req)
  if err: WriteErrorResponse(w, r, ErrInternalError); return

  // 6. Compute ETag (MD5 of data for non-multipart)
  eTag = computeETag(r.Body)

  // 7. Respond
  w.Header().Set("ETag", "\""+eTag+"\"")
  w.WriteHeader(200)
```

### Multipart Upload
```
initiateMultipart(w, r, bucket, key):
  uploadId = generateUploadId()
  // Create staging directory in filer:
  // /buckets/{bucket}/.uploads/{uploadId}/
  filerCreateDir(uploadId)
  writeXML(w, 200, InitiateMultipartUploadResult{bucket, key, uploadId})

uploadPart(w, r, bucket, key, partNumber, uploadId):
  filerPath = "/buckets/{bucket}/.uploads/{uploadId}/{partNumber}"
  // Upload part as a regular file to filer
  uploadToFiler(filerPath, r.Body)
  eTag = computeETag(r.Body)
  w.Header().Set("ETag", "\""+eTag+"\"")
  w.WriteHeader(200)

completeMultipart(w, r, bucket, key, uploadId):
  // Parse XML body with part list
  parts = parseCompletionXML(r.Body)
  // Validate part ETags
  // Read all part entries from filer
  // Concatenate chunk lists into final entry
  finalChunks = []
  for part in parts:
    filerPath = "/buckets/{bucket}/.uploads/{uploadId}/{part.PartNumber}"
    entry = filerGetEntry(filerPath)
    finalChunks = append(finalChunks, entry.Chunks...)
  // Create final object with all chunks
  filerCreateEntry("/buckets/{bucket}/{key}", finalChunks)
  // Delete staging directory
  filerDeleteDir("/buckets/{bucket}/.uploads/{uploadId}/")
  writeXML(w, 200, CompleteMultipartUploadResult{bucket, key, eTag})
```

### Bucket Config Cache
```
getBucketConfig(bucket):
  cache.mu.RLock()
  entry = cache.cache[bucket]
  cache.mu.RUnlock()

  if entry != nil && time.Now().Before(entry.ExpiresAt):
    return entry  // cache hit

  // Cache miss: fetch from filer
  config = fetchBucketConfigFromFiler(bucket)
  cache.mu.Lock()
  cache.cache[bucket] = &BucketConfig{..., ExpiresAt: time.Now().Add(cache.ttl)}
  cache.mu.Unlock()
  return config

// Event-driven invalidation:
// subscribeMetaEvents watches /buckets/ for configuration changes.
// On change to /{bucket}/.config or policy objects:
invalidateBucketCache(bucket):
  cache.mu.Lock()
  delete(cache.cache, bucket)
  cache.mu.Unlock()
```

## 8. Persistence Model

The S3 gateway persists nothing directly. All state is:
- **Object data**: stored in volume servers (via filer)
- **Bucket structure**: stored as directories in filer (under `/buckets/`)
- **Bucket policies**: stored as files in filer (e.g., `/buckets/my-bucket/.s3/policy.json`)
- **IAM config**: stored in filer at `/etc/iam_config.json` or a configured file
- **Multipart staging**: stored as files in filer under `/buckets/{bucket}/.uploads/`

## 9. Concurrency Model

- `IdentityAccessManagement.identityLock sync.RWMutex`: RLock for every request auth; Lock for config reload
- `BucketConfigCache.mu sync.RWMutex`: RLock for cache reads; Lock for cache writes
- `CircuitBreaker.mu sync.Mutex`: Lock for counter updates
- `inFlightDataLimitCond *sync.Cond`: blocks uploads above limit
- `FilerClient`: thread-safe connection pool to multiple filer instances

## 10. Configuration

```go
type S3ApiServerOption struct {
    Filers                   []types.ServerAddress // required
    Masters                  []types.ServerAddress // for filer discovery
    Port                     int           `default:"8333"`
    GRPCPort                 int           `default:"18333"`
    DomainName               string        `default:""` // enables virtual-hosted style
    BucketsPath              string        `default:"/buckets"`
    AllowEmptyFolder         bool          `default:"false"`
    EnableIam                bool          `default:"false"`
    IamConfigFile            string        `default:""`
    ExternalUrl              string        `default:""`
    Cipher                   bool          `default:"false"`
    ConcurrentUploadLimitMB  int64         `default:"0"`
    ConcurrentFileUploadLimit int          `default:"0"`
    BucketConfigCacheTTL     time.Duration `default:"60m"`
    SecurityConfig           *security.SecurityConfig
}
```

## 11. Observability

- `obs.S3RequestsTotal.WithLabelValues(operation, bucket, status).Inc()` per request
- `obs.S3RequestLatency.WithLabelValues(operation).Observe(duration)` per request
- `obs.S3InFlightUploads.Set(count)` updated atomically
- Circuit breaker throttle events logged at WARN: `"circuit breaker triggered bucket=%s"`
- IAM config reload logged at INFO
- Bucket size metrics computed periodically and exposed

Debug endpoints:
- `GET /s3/status` — JSON with connected filers, IAM config hash
- `GET /s3/iam` — list of IAM identities (admin only)

## 12. Testing Strategy

- **Unit tests**:
  - `TestSigV4Verification`: generate and verify SigV4 signature
  - `TestSigV4WrongKey`: assert ErrSignatureDoesNotMatch
  - `TestSigV4Expired`: presigned URL with past X-Amz-Expires, assert error
  - `TestBucketConfigCacheHit`: set config, get, assert no filer call
  - `TestBucketConfigCacheExpiry`: set config with short TTL, wait, assert filer call on next get
  - `TestBucketConfigCacheInvalidation`: filer event fires, assert cache cleared
  - `TestCircuitBreakerAllow`: below limit → allowed; at limit → ErrSlowDown
  - `TestMultipartUploadFlow`: initiate → uploadPart × 3 → complete, assert final object
  - `TestPutObject`: mock filer, assert upload proxied correctly
  - `TestGetObject`: mock filer entry with chunks, assert bytes streamed back
- **Integration tests**:
  - `TestS3CompatibleSDK`: use `aws-sdk-go` client against the gateway, run PutObject/GetObject/DeleteObject/ListObjects

## 13. Open Questions

None.
