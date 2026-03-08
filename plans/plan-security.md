# Feature: Security & Authentication

## 1. Purpose

The Security subsystem provides all access control and transport security for GoBlob. It implements a layered model:

1. **TLS/mTLS** — encrypted transport for HTTP and gRPC
2. **IP Whitelist** — restricts write access by source IP or CIDR range
3. **JWT tokens** — token-based authorization for volume and filer write/read operations
4. **S3 Signature V4/V2** — AWS-compatible authentication for the S3 gateway (handled in S3 gateway plan)

The security subsystem's primary consumers are the Volume Server (verifies write/read JWTs), the Filer Server (generates and verifies filer JWTs), and the Master Server (generates write JWTs for volume access).

## 2. Responsibilities

- Parse `security.toml` and expose typed config structs
- Load and hot-reload TLS certificates from file
- Provide `Guard` HTTP middleware enforcing IP whitelist + JWT verification
- Generate JWT tokens (`GoBlobFileIdClaims` for volume, `GoBlobFilerClaims` for filer)
- Verify JWT tokens on incoming requests
- Load gRPC TLS credentials for outbound and inbound connections
- Provide `GetActualRemoteHost()` that returns the real source IP without trusting proxy headers

## 3. Non-Responsibilities

- Does not implement S3 Signature V4 (that is in the S3 gateway)
- Does not manage user accounts or authorization policies (that is IAM in the S3 gateway)
- Does not implement mutual TLS handshakes at the application layer (handled by `crypto/tls`)
- Does not rotate JWT signing keys at runtime

## 4. Architecture Design

```
Incoming HTTP Request
        |
        v
Guard.WhiteList(handler)
        |
        +-- Is security active?  (any key or whitelist configured?)
        |   No  --> pass through to handler
        |   Yes -->
        |
        +-- checkWhiteList(r.RemoteAddr)
        |   IP in whitelist? --> pass through
        |   Not in whitelist -->
        |
        +-- Extract JWT from request:
        |   1. URL query param ?jwt=...
        |   2. Authorization: Bearer <token>
        |   3. Cookie "AT"
        |
        +-- VerifyJWT(token, signingKey)
        |   Valid?  --> pass through to handler
        |   Invalid --> 401 Unauthorized
        |
        v
handler(w, r)
```

### JWT Token Types

```
Master --> Client:  GoBlobFileIdClaims
  Signed with jwt.signing.key
  Payload: {fid: "3,abc123", exp: unix+10s}
  Purpose: authorize one specific volume write

Client --> Volume: same token in Authorization header
  Volume verifies with same key

Filer --> S3/Client: GoBlobFilerClaims
  Signed with jwt.filer_signing.key
  Payload: {allowedPrefixes: ["/buckets/my-bucket"], allowedMethods: ["PUT"]}
  Purpose: authorize filer access with path/method constraints
```

## 5. Core Data Structures (Go)

```go
package security

import (
    "crypto/tls"
    "net"
    "net/http"
    "sync"
    "github.com/golang-jwt/jwt/v5"
)

// SigningKey is the raw HMAC-SHA256 secret for JWT signing.
type SigningKey []byte

// Guard is the HTTP middleware enforcing IP whitelist and JWT verification.
type Guard struct {
    // Signing key for write JWT tokens.
    SigningKey          SigningKey
    ExpiresAfterSec     int
    // Separate signing key for read-only JWT tokens (optional).
    ReadSigningKey      SigningKey
    ReadExpiresAfterSec int

    // IP whitelist: exact matches and CIDR ranges.
    whiteListIp   map[string]struct{}
    whiteListCIDR []*net.IPNet
    whiteListMu   sync.RWMutex

    // isWriteActive is true when any security mechanism is configured.
    isWriteActive    bool
    // isEmptyWhiteList is true when the whitelist has no entries.
    isEmptyWhiteList bool
}

// GoBlobFileIdClaims is the JWT payload for volume write/read authorization.
type GoBlobFileIdClaims struct {
    Fid string `json:"fid"` // the FileId this token grants access to
    jwt.RegisteredClaims   // exp, nbf, iat standard fields
}

// GoBlobFilerClaims is the JWT payload for filer access authorization.
type GoBlobFilerClaims struct {
    AllowedPrefixes []string `json:"allowed_prefixes,omitempty"` // path prefix whitelist
    AllowedMethods  []string `json:"allowed_methods,omitempty"`  // HTTP method whitelist
    jwt.RegisteredClaims
}

// TLSConfig holds TLS configuration for a single service (HTTP or gRPC).
type TLSConfig struct {
    CertFile string
    KeyFile  string
    CAFile   string // non-empty = enable mTLS (client cert verification)
}

// certWatcher auto-reloads TLS certificates when the file changes on disk.
type certWatcher struct {
    certFile string
    keyFile  string
    mu       sync.RWMutex
    cert     *tls.Certificate
}
```

## 6. Public Interfaces

```go
package security

// NewGuard creates a Guard with the given whitelist and signing keys.
func NewGuard(
    whiteList []string,
    signingKey SigningKey,
    expiresAfterSec int,
    readSigningKey SigningKey,
    readExpiresAfterSec int,
) *Guard

// WhiteList returns an HTTP middleware that enforces the whitelist and JWT policy.
func (g *Guard) WhiteList(handler http.HandlerFunc) http.HandlerFunc

// UpdateWhiteList replaces the IP whitelist dynamically (thread-safe).
func (g *Guard) UpdateWhiteList(ips []string)

// JWT generation

// GenJwtForVolumeServer generates a write JWT for a specific FileId.
// Returns "" if signingKey is empty (security disabled).
func GenJwtForVolumeServer(signingKey SigningKey, expiresAfterSec int, fileId string) string

// GenJwtForFiler generates a filer access JWT.
func GenJwtForFiler(
    signingKey SigningKey,
    expiresAfterSec int,
    allowedPrefixes []string,
    allowedMethods []string,
) string

// JWT verification

// VerifyJwt verifies a volume server JWT and returns the FileId claim.
func VerifyJwt(signingKey SigningKey, tokenString string) (*GoBlobFileIdClaims, error)

// VerifyFilerJwt verifies a filer JWT.
func VerifyFilerJwt(signingKey SigningKey, tokenString string) (*GoBlobFilerClaims, error)

// TLS helpers

// LoadServerTLS creates a *tls.Config for an HTTPS server, with optional mTLS.
// If caFile is non-empty, client certificates are required and verified.
func LoadServerTLS(certFile, keyFile, caFile string) (*tls.Config, error)

// LoadClientTLS creates a *tls.Config for an HTTPS client.
func LoadClientTLS(certFile, keyFile, caFile string) (*tls.Config, error)

// NewCertWatcher creates a certWatcher that reloads the cert/key pair when changed.
func NewCertWatcher(certFile, keyFile string) (*certWatcher, error)

// GetTLSConfig returns a *tls.Config that uses the current cert (auto-reloaded).
func (cw *certWatcher) GetTLSConfig() *tls.Config

// GetActualRemoteHost extracts the real remote IP from the request.
// Does NOT trust X-Forwarded-For or X-Real-IP headers (prevent spoofing).
func GetActualRemoteHost(r *http.Request) string

// gRPC TLS helpers

// LoadGrpcServerTLS creates gRPC server credentials.
func LoadGrpcServerTLS(cfg TLSConfig) (credentials.TransportCredentials, error)

// LoadGrpcClientTLS creates gRPC client credentials.
func LoadGrpcClientTLS(cfg TLSConfig) (grpc.DialOption, error)
```

## 7. Internal Algorithms

### Guard.WhiteList middleware
```
WhiteList(handler):
  return func(w http.ResponseWriter, r *http.Request):
    if !g.isWriteActive:
      handler(w, r)
      return

    remoteHost = GetActualRemoteHost(r)

    // Check whitelist
    if !g.isEmptyWhiteList:
      if g.checkWhiteList(remoteHost) == nil:
        handler(w, r)
        return

    // Check JWT
    tokenStr = extractJwtFromRequest(r)
    if tokenStr == "":
      http.Error(w, "Access denied", http.StatusUnauthorized)
      return

    claims, err = VerifyJwt(g.SigningKey, tokenStr)
    if err != nil:
      http.Error(w, "Invalid JWT: "+err.Error(), http.StatusUnauthorized)
      return

    handler(w, r)
```

### extractJwtFromRequest
Checks three locations in order:
1. URL query parameter `?jwt=<token>`
2. `Authorization: Bearer <token>` header
3. Cookie with name `"AT"`

Returns the first found, or "" if none.

### checkWhiteList
```
checkWhiteList(host string) error:
  ip = net.ParseIP(host)
  if ip == nil: return ErrInvalidIP

  // Exact IP match
  g.whiteListMu.RLock()
  defer g.whiteListMu.RUnlock()

  if _, ok = g.whiteListIp[host]; ok: return nil

  // CIDR range match
  for _, cidr in g.whiteListCIDR:
    if cidr.Contains(ip): return nil

  return ErrNotWhitelisted
```

### GenJwtForVolumeServer
```
GenJwtForVolumeServer(signingKey, expiresAfterSec, fileId):
  if len(signingKey) == 0: return ""  // security disabled

  now = time.Now()
  claims = GoBlobFileIdClaims{
    Fid: fileId,
    RegisteredClaims: jwt.RegisteredClaims{
      ExpiresAt: jwt.NewNumericDate(now.Add(time.Duration(expiresAfterSec) * time.Second)),
      NotBefore: jwt.NewNumericDate(now),
    },
  }
  token = jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
  signed, _ = token.SignedString([]byte(signingKey))
  return signed
```

### VerifyJwt
```
VerifyJwt(signingKey, tokenString):
  if len(signingKey) == 0: return &GoBlobFileIdClaims{}, nil  // skip verification

  token, err = jwt.ParseWithClaims(tokenString, &GoBlobFileIdClaims{},
    func(token *jwt.Token) (interface{}, error):
      if _, ok = token.Method.(*jwt.SigningMethodHMAC); !ok:
        return nil, ErrUnexpectedSigningMethod
      return []byte(signingKey), nil
  )
  if err: return nil, err
  if claims, ok = token.Claims.(*GoBlobFileIdClaims); ok && token.Valid:
    return claims, nil
  return nil, ErrInvalidClaims
```

### TLS Certificate Hot-Reload
```
NewCertWatcher(certFile, keyFile):
  cw = &certWatcher{certFile, keyFile, ...}
  cw.reload()  // initial load
  go cw.watchLoop()  // fsnotify or polling
  return cw

watchLoop():
  // watch for file modification events on certFile and keyFile
  // on event: cw.reload()

reload():
  cert, err = tls.LoadX509KeyPair(cw.certFile, cw.keyFile)
  if err: log.Error("failed to reload cert", "err", err); return
  cw.mu.Lock()
  cw.cert = &cert
  cw.mu.Unlock()

GetTLSConfig():
  return &tls.Config{
    GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error):
      cw.mu.RLock()
      defer cw.mu.RUnlock()
      return cw.cert, nil
  }
```

## 8. Persistence Model

JWT signing keys are read-only secrets in `security.toml`. They are not changed at runtime.

TLS certificates are files on disk. The `certWatcher` monitors them and reloads on change (zero-downtime rotation).

## 9. Concurrency Model

- `Guard.whiteListMu` (`sync.RWMutex`): RLock for every request; Lock only for `UpdateWhiteList` (rare)
- `certWatcher.mu` (`sync.RWMutex`): RLock for `GetTLSConfig` on every TLS handshake; Lock only on file reload
- JWT operations are stateless and goroutine-safe (no shared mutable state)

## 10. Configuration

Loaded from `security.toml` via Viper (see Configuration System plan):

```toml
[jwt.signing]
  key = "your_volume_signing_secret"
  expires_after_seconds = 10

[jwt.signing.read]
  key = "your_volume_read_secret"
  expires_after_seconds = 60

[jwt.filer_signing]
  key = "your_filer_signing_secret"
  expires_after_seconds = 10

[guard]
  white_list = "10.0.0.0/8,192.168.0.0/16"

[https.master]
  cert = "/etc/goblob/certs/master.crt"
  key  = "/etc/goblob/certs/master.key"
  ca   = "/etc/goblob/certs/ca.crt"

[grpc.master]
  cert = "/etc/goblob/certs/grpc-master.crt"
  key  = "/etc/goblob/certs/grpc-master.key"
  ca   = "/etc/goblob/certs/grpc-ca.crt"
```

If `security.toml` is absent, all security mechanisms are disabled (open access).

## 11. Observability

- Security config loading logged at INFO: `"security.toml loaded: jwt=enabled whitelist=10.0.0.0/8,..."`
- JWT verification failures logged at WARN: `"JWT verification failed addr=%s err=%v"`
- Whitelist rejection logged at WARN: `"IP not in whitelist remote=%s"`
- Cert reload events logged at INFO: `"TLS certificate reloaded"`
- Failed cert reload logged at ERROR

## 12. Testing Strategy

- **Unit tests**:
  - `TestGuardPassThroughNoSecurity`: no config, assert all requests pass
  - `TestGuardWhitelistExactIP`: configure whitelist with one IP, assert only that IP passes
  - `TestGuardWhitelistCIDR`: configure CIDR range, test IPs inside and outside
  - `TestGuardJwtRequired`: configure key, no IP in whitelist, valid JWT passes, missing JWT rejected
  - `TestGuardJwtExpired`: generate expired token, assert rejected
  - `TestGuardJwtWrongKey`: sign with key A, verify with key B, assert rejected
  - `TestGenJwtForVolumeServerEmptyKey`: assert returns ""
  - `TestVerifyJwtEmptyKey`: assert all tokens accepted when key empty
  - `TestGetActualRemoteHostIgnoresXFF`: request with X-Forwarded-For, assert returns RemoteAddr
  - `TestCertWatcherReload`: write new cert file, trigger reload, assert new cert returned
- **Fuzz tests**:
  - `FuzzVerifyJwt`: feed arbitrary strings, assert no panic (only error)

## 13. Open Questions

None.
