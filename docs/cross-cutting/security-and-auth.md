# Security and Authentication

## Assumptions

- Security configuration is loaded from `security.toml` found in standard config directories.
- JWT is the primary token-based authentication mechanism for inter-service communication.
- TLS is optional and configured per-service (master, volume, filer, etc.).

## Code Files / Modules Referenced

- `goblob/security/guard.go` - `Guard` struct, IP whitelist + JWT middleware
- `goblob/security/jwt.go` - JWT generation and decoding (HS256)
- `goblob/security/tls.go` - TLS configuration loading
- `goblob/security/tls_client.go` - Client-side TLS loading
- `goblob/util/config.go` - `LoadConfiguration()`, Viper config loading
- `goblob/server/master_server.go` - Master JWT signing key configuration
- `goblob/server/volume_server.go` - Volume JWT verification
- `goblob/server/filer_server.go` - Filer JWT signing and volume JWT
- `goblob/s3api/auth_credentials.go` - S3 IAM identity management
- `goblob/s3api/auth_signature_v4.go` - AWS Signature V4 verification
- `goblob/s3api/auth_signature_v2.go` - AWS Signature V2 verification
- `goblob/s3api/s3_sse_s3.go` - Server-Side Encryption with S3 keys
- `goblob/s3api/s3_sse_c.go` - Server-Side Encryption with customer keys
- `goblob/s3api/s3_sse_kms.go` - Server-Side Encryption with KMS
- `goblob/credential/` - Credential management for IAM
- `goblob/s3api/s3api_embedded_iam.go` - Embedded IAM API

## Overview

GoBlob implements a layered security model. Internal communication between master, volume, and filer servers uses JWT tokens and optional TLS/mTLS. The S3 gateway adds AWS-compatible authentication (Signature V4/V2), IAM identity management, and server-side encryption. IP whitelisting provides an additional access control layer.

## Responsibilities

- **JWT authentication**: Token-based access control for volume and filer operations
- **IP whitelisting**: Restrict write access to specific IP addresses/CIDR ranges
- **TLS/mTLS**: Encrypted transport with optional mutual certificate verification
- **S3 authentication**: AWS Signature V4/V2 verification for S3 API requests
- **IAM management**: Identity, policy, and credential management for S3 access
- **Server-Side Encryption**: SSE-S3, SSE-C, SSE-KMS for data-at-rest encryption

## Architecture Role

```
+------------------------------------------------------------------+
|                      Security Layers                              |
+------------------------------------------------------------------+
|                                                                   |
|  Layer 1: Transport Security (TLS/mTLS)                          |
|  +------------------------------------------------------------+  |
|  | security.toml: https.{master,volume,filer}.{cert,key,ca}   |  |
|  | grpc.{master,volume,filer} TLS config                      |  |
|  +------------------------------------------------------------+  |
|                                                                   |
|  Layer 2: IP Whitelist (Guard)                                   |
|  +------------------------------------------------------------+  |
|  | -whiteList flag or guard.white_list in security.toml        |  |
|  | Checked before JWT; exact IP + CIDR range matching          |  |
|  +------------------------------------------------------------+  |
|                                                                   |
|  Layer 3: JWT Token Authentication                               |
|  +------------------------------------------------------------+  |
|  | Master signs:  jwt.signing.key      -> Volume verifies      |  |
|  | Filer signs:   jwt.filer_signing.key -> Filer verifies      |  |
|  | S3 -> Filer:   jwt.filer_signing.key (filer access token)   |  |
|  | Read tokens:   jwt.signing.read.key (separate read-only)    |  |
|  +------------------------------------------------------------+  |
|                                                                   |
|  Layer 4: S3 Authentication (Gateway only)                       |
|  +------------------------------------------------------------+  |
|  | AWS Signature V4 (HMAC-SHA256)                              |  |
|  | AWS Signature V2 (legacy)                                   |  |
|  | Presigned URLs (time-limited)                               |  |
|  +------------------------------------------------------------+  |
|                                                                   |
|  Layer 5: Data Encryption (S3 Gateway)                           |
|  +------------------------------------------------------------+  |
|  | SSE-S3:  Server-managed keys (KEK in filer)                 |  |
|  | SSE-C:   Customer-provided keys (per-request)               |  |
|  | SSE-KMS: KMS-managed keys                                   |  |
|  +------------------------------------------------------------+  |
|                                                                   |
+------------------------------------------------------------------+
```

## Component Structure Diagram

```
+---------------------------------------------------------------+
|                         Guard                                  |
+---------------------------------------------------------------+
| whiteListIp      map[string]struct{}  # exact IP match         |
| whiteListCIDR    map[string]*net.IPNet # CIDR range match      |
| SigningKey        SigningKey           # write JWT key          |
| ExpiresAfterSec  int                  # write token TTL        |
| ReadSigningKey    SigningKey           # read JWT key           |
| ReadExpiresAfterSec int               # read token TTL         |
| isWriteActive    bool                 # any security enabled?  |
| isEmptyWhiteList bool                 # whitelist empty?       |
+---------------------------------------------------------------+
| WhiteList(handler) http.HandlerFunc   # middleware wrapper     |
| checkWhiteList(w, r) error            # IP check              |
| UpdateWhiteList([]string)             # dynamic update         |
+---------------------------------------------------------------+

JWT Token Types:
+---------------------------------------------------------------+
| GoBlobFileIdClaims                                            |
| (Master -> Volume)                                             |
+---------------------------------------------------------------+
| Fid  string              # file ID this token grants access to|
| jwt.RegisteredClaims     # exp, nbf standard claims           |
+---------------------------------------------------------------+

+---------------------------------------------------------------+
| GoBlobFilerClaims                                             |
| (S3/Gateway -> Filer)                                          |
+---------------------------------------------------------------+
| AllowedPrefixes []string # path prefixes allowed               |
| AllowedMethods  []string # HTTP methods allowed                |
| jwt.RegisteredClaims     # exp, nbf standard claims           |
+---------------------------------------------------------------+
```

## Control Flow

### JWT Token Flow (Write)

```
Client                Master               Volume Server
  |                     |                       |
  |-- /dir/assign ----->|                       |
  |                     |-- GenJwtForVolume     |
  |                     |   Server(signingKey,  |
  |                     |   expiresAfterSec,    |
  |                     |   fileId)             |
  |                     |                       |
  |<-- {fid, url, jwt}--|                       |
  |                     |                       |
  |-- PUT /fid -------->|                       |
  |   Authorization:    |                       |
  |   Bearer <jwt>      |                       |
  |                     |                       |
  |                     |     DecodeJwt(key, jwt)
  |                     |     verify HS256 sig  |
  |                     |     check exp, nbf    |
  |                     |     check fid match   |
  |                     |                       |
  |<-- 201 Created -----|<-- OK if valid -------|
```

### Guard Middleware Flow

```
HTTP Request
    |
    v
Guard.WhiteList(handler)
    |
    +--> Is security active? (isWriteActive)
    |    No  --> pass through to handler
    |    Yes --> continue
    |
    +--> checkWhiteList(r):
    |    +--> GetActualRemoteHost(r)
    |    |    (uses RemoteAddr only, never X-Forwarded-For)
    |    |
    |    +--> Check exact IP match (whiteListIp)
    |    |    Found? --> pass through
    |    |
    |    +--> Check CIDR range match (whiteListCIDR)
    |    |    Found? --> pass through
    |    |
    |    +--> Not found: return 401 Unauthorized
    |
    +--> handler(w, r)
```

### S3 Signature V4 Verification

```
S3 Request
    |
    +--> Extract auth type:
    |    - Authorization header (SigV4, SigV2)
    |    - Query params (presigned URL)
    |    - Anonymous
    |
    +--> If SigV4:
    |    +--> Parse Authorization header:
    |    |    AWS4-HMAC-SHA256
    |    |    Credential=<accessKey>/<date>/<region>/s3/aws4_request
    |    |    SignedHeaders=<headers>
    |    |    Signature=<sig>
    |    |
    |    +--> Lookup identity by accessKey
    |    +--> Derive signing key:
    |    |    HMAC(HMAC(HMAC(HMAC("AWS4"+secret, date), region), "s3"), "aws4_request")
    |    |
    |    +--> Compute canonical request hash
    |    +--> Compute string to sign
    |    +--> Compute expected signature
    |    +--> Compare with provided signature
    |    |
    |    +--> If chunked transfer:
    |         Verify each chunk signature (streaming SigV4)
    |
    +--> If presigned URL:
    |    +--> Check X-Amz-Expires not exceeded
    |    +--> Verify signature against query params
    |
    +--> Check IAM permissions:
         +--> Match identity policies against action + resource
         +--> Check bucket policies
         +--> Allow or deny
```

## Data Flow Diagram

```
Configuration Loading:

  security.toml
  +----------------------------------------------+
  | [jwt.signing]                                |
  |   key = "your_secret_key"                    |
  |   expires_after_seconds = 10                 |
  |                                              |
  | [jwt.signing.read]                           |
  |   key = "your_read_secret_key"               |
  |   expires_after_seconds = 60                 |
  |                                              |
  | [jwt.filer_signing]                          |
  |   key = "your_filer_secret_key"              |
  |   expires_after_seconds = 10                 |
  |                                              |
  | [guard]                                      |
  |   white_list = "10.0.0.0/8,192.168.1.0/24"  |
  |                                              |
  | [https.master]                               |
  |   cert = "/path/to/master.crt"               |
  |   key  = "/path/to/master.key"               |
  |   ca   = "/path/to/ca.crt"  (for mTLS)      |
  |                                              |
  | [grpc.master]                                |
  |   cert = "/path/to/grpc-master.crt"          |
  |   key  = "/path/to/grpc-master.key"          |
  |   ca   = "/path/to/grpc-ca.crt"             |
  +----------------------------------------------+
       |
       v (Viper loads)
  +----+---------+----+---------+----+----------+
  | Master       | Volume      | Filer          |
  | signs JWT    | verifies JWT| signs filer JWT|
  | for volumes  | on write/   | for S3 proxy   |
  |              | read access |                |
  +--------------+-------------+----------------+

TLS Certificate Flow:

  +----------+   TLS    +----------+   TLS    +----------+
  | Master   |<-------->| Volume   |<-------->| Filer    |
  | HTTPS    |  mTLS    | HTTPS    |  mTLS    | HTTPS    |
  | gRPC TLS |  (opt)   | gRPC TLS |  (opt)   | gRPC TLS |
  +----------+          +----------+          +----------+
       ^                                           ^
       |              TLS (client cert)            |
       +-------------------------------------------+
```

## Dependencies

| Dependency | Purpose |
|---|---|
| `github.com/golang-jwt/jwt/v5` | JWT token creation and parsing |
| `github.com/spf13/viper` | Configuration loading from security.toml |
| `crypto/tls` | TLS configuration |
| `github.com/hashicorp/go-plugin` | Certificate watching/auto-refresh (via pemfile) |
| `google.golang.org/grpc/credentials` | gRPC TLS credentials |
| Standard `net` | IP parsing, CIDR range matching |

## Error Handling

- **JWT expired**: Returns 401 Unauthorized; client must re-obtain token
- **JWT invalid signature**: Returns 401; logged at V(0) level
- **IP not in whitelist**: Returns 401; logged with actual IP and RemoteAddr
- **TLS handshake failure**: Connection rejected; error logged
- **Missing security.toml**: No security enforced (all access allowed)
- **S3 SignatureDoesNotMatch**: Returns S3 XML error `SignatureDoesNotMatch`

## Security Considerations

- **No X-Forwarded-For trust**: `GetActualRemoteHost()` only uses `RemoteAddr` from the connection, never trusts proxy headers
- **Separate read/write keys**: Different JWT signing keys for read vs write operations
- **Token scoping**: `GoBlobFileIdClaims.Fid` restricts tokens to specific file IDs
- **Filer claims**: `GoBlobFilerClaims` can restrict to specific path prefixes and methods
- **HS256 only**: JWT signing uses HMAC-SHA256; other algorithms rejected
- **Certificate auto-refresh**: TLS certificates monitored for changes; hot-reloaded
- **Cipher mode**: Volume-level encryption when `-encryptVolumeData` enabled

## Configuration

- **Config search paths**: `.`, `$HOME/.goblob/`, `/usr/local/etc/goblob/`, `/etc/goblob/`
- **JWT sections**: `jwt.signing`, `jwt.signing.read`, `jwt.filer_signing`, `jwt.filer_signing.read`
- **TLS sections**: `https.{master,volume,filer}.{cert,key,ca}` for HTTPS
- **gRPC TLS sections**: `grpc.{master,volume,filer}.{cert,key,ca}`
- **Guard whitelist**: `guard.white_list` (comma-separated IPs/CIDRs)
- **CORS**: `cors.allowed_origins.values` (comma-separated domains)
- **S3 IAM**: Via `filer.toml` or `-iam.config` file or filer `/etc/iam_config.json`

## Edge Cases

- **Empty signing key**: JWT generation returns empty string; no token required for access
- **Both whitelist and JWT**: Whitelist checked first; if whitelisted, JWT not required
- **CIDR parsing failure**: Invalid CIDR in whitelist logged as error but other entries still work
- **Token in multiple places**: Checked in order: URL query param `jwt`, Authorization header, cookie `AT`
- **Zero expires_after_seconds**: Token has no expiration (not recommended for production)
- **Filer IP whitelist disabled**: Currently disabled per GitHub issue #7094; warning logged if configured
