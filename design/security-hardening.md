# Security Hardening

### Purpose

Enforce request authorization and baseline HTTP hardening across services using IP whitelist checks, JWT verification, rate limiting, request size limits, and security headers.

### Scope

**In scope:**
- Guard authorization model in `goblob/security/guard.go`.
- JWT signing/verification helpers in `goblob/security/jwt.go`.
- Hardening middleware chain in `goblob/security/hardening.go` and related middleware files.
- TLS helper utilities in `goblob/security/tls.go`.

**Out of scope:**
- S3 IAM action authorization (handled in S3 IAM layer).
- Per-command access control outside shared middleware/guard usage.

### Primary User Flow

1. Service receives HTTP request.
2. Hardening middleware applies headers, audit log, size limits, and rate limiting.
3. Handler checks guard permissions (`Allowed` or `FilerAllowed`) for IP whitelist and optional JWT.
4. Unauthorized requests return `401`/`403`; authorized requests proceed to business logic.

### System Flow

1. Runtime wrappers (`command/runtime.go` and command-specific paths) wrap mux handlers with `security.ApplyHardening`.
2. `ApplyHardening` composes middleware order:
   - `SecurityHeaders`
   - `AuditLog`
   - `MaxBytesSizeLimit`
   - rate limiter middleware (`NewRateLimiter`)
3. Guard checks:
   - `Allowed(r,requiresJWT)` verifies caller IP against whitelist CIDR/IP list.
   - if JWT required and signing key configured, bearer token is validated with HS256.
   - `FilerAllowed` uses `filerKey` for filer-specific token enforcement.
4. Security reload flow:
   - master/volume/filer commands call `ReloadSecurityConfig` with values loaded from `security.toml`.
   - guard whitelist/signing keys are updated in-place.

```
HTTP request
  -> ApplyHardening middleware stack
  -> handler auth check (Guard)
     -> [denied] 401/403
     -> [allowed] business handler
```

### Data Model

- `Guard` state:
  - `whiteList []string` + compiled `whiteNets []*net.IPNet`.
  - `signingKey string` (general JWT), `filerKey string` (filer-specific JWT).
- JWT claims used by `SignJWT`:
  - `exp` unix timestamp only (HS256 signing).
- Rate limiter state (`RateLimiter`):
  - per-IP token-bucket entries `{tokens,last,lastSeen}` in memory.
- Config model (`config.SecurityConfig`):
  - `jwt.signing`, `jwt.filer_signing`, `guard.white_list`, TLS blocks, CORS blocks.

### Interfaces and Contracts

- Guard methods:
  - `Allowed(*http.Request, requiresJWT bool) bool`.
  - `FilerAllowed(*http.Request) bool`.
  - dynamic updates: `SetWhiteList`, `SetSigningKey`, `SetFilerKey`.
- JWT helpers:
  - `SignJWT(key, expiresAfterSec)` -> token.
  - `VerifyJWT(token, key)` -> claims or error.
- Middleware contracts:
  - `ApplyHardening(http.Handler, HardeningOption) http.Handler`.
  - `MaxBytesSizeLimit` enforces max body bytes via `http.MaxBytesReader`.
  - `NewRateLimiter(rps, burst)` enforces per-remote-host limits.

### Dependencies

**Internal modules:**
- Used by master/volume/filer/webdav command runtime wrappers.
- Uses `config.SecurityConfig` for live reload input.

**External services/libraries:**
- JWT parsing/signing via `github.com/golang-jwt/jwt/v5`.
- TLS and x509 operations from Go crypto packages.

### Failure Modes and Edge Cases

- Empty JWT signing key with `requiresJWT=true` denies requests expecting tokens.
- Malformed `Authorization` header or invalid signature returns unauthorized behavior.
- Whitelist parse tolerates invalid entries by skipping unparseable CIDRs/IPs.
- Rate limiter uses in-memory map; process restart resets historical limits.
- `CheckCertExpiry` currently returns boolean based on date comparison but does not expose days-remaining details.

### Observability and Debugging

- Audit logs emit for errors, mutations, or auth-bearing requests with fields: method, path, status, remote host, duration.
- Debug points:
  - `guard.go:Allowed` for auth decisions.
  - `rate_limit.go:allow` for throttle decisions.
  - `hardening.go:ApplyHardening` for middleware ordering.
- No dedicated security metrics counters are currently defined.

### Risks and Notes

- Default guard behavior allows all IPs when whitelist is empty.
- Rate limiting keying on remote host/IP can be inaccurate behind proxies if forwarding headers are not trustworthy.
- JWT claims are minimal (`exp` only); no audience/subject constraints are enforced in shared helper.

Changes:

