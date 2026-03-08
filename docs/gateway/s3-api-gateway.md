# S3 API Gateway

## Assumptions

- The S3 gateway is a stateless proxy that translates S3 API calls into Filer operations.
- It requires at least one Filer address; optionally discovers filers via master servers.
- Bucket operations map to directories under a configurable buckets path (default `/buckets`).

## Code Files / Modules Referenced

- `goblob/command/s3.go` - CLI entry, `runS3()`
- `goblob/s3api/s3api_server.go` - `S3ApiServer` struct, `NewS3ApiServer()`
- `goblob/s3api/s3api_handlers.go` - Top-level handler dispatch
- `goblob/s3api/s3api_bucket_handlers.go` - Bucket CRUD (Create, Delete, List, Head)
- `goblob/s3api/s3api_object_handlers.go` - Object GET, HEAD, metadata
- `goblob/s3api/s3api_object_handlers_put.go` - Object PUT, upload
- `goblob/s3api/s3api_object_handlers_delete.go` - Object DELETE, batch delete
- `goblob/s3api/s3api_object_handlers_copy.go` - Object COPY
- `goblob/s3api/s3api_object_handlers_list.go` - ListObjects v1/v2
- `goblob/s3api/s3api_object_handlers_multipart.go` - Multipart upload
- `goblob/s3api/auth_credentials.go` - `IdentityAccessManagement`, identity loading
- `goblob/s3api/auth_signature_v4.go` - AWS Signature V4 verification
- `goblob/s3api/auth_signature_v2.go` - AWS Signature V2 verification
- `goblob/s3api/s3api_circuit_breaker.go` - `CircuitBreaker` for request limiting
- `goblob/s3api/s3api_bucket_config.go` - Bucket-level configuration caching
- `goblob/s3api/s3api_bucket_policy_engine.go` - Bucket policy evaluation
- `goblob/s3api/s3api_embedded_iam.go` - Embedded IAM API server
- `goblob/s3api/s3_sse_s3.go` - Server-Side Encryption with S3-managed keys
- `goblob/s3api/s3_sse_c.go` - Server-Side Encryption with customer keys
- `goblob/s3api/s3_sse_kms.go` - Server-Side Encryption with KMS keys
- `goblob/s3api/s3api_object_versioning.go` - Object versioning support
- `goblob/s3api/s3api_object_retention.go` - Object Lock / retention
- `goblob/s3api/filer_multipart.go` - Multipart upload via filer
- `goblob/s3api/filer_util.go` - Filer utility operations
- `goblob/s3api/bucket_metadata.go` - Bucket metadata management
- `goblob/s3api/s3api_server_grpc.go` - gRPC service for IAM cache
- `goblob/s3api/cors/` - CORS handling
- `goblob/s3api/s3err/` - S3-compatible error codes
- `goblob/s3api/s3_constants/` - S3 constants and headers
- `goblob/s3api/iceberg/` - Apache Iceberg REST Catalog integration
- `goblob/s3api/s3tables/` - S3 Tables support
- `goblob/wdclient/filer_client.go` - `FilerClient` for HA filer access

## Overview

The S3 API Gateway provides an Amazon S3-compatible interface to GoBlob. It translates S3 REST API calls into operations against the Filer server, mapping S3 buckets to directories and S3 objects to files. It supports authentication (SigV4, SigV2), authorization (IAM policies, bucket policies), server-side encryption (SSE-S3, SSE-C, SSE-KMS), multipart uploads, object versioning, object lock, CORS, and more.

## Responsibilities

- **S3 REST API**: Full bucket and object CRUD, list, copy, multipart upload
- **Authentication**: AWS Signature V4/V2, presigned URLs
- **Authorization**: IAM identities, IAM policies, bucket policies, ACLs
- **Server-Side Encryption**: SSE-S3, SSE-C (customer-provided keys), SSE-KMS
- **Object Versioning**: Per-bucket versioning with version ID management
- **Object Lock**: Governance and compliance mode retention, legal hold
- **Multipart Upload**: Initiate, upload parts, complete, abort
- **Circuit Breaker**: Request rate limiting and concurrent upload throttling
- **Bucket Configuration**: Per-bucket settings caching with event-driven invalidation
- **CORS**: Cross-origin resource sharing per bucket
- **Bucket Policies**: S3-compatible bucket policy evaluation
- **Embedded IAM**: Optional IAM API on the same port for user/policy management
- **Iceberg Catalog**: Apache Iceberg REST Catalog for table metadata
- **Metrics**: Bucket size metrics collection

## Architecture Role

```
+------------------------------------------------------------------+
|                      S3 API Gateway                               |
+------------------------------------------------------------------+
|                                                                   |
|  HTTP :8333                gRPC :18333                            |
|  (S3 REST API)             (IAM cache sync)                       |
|       |                         |                                 |
|  +----+--------+          +-----+------+                          |
|  | gorilla/mux |          | gRPC Svc   |                          |
|  | Router      |          | S3IamCache |                          |
|  +----+--------+          +-----+------+                          |
|       |                         |                                 |
|  +----+---+----+---+----+---+----+---+                            |
|  |Auth    |Bucket|Object|Multi |IAM   |                           |
|  |Middleware|Hdlr |Hdlr  |part |Embed |                           |
|  +----+---+----+---+----+---+----+---+                            |
|       |         |       |       |                                 |
|  +----+---------+-------+-------+-----+                           |
|  |        IdentityAccessManagement     |                          |
|  |  (SigV4, SigV2, IAM, Policies)     |                          |
|  +----+--------------------------------+                          |
|       |                                                           |
|  +----+--------+   +----------+   +-----------+                   |
|  |FilerClient  |   |CircuitBkr|   |BucketReg  |                   |
|  |(HA, multi-  |   |(rate     |   |(metadata  |                   |
|  | filer)      |   | limit)   |   | events)   |                   |
|  +----+--------+   +----------+   +-----------+                   |
|       |                                                           |
+-------+-----------------------------------------------------------+
        |
        v
+-------+--------+
|  Filer Server   |
|  (metadata +    |
|   data proxy)   |
+-------+---------+
        |
        v
+-------+--------+
| Volume Servers  |
| (blob storage)  |
+----------------+
```

## Component Structure Diagram

```
+---------------------------------------------------------------+
|                       S3ApiServer                              |
+---------------------------------------------------------------+
| option             *S3ApiServerOption                           |
| iam                *IdentityAccessManagement  # SigV4/V2, IAM  |
| iamIntegration     *S3IAMIntegration          # advanced IAM    |
| cb                 *CircuitBreaker            # rate limiting   |
| filerGuard         *security.Guard            # filer JWT       |
| filerClient        *wdclient.FilerClient      # HA filer access |
| client             HTTPClientInterface        # HTTP to filer   |
| bucketRegistry     *BucketRegistry            # metadata events |
| credentialManager  *credential.CredentialManager                |
| bucketConfigCache  *BucketConfigCache         # 60-min TTL      |
| policyEngine       *BucketPolicyEngine        # policy eval     |
| embeddedIam        *EmbeddedIamApi            # optional IAM API|
| cipher             bool                       # volume encrypt  |
| inFlightDataSize   int64                      # upload tracking |
| inFlightDataLimitCond *sync.Cond                                |
+---------------------------------------------------------------+

+---------------------------------------------------------------+
|               IdentityAccessManagement                         |
+---------------------------------------------------------------+
| identities   []*Identity                                       |
| identityLock sync.RWMutex                                      |
| domain       string                  # virtual-hosted style    |
| externalHost string                  # for proxy SigV4 verify  |
| hashes       map[string]*sync.Pool   # reusable HMAC pools    |
| hashCounters map[string]*int32                                  |
| filerClient  *wdclient.FilerClient                              |
| policyEngine *BucketPolicyEngine                                |
| credentialManager *credential.CredentialManager                 |
+---------------------------------------------------------------+

+---------------------------------------------------------------+
|                     CircuitBreaker                             |
+---------------------------------------------------------------+
| enabled      bool                                              |
| counters     map[string]*int64        # per-bucket counters    |
| limitations  map[string]int64         # per-bucket limits      |
| s3a          *S3ApiServer             # back-ref for upload lim|
+---------------------------------------------------------------+
```

## Control Flow

### Startup Sequence

```
runS3() or embedded in startFiler()
    |
    +--> NewS3ApiServer(router, option)
    |       |
    |       +--> Load JWT signing keys
    |       +--> NewIdentityAccessManagement(option, nil)
    |       |    (initial IAM with no filer client)
    |       |
    |       +--> NewBucketPolicyEngine()
    |       |
    |       +--> Create FilerClient:
    |       |    if masters provided:
    |       |       NewMasterClient() -> go KeepConnectedToMaster()
    |       |       NewFilerClient(filers, ..., MasterClient, discovery)
    |       |    else:
    |       |       NewFilerClient(filers, ..., no discovery)
    |       |
    |       +--> InitializeGlobalSSES3KeyManager()
    |       +--> iam.SetFilerClient(filerClient)
    |       +--> go iam.loadS3ApiConfigurationFromFiler()
    |       |
    |       +--> NewCircuitBreaker(option)
    |       +--> NewBucketConfigCache(60 * time.Minute)
    |       |
    |       +--> If IamConfig or EnableIam:
    |       |    loadIAMManagerFromConfig()
    |       |    NewS3IAMIntegration()
    |       |
    |       +--> If EnableIam:
    |       |    NewEmbeddedIamApi()
    |       |
    |       +--> NewBucketRegistry(s3a)
    |       +--> registerRouter(router)
    |       |
    |       +--> go subscribeMetaEvents(...)
    |       |    (listen for bucket/IAM config changes)
    |       |
    |       +--> go startBucketSizeMetricsLoop()
    |
    +--> Start gRPC server
    +--> Start HTTP server
    +--> grace.OnInterrupt(s3a.Shutdown)
```

### Request Flow

```
HTTP Request --> gorilla/mux Router
    |
    +--> CORS middleware (if configured)
    |
    +--> Route matching:
    |    /{bucket}           --> Bucket handlers
    |    /{bucket}/{object}  --> Object handlers
    |    /                   --> ListBuckets
    |
    +--> Auth middleware (s3api_iam_middleware.go):
    |    +--> Extract auth type (SigV4, SigV2, Presigned, Anonymous)
    |    +--> Verify signature
    |    +--> Resolve IAM identity
    |    +--> Check bucket/object permissions (IAM + bucket policy)
    |    +--> Set identity in request context
    |
    +--> CircuitBreaker check:
    |    +--> Check per-bucket request limits
    |    +--> Check global concurrent upload limits
    |
    +--> Handler execution:
    |    +--> Translate S3 operation to Filer operation
    |    +--> Execute via FilerClient / HTTP to filer
    |    +--> Return S3 XML response
```

## Runtime Sequence Flow

### PutObject

```
Client                S3Gateway              Filer              Volume
  |                      |                     |                   |
  |-- PUT /bucket/key -->|                     |                   |
  |   + SigV4 headers    |                     |                   |
  |                      |-- verify SigV4 ---->|                   |
  |                      |   (HMAC-SHA256)     |                   |
  |                      |                     |                   |
  |                      |-- check IAM policy->|                   |
  |                      |   (s3:PutObject)    |                   |
  |                      |                     |                   |
  |                      |-- CircuitBreaker -->|                   |
  |                      |   check limits      |                   |
  |                      |                     |                   |
  |                      |-- POST /buckets/    |                   |
  |                      |   bucket/key ------>|                   |
  |                      |   (proxy upload     |-- assign fid --->|
  |                      |    to filer)        |<-- {fid, url} ---|
  |                      |                     |-- PUT to vol --->|
  |                      |                     |<-- 201 ----------|
  |                      |                     |-- save entry     |
  |                      |<-- entry metadata --|                   |
  |                      |                     |                   |
  |<-- 200 OK + ETag ----|                     |                   |
```

### Multipart Upload

```
Client                S3Gateway              Filer
  |                      |                     |
  |-- InitiateMultipart->|                     |
  |                      |-- create multipart  |
  |                      |   metadata in filer |
  |                      |   (/buckets/b/.uploads/uploadId/)
  |<-- {uploadId} -------|                     |
  |                      |                     |
  |-- UploadPart(1) ---->|                     |
  |                      |-- save part as file |
  |                      |   in filer          |
  |<-- ETag(1) ----------|                     |
  |                      |                     |
  |-- UploadPart(2) ---->|                     |
  |                      |-- save part as file |
  |<-- ETag(2) ----------|                     |
  |                      |                     |
  |-- CompleteMultipart->|                     |
  |   [part1, part2]     |                     |
  |                      |-- read part entries |
  |                      |-- concatenate chunks|
  |                      |   into final object |
  |                      |-- delete .uploads/  |
  |<-- 200 + ETag -------|                     |
```

## Data Flow Diagram

```
S3 Bucket/Object to Filer Path Mapping:

  S3: s3://my-bucket/path/to/object.txt
       |
       v
  Filer: /buckets/my-bucket/path/to/object.txt
         |          |              |
    BucketsPath  Bucket dir    Object path

Config Changes Flow:

  Filer /etc/                        S3 Gateway
  +--------------------+             +------------------+
  | iam_config.json    |  --event--> | subscribeMetaEvents()
  | identities/        |             | reload IAM config|
  | policies/          |             | invalidate cache |
  +--------------------+             +------------------+
```

## Dependencies

| Dependency | Purpose |
|---|---|
| `github.com/gorilla/mux` | HTTP routing |
| `goblob/wdclient` | `FilerClient` for HA filer access, `MasterClient` for discovery |
| `goblob/security` | JWT guard, TLS |
| `goblob/filer` | Directory constants, entry types |
| `goblob/credential` | Credential management |
| `goblob/pb/s3_pb` | gRPC IAM cache service |
| `goblob/s3api/s3err` | S3-compatible error codes |
| `goblob/s3api/s3_constants` | Header constants |
| `goblob/s3api/cors` | CORS handling |
| `goblob/s3api/policy_engine` | Advanced policy evaluation |
| `goblob/util/grace` | Config reload signals |

## Error Handling

- **Auth failure**: Returns S3 `AccessDenied` or `SignatureDoesNotMatch` XML error
- **Bucket not found**: Returns S3 `NoSuchBucket`
- **Object not found**: Returns S3 `NoSuchKey`
- **Circuit breaker**: Returns S3 `SlowDown` when limits exceeded
- **Filer unreachable**: `FilerClient` fails over to next filer; returns 503 if all unavailable
- **SSE errors**: Returns appropriate S3 error codes for encryption failures

## Async / Background Behavior

| Goroutine | Purpose |
|---|---|
| `subscribeMetaEvents()` | Watches filer for IAM/bucket config changes |
| `startBucketSizeMetricsLoop()` | Periodic bucket size metrics collection |
| `MasterClient.KeepConnectedToMaster()` | Filer discovery via master |
| `loadS3ApiConfigurationFromFiler()` | Initial IAM config loading |

## Security Considerations

- **SigV4 verification**: Full AWS Signature V4 with chunked upload support
- **SigV2 fallback**: Legacy signature support
- **Presigned URLs**: Time-limited signed URLs for anonymous access
- **Bucket policies**: JSON policy documents evaluated per request
- **SSE-S3**: Transparent encryption with server-managed keys (KEK stored in filer)
- **SSE-C**: Customer-provided encryption keys (never stored)
- **SSE-KMS**: KMS-managed encryption keys
- **IAM read-only mode**: `-s3.iam.readOnly` prevents write operations on IAM API
- **Local filer socket**: Unix socket for co-located S3+filer (bypasses network)

## Configuration

- **Key CLI flags**: `-filer`, `-port`, `-config`, `-domainName`, `-allowEmptyFolder`
- **IAM config**: `-iam.config` for advanced IAM; `-s3.enableIam` for embedded IAM API
- **External URL**: `-s3.externalUrl` for signature verification behind reverse proxy
- **Buckets path**: Configurable via `filer.options.buckets_folder` (default `/buckets`)
- **Encryption**: `-encryptVolumeData` for volume-level encryption
- **Limits**: `-concurrentUploadLimitMB`, `-concurrentFileUploadLimit`

## Edge Cases

- **Virtual-hosted style**: `-domainName` enables `bucket.domain.com` style URLs
- **Empty folder handling**: S3 has no real directories; GoBlob creates marker entries
- **Large object copy**: Streaming copy avoids loading entire object into memory
- **Multipart cleanup**: Incomplete multiparts left in `.uploads/`; manual cleanup needed
- **Bucket config cache**: Event-driven invalidation with 60-minute TTL fallback
- **Multiple filers**: `FilerClient` tracks current active filer, fails over on error
- **SIGHUP reload**: Reloads IAM configuration from file without restart
