# Feature: Identity and Access Management (IAM)

## 1. Purpose

The IAM subsystem manages S3 identities (users), their access key credentials, and the actions they are permitted to perform. It is co-hosted on the Filer gRPC port and persists identity configuration as a JSON document in the Filer's KV store. The S3 gateway consults IAM on every authenticated request to look up the identity associated with an access key and verify that the identity is allowed to perform the requested action.

## 2. Responsibilities

- **Identity store**: Store and retrieve S3 identities (name, access keys, allowed actions)
- **Access key lookup**: Find the identity matching a given access key ID
- **SigV4/SigV2 credential verification**: Provide the secret key for HMAC signature validation
- **Action authorization**: Evaluate whether an identity is allowed to perform an S3 action on a bucket/key
- **Configuration persistence**: Save/load the identity list from the Filer KV store as a JSON blob
- **gRPC API**: Expose `IAMService.GetS3ApiConfiguration` / `PutS3ApiConfiguration` on the Filer gRPC port
- **Hot reload**: Broadcast IAM changes to connected S3 gateways via Filer's `SubscribeMetadata` stream

## 3. Non-Responsibilities

- Does not implement S3 protocol parsing (that is the S3 gateway)
- Does not implement SigV4 canonical request construction (that is the S3 gateway's auth module)
- Does not store blob data or file metadata
- Does not enforce TLS (that is the security/guard layer)
- Does not implement IAM resource-based policies or condition keys (only action-level allow/deny)
- Does not implement STS (Security Token Service) — out of scope for v1

## 4. Architecture Design

```
+--------------------------------------------------------------+
|                          IAM                                  |
+--------------------------------------------------------------+
|                                                               |
|  S3 Gateway                    Filer gRPC port               |
|  SigV4 verify                  IAMService                    |
|       |                        GetS3ApiConfiguration         |
|       |                        PutS3ApiConfiguration         |
|       |                               |                      |
|  +----+----+                  +-------+-------+              |
|  |  IAM    |  <-- cache miss  | IAM gRPC impl |              |
|  | Client  |  (read from filer| (on filer)    |              |
|  | (in S3) |   KV store)      +-------+-------+              |
|  +---------+                          |                      |
|       |                       Filer KV store                 |
|       |                       key: "__iam_config__"          |
|       |                       value: JSON(S3ApiConfiguration)|
|       |                                                      |
|  in-memory                                                   |
|  +----+----+                                                 |
|  | Identity|                                                 |
|  | Access  |                                                 |
|  | Mgmt    |                                                 |
|  | struct  |                                                 |
|  +---------+                                                 |
|  identities []*Identity                                      |
|  hashes     map[accessKey]*sync.Pool (HMAC key pools)        |
+--------------------------------------------------------------+
```

### Identity Configuration Storage

The full IAM configuration is stored as a single JSON document in the Filer KV store:

```
Filer KV key:   []byte("__iam_config__")
Filer KV value: JSON-marshalled S3ApiConfiguration
```

## 5. Core Data Structures (Go)

```go
package iam

import (
    "sync"
    "crypto/hmac"
    "goblob/pb/iam_pb"
)

// IdentityAccessManagement manages the full set of S3 identities.
type IdentityAccessManagement struct {
    // identities is the live slice of all configured identities.
    // Replaced atomically on hot-reload; never mutated in place.
    identities []*Identity
    mu         sync.RWMutex

    // hashes pools pre-keyed HMAC instances per access key for performance.
    // map key = access key ID; pool value = *hmac.Hash keyed with the secret.
    hashes map[string]*sync.Pool
    hashesMu sync.RWMutex

    // filerClient is used to persist and load identity config from Filer KV.
    filerClient FilerIAMClient

    logger *slog.Logger
}

// Identity is a single S3 user.
type Identity struct {
    // Name is the human-readable user name.
    Name string

    // Credentials holds all access key / secret key pairs for this identity.
    Credentials []*Credential

    // Actions is the list of S3 actions this identity may perform.
    // Special values:
    //   "Read"   = s3:GetObject, s3:HeadObject, s3:ListBucket, s3:GetBucketLocation
    //   "Write"  = s3:PutObject, s3:DeleteObject, s3:AbortMultipartUpload, ...
    //   "Admin"  = all actions including bucket create/delete/policy
    //   Explicit = "s3:GetObject", "s3:PutObject", etc.
    //   "*"      = all actions (super-admin)
    Actions []string

    // AccountId is the AWS-style account ID for this identity (optional).
    AccountId string
}

// Credential holds one access key / secret key pair.
type Credential struct {
    AccessKey string
    SecretKey string
}

// S3Action represents a single S3 API action.
type S3Action string

const (
    S3ActionGetObject              S3Action = "s3:GetObject"
    S3ActionHeadObject             S3Action = "s3:HeadObject"
    S3ActionPutObject              S3Action = "s3:PutObject"
    S3ActionDeleteObject           S3Action = "s3:DeleteObject"
    S3ActionListBucket             S3Action = "s3:ListBucket"
    S3ActionListBucketMultipart    S3Action = "s3:ListBucketMultipartUploads"
    S3ActionCreateMultipart        S3Action = "s3:CreateMultipartUpload"
    S3ActionCompleteMultipart      S3Action = "s3:CompleteMultipartUpload"
    S3ActionAbortMultipart         S3Action = "s3:AbortMultipartUpload"
    S3ActionUploadPart             S3Action = "s3:UploadPart"
    S3ActionGetBucketLocation      S3Action = "s3:GetBucketLocation"
    S3ActionCreateBucket           S3Action = "s3:CreateBucket"
    S3ActionDeleteBucket           S3Action = "s3:DeleteBucket"
    S3ActionGetBucketAcl           S3Action = "s3:GetBucketAcl"
    S3ActionPutBucketAcl           S3Action = "s3:PutBucketAcl"
    S3ActionGetBucketPolicy        S3Action = "s3:GetBucketPolicy"
    S3ActionPutBucketPolicy        S3Action = "s3:PutBucketPolicy"
    S3ActionDeleteBucketPolicy     S3Action = "s3:DeleteBucketPolicy"
    S3ActionGetBucketCors          S3Action = "s3:GetBucketCors"
    S3ActionPutBucketCors          S3Action = "s3:PutBucketCors"
    S3ActionListAllMyBuckets       S3Action = "s3:ListAllMyBuckets"
    S3ActionAdmin                  S3Action = "Admin"
    S3ActionWildcard               S3Action = "*"
)

// AuthResult is returned from credential lookup.
type AuthResult struct {
    Identity  *Identity
    IsAnon    bool   // true when no credentials were presented
    ErrorCode string // non-empty = auth failed (e.g., "InvalidAccessKeyId")
}

// FilerIAMClient is the interface for reading/writing IAM config from the Filer KV store.
type FilerIAMClient interface {
    KvGet(ctx context.Context, key []byte) ([]byte, error)
    KvPut(ctx context.Context, key []byte, value []byte) error
}
```

## 6. Public Interfaces

```go
package iam

// NewIdentityAccessManagement creates an IAM manager and loads the current config from filer.
func NewIdentityAccessManagement(filerClient FilerIAMClient, logger *slog.Logger) (*IdentityAccessManagement, error)

// LookupByAccessKey finds the identity whose credentials include accessKey.
// Returns (identity, secretKey, true) on hit; (nil, "", false) on miss.
func (iam *IdentityAccessManagement) LookupByAccessKey(accessKey string) (*Identity, string, bool)

// Authenticate verifies a request's credentials and returns an AuthResult.
// It does NOT verify the SigV4 signature itself — it returns the secret key
// so the caller can do HMAC verification.
func (iam *IdentityAccessManagement) Authenticate(accessKey string) AuthResult

// IsAuthorized reports whether the identity may perform the given action on bucket/key.
func (iam *IdentityAccessManagement) IsAuthorized(identity *Identity, action S3Action, bucket, object string) bool

// LoadFromFiler fetches the IAM configuration from the Filer KV store and replaces the in-memory state.
func (iam *IdentityAccessManagement) LoadFromFiler(ctx context.Context) error

// SaveToFiler persists the current in-memory IAM configuration to the Filer KV store.
func (iam *IdentityAccessManagement) SaveToFiler(ctx context.Context) error

// Reload atomically replaces the in-memory identity list.
// Called by the S3 gateway's metadata subscription when IAM config changes.
func (iam *IdentityAccessManagement) Reload(cfg *iam_pb.S3ApiConfiguration)

// GetConfiguration returns the current identity list as a proto message (for gRPC response).
func (iam *IdentityAccessManagement) GetConfiguration() *iam_pb.S3ApiConfiguration

```

## 7. Internal Algorithms

### Access Key Lookup
```
LookupByAccessKey(accessKey):
  iam.mu.RLock()
  identities = iam.identities
  iam.mu.RUnlock()

  for each identity in identities:
    for each cred in identity.Credentials:
      if cred.AccessKey == accessKey:
        return identity, cred.SecretKey, true
  return nil, "", false
```

### IsAuthorized (action-level check)
```
IsAuthorized(identity, action, bucket, object):
  if identity == nil: return false

  for each a in identity.Actions:
    switch a:
    case "*", "Admin":
      return true  // super-admin
    case "Read":
      if action is a read action (GetObject, HeadObject, ListBucket, etc.):
        return true
    case "Write":
      if action is a write action (PutObject, DeleteObject, etc.):
        return true
    case string(action):
      return true  // exact match

    // Bucket-scoped: "s3:GetObject:my-bucket/*"
    if strings.HasPrefix(a, string(action)+":"):
      pattern = a[len(action)+1:]
      if matchBucketPattern(pattern, bucket, object):
        return true

  return false
```

### Hot Reload (triggered by Filer metadata subscription)
```
// S3 gateway subscribes to filer changes on the IAM config key path.
// When the config file changes:

onIAMConfigChange(event):
  cfg = loadFromFiler(ctx)
  iam.Reload(cfg)

Reload(cfg):
  newIdentities = protoToIdentities(cfg)

  // Rebuild HMAC key pools
  newHashes = map[string]*sync.Pool{}
  for each identity in newIdentities:
    for each cred in identity.Credentials:
      key = []byte(cred.SecretKey)
      newHashes[cred.AccessKey] = &sync.Pool{
        New: func() interface{} {
          return hmac.New(sha256.New, key)
        },
      }

  iam.mu.Lock()
  iam.identities = newIdentities
  iam.mu.Unlock()

  iam.hashesMu.Lock()
  iam.hashes = newHashes
  iam.hashesMu.Unlock()
```

### GetHmacPool (for SigV4 signature verification)
```
GetHmacPool(accessKey):
  iam.hashesMu.RLock()
  pool = iam.hashes[accessKey]
  iam.hashesMu.RUnlock()
  return pool
```

### gRPC Service Implementation (co-hosted on Filer gRPC port)

```
GetS3ApiConfiguration(ctx, req):
  cfg = iam.GetConfiguration()
  return &iam_pb.GetS3ApiConfigurationResponse{S3ApiConfiguration: cfg}, nil

PutS3ApiConfiguration(ctx, req):
  // Validate the incoming config
  if err = validate(req.S3ApiConfiguration); err != nil:
    return nil, status.Errorf(codes.InvalidArgument, err.Error())

  // Save to Filer KV
  iam.Reload(req.S3ApiConfiguration)
  if err = iam.SaveToFiler(ctx); err != nil:
    return nil, err

  // The Filer's metadata subscription mechanism notifies all connected S3 gateways.
  return &iam_pb.PutS3ApiConfigurationResponse{}, nil
```

## 8. Persistence Model

The IAM configuration is stored as a single JSON-encoded `S3ApiConfiguration` protobuf message in the Filer KV store:

```
Filer KV key:   []byte("__iam_config__")
Filer KV value: proto.Marshal(S3ApiConfiguration{...})
```

## 9. Concurrency Model

| Mechanism | Usage |
|-----------|-------|
| `mu sync.RWMutex` | Protects `identities` slice; replaced atomically on reload |
| `hashesMu sync.RWMutex` | Protects `hashes` map; replaced atomically on reload |
| `sync.Pool` per access key | Reuses HMAC instances across concurrent SigV4 verifications |
| Filer KV store | Thread-safe via Filer gRPC; no additional locking needed |

The `identities` slice and `hashes` map are always replaced as a unit (never mutated element-by-element), so readers always see a consistent snapshot.

## 10. Configuration

IAM configuration is managed at runtime via the `PutS3ApiConfiguration` gRPC API or by editing the Filer KV directly. There is no static config file for IAM.

Initial bootstrap: if no `__iam_config__` key exists in Filer KV, the IAM manager starts with an empty identity list (all requests treated as anonymous).

Anonymous access: some actions (e.g., `s3:GetObject` on a public bucket) may be allowed even without credentials. This is controlled by a synthetic `anonymous` identity with no credentials and explicit `Read` actions.

## 11. Observability

- `obs.IAMAuthTotal.WithLabelValues("ok"/"invalid_access_key"/"signature_mismatch").Inc()` per auth attempt
- `obs.IAMIdentityCount.Set(len(identities))` updated on every reload
- IAM config reload events logged at INFO with identity count
- `PutS3ApiConfiguration` calls logged at INFO with caller address

## 12. Testing Strategy

- **Unit tests**:
  - `TestLookupByAccessKey`: two identities with different access keys, assert correct one returned
  - `TestLookupByAccessKeyMiss`: unknown access key, assert (nil, "", false)
  - `TestIsAuthorizedWildcard`: identity with `"*"`, assert all actions allowed
  - `TestIsAuthorizedRead`: identity with `"Read"`, assert GetObject allowed, PutObject denied
  - `TestIsAuthorizedExplicit`: identity with `"s3:PutObject"`, assert only PutObject allowed
  - `TestIsAuthorizedBucketScoped`: `"s3:GetObject:my-bucket/*"`, assert correct bucket/object matching
  - `TestReloadAtomic`: reload while concurrent lookups running, assert no data race (run with -race)
  - `TestSaveAndLoadFromFiler`: save config, reload from mock Filer KV, assert identical
  - `TestHotReload`: simulate Filer metadata event, assert in-memory state updated

- **Integration tests**:
  - `TestIAMEndToEnd`: start filer + IAM, PUT config via gRPC, connect S3 gateway, assert authenticated request succeeds

## 13. Open Questions

None.
