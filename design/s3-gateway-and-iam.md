# S3 Gateway and IAM

### Purpose

Expose an S3-compatible HTTP API backed by filer metadata/data, with SigV4 authentication, IAM authorization, bucket/object controls, multipart upload support, and lifecycle policy storage.

### Scope

**In scope:**
- S3 server routing and authorization in `goblob/s3api/server.go`.
- `FilerGateway` interface and `BucketRootInitializer` optional interface in `goblob/s3api/store.go`.
- Bucket/object/multipart/lifecycle/advanced handlers in `goblob/s3api/*_handlers.go`.
- SigV4 verification in `goblob/s3api/auth/auth.go`.
- IAM in-memory manager and filer KV persistence in `goblob/s3api/iam/*.go`.
- Filer integration client (`FilerClient`) in `goblob/s3api/filer_client.go` — implements `FilerGateway`.
- Quota checks used by object writes/deletes.

**Out of scope:**
- Filer service implementation details.
- External cloud-specific tiering implementations beyond S3-compatible paths.

### Primary User Flow

1. S3 client sends request to `/` or `/<bucket>/<key>`.
2. Gateway resolves operation by HTTP method + query keys (`uploads`, `uploadId`, `versioning`, `policy`, `cors`, `lifecycle`, `tagging`, `delete`).
3. Gateway verifies SigV4 credentials and IAM action authorization.
4. Handler executes filer-backed operation (bucket create/list/delete, object put/get/delete, multipart state updates, metadata updates).
5. XML/HTTP response is returned with S3-style status/error semantics.

### System Flow

1. Entry point: `NewS3ApiServer` creates `FilerClient`, IAM manager, optional quota manager, then registers handlers (`/health`, `/ready`, `/`).
2. Dispatch path (`handle`):
   - `splitBucketAndKey` parses path.
   - bucket-level and object-level handlers are selected via query flags and method.
3. Auth path (`authorize`):
   - `s3auth.VerifyRequest` determines auth type (`Authorization` header or presigned query params).
   - canonical request + string-to-sign are built and HMAC compared.
   - IAM `Authenticate` and `IsAuthorized` enforce action grants.
4. Data path examples:
   - `handlePutObject`: read body (<=256MB), check user/bucket quota, persist object, set extended metadata (`s3:etag`, owner, version id, SSE).
   - `handleGetObject`: fetch object from filer and map extended metadata to HTTP headers.
   - `handleCompleteMultipart`: validates part order/ETag/min-size, concatenates parts, writes final object, deletes multipart state.
5. Config/state path:
   - Versioning/policy/CORS/lifecycle are stored as filer bucket metadata keys.
   - IAM configuration is loaded/saved through filer KV key `__iam_config__`.

```
S3 request
  -> handle() route split
  -> authorize(action,bucket,key)
     -> SigV4 verify + IAM check
       -> [deny] S3 error XML
       -> [allow] handler executes filer operation
  -> XML/HTTP response
```

### Data Model

- `FilerGateway` interface (`goblob/s3api/store.go`) — all filer-backed operations behind one seam:
  - bucket ops: `ListBuckets`, `BucketExists`, `CreateBucket`, `DeleteBucket`.
  - object ops: `PutObject`, `GetObject`, `DeleteObject`, `ListObjects`, `UpdateObjectExtended`.
  - bucket metadata: `GetBucketMeta`, `SetBucketMeta`, `DeleteBucketMeta` (keyed by e.g. `"lifecycle"`, `"cors"`, `"policy"`, `"versioning"`).
  - multipart: `CreateMultipartUpload`, `LoadMultipartUpload`, `PutMultipartPart`, `GetMultipartPart`, `DeleteMultipartUpload`.
  - KV store (also satisfies `quota.KVStore` and `iam.FilerIAMClient`): `KvGet`, `KvPut`, `KvDelete`.
  - `BucketRootInitializer` optional interface: `EnsureBucketsRoot(ctx)` called at startup.
  - `*FilerClient` in `filer_client.go` is the production implementation; tests inject fakes.
- IAM model (`goblob/s3api/iam/identity.go`):
  - `Identity { Name, Credentials[], Actions[] }`.
  - `Credential { AccessKey, SecretKey }`.
  - persisted at KV key `__iam_config__` as JSON proto payload.
- Object metadata (`ObjectData` in `goblob/s3api/filer_client.go`):
  - `Content []byte`, `ContentType string`, `Extended map[string][]byte`, `Mtime`, `Size`.
  - extended keys used by handlers: `s3:etag`, `s3:owner`, `s3:version_id`, `s3:sse`, `s3:tags`, `s3:storage_class`.
- Multipart metadata (`MultipartMeta` + `multipart.MultipartUpload`):
  - `upload_id`, `bucket`, `key`, `created_at_unix`, part map keyed by `partNumber`.
- Quota model (`goblob/quota/quota.go`):
  - JSON `Quota { max_bytes, used_bytes }` stored in filer KV keys `quota:user:<id>` and `quota:bucket:<name>`.

### Interfaces and Contracts

- Health:
  - `GET /health` -> `200 OK`.
  - `GET /ready` -> `503` when filer store is not configured.
- Bucket-level S3 operations:
  - `GET /` list buckets.
  - `PUT /<bucket>` create bucket.
  - `DELETE /<bucket>` delete bucket (must be empty).
  - `GET /<bucket>` list objects (`prefix` supported).
  - `HEAD /<bucket>` bucket existence check.
  - Query subresources: `?versioning`, `?policy`, `?cors`, `?lifecycle`, `?delete`.
- Object-level operations:
  - `PUT/GET/HEAD/DELETE /<bucket>/<key>`.
  - `?tagging` (PUT/GET/DELETE).
  - multipart: `?uploads`, `?uploadId=<id>&partNumber=<n>`, `?uploadId=<id>` completion/abort.
- Auth contract:
  - Anonymous access denied when IAM config has identities.
  - Error codes map to S3-style XML (`AccessDenied`, `SignatureDoesNotMatch`, `InvalidAccessKeyId`, etc.).

### Dependencies

**Internal modules:**
- `goblob/s3api/filer_client.go` for all filer-backed reads/writes.
- `goblob/s3api/auth` and `goblob/s3api/iam` for authn/authz.
- `goblob/quota` for quota enforcement.
- `goblob/s3api/lifecycle` for lifecycle validation and processing model.

**External services/libraries:**
- Depends on filer gRPC availability.
- S3 clients depend on strict SigV4 canonicalization behavior.

### Failure Modes and Edge Cases

- Missing or invalid signatures: request rejected with S3 auth error codes.
- Presigned URL expiry: returns `RequestExpired`.
- Object upload over 256MB in single PUT: `EntityTooLarge`.
- Multipart completion with missing/duplicate/ETag mismatch/too-small part: `InvalidPart` or `EntityTooSmall`.
- Quota enforcement:
  - hard deny only on explicit `QuotaExceededError`.
  - other quota load/update failures are logged and treated fail-open.
- Bucket metadata missing:
  - lifecycle/policy/cors/versioning GETs return specific `NoSuch*` errors.

### Observability and Debugging

- Logs:
  - S3 logger from `obs.New("s3")` records IAM reload and warning paths (`quota check failed`, metadata persist warnings).
- Debug entry points:
  - `server.go:handleBucketLevel` and `handleObjectLevel` for routing bugs.
  - `auth/auth.go:VerifyRequest` for signature mismatch analysis.
  - `filer_client.go` for filer-gRPC read/write behavior.
- Metrics:
  - No dedicated S3 Prometheus counters are defined in current code.

### Risks and Notes

- Several cloud-provider semantics are intentionally simplified (single-node metadata store, in-memory IAM state between reloads, multipart part composition in-memory).
- Single PUT reads full body into memory before write.
- Authorization model uses action strings and optional pattern matches; misconfigured wildcard grants can over-permit access.

Changes:

