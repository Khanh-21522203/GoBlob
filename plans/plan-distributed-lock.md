# Feature: Distributed Lock Manager

## 1. Purpose

The Distributed Lock Manager (DLM) provides named, expiring distributed locks for the Filer server and other subsystems that need cross-process mutual exclusion. Locks are stored in the Filer's KV store (which is itself replicated across filer instances via `MetaAggregator`). The DLM is a field on the `Filer` struct and is used for: cross-filer atomic rename coordination and admin shell cluster lock.

This plan covers the DLM as exposed via the Filer gRPC API (`DistributedLock` / `DistributedUnlock` RPCs) and the in-process Go API used within the filer server itself.

## 2. Responsibilities

- **Named lock acquisition**: Try to acquire a lock by name; fail if held by another owner
- **Lock renewal**: Allow the current holder to extend a lock's expiry before it lapses
- **Lock release**: Remove a lock entry from the KV store
- **Expiry enforcement**: Locks not renewed within their TTL are automatically available to new acquirers
- **Renew token**: Issue an opaque token on acquisition; require it on renewal/release to prevent stale holders from accidentally releasing a lock they lost
- **gRPC service**: Expose `DistributedLock` / `DistributedUnlock` RPCs on the Filer gRPC port so that remote processes (admin shell) can use the DLM
- **In-process API**: Provide a Go-level `Lock` / `Unlock` / `RenewLock` for code running inside the filer process

## 3. Non-Responsibilities

- Does not implement the Raft consensus layer (that is `plan-raft-consensus`)
- Does not provide read/write locks or semaphores — only exclusive (mutex) locks
- Does not implement session-based automatic unlock on process death; callers must use TTL-based expiry
- Does not provide observing or waiting on a lock (callers poll with backoff)
- Does not manage volume data or file metadata
- Does not coordinate MQ broker balancer election — MQ is out of scope for v1
- Does not coordinate TUS upload sessions — TUS is out of scope for v1

## 4. Architecture Design

```
+--------------------------------------------------------------+
|                 DistributedLockManager                        |
+--------------------------------------------------------------+
|                                                               |
|  Filer gRPC port                                             |
|  DistributedLock(name, expireNs, renewToken, owner)          |
|  DistributedUnlock(name, renewToken)                         |
|       |                                                      |
|  +----+------------------+                                   |
|  | DistributedLockManager|                                   |
|  |  filerStore           |  <-- VirtualFilerStore            |
|  |  localLocks (cache)   |  (in-memory for same-process      |
|  |                       |   callers, avoids KV round-trip)  |
|  +----+------------------+                                   |
|       |                                                      |
|  Filer KV store                                              |
|  key:   "__lock__:<lockName>"                                |
|  value: LockEntry{Owner, ExpireAtNs, RenewToken}            |
+--------------------------------------------------------------+
```

### Lock State Machine

```
Not held                       Held (by A)
    |                              |
    | Lock(A, expireNs)            | Lock(A, expireNs, token)  <- renewal
    v                              v
Held (by A) --------expiry------> Not held
    |                              |
    | Unlock(A, token)             | Lock(B, ...) (after expiry)
    v                              v
Not held                       Held (by B)
```

## 5. Core Data Structures (Go)

```go
package filer

import (
    "sync"
    "time"
    "context"
)

// DistributedLockManager manages named distributed locks backed by the Filer KV store.
type DistributedLockManager struct {
    // filerStore is the underlying KV store for lock persistence.
    filerStore FilerStore

    // localLocks is an in-memory index of locks held by the local filer process.
    // Used to short-circuit KV reads for local callers.
    localLocks map[string]*lockEntry
    localMu    sync.Mutex

    logger *slog.Logger
}

// lockEntry is the KV value stored for each active lock.
type lockEntry struct {
    // Owner is an opaque string identifying the lock holder (e.g., "host:port:sessionId").
    Owner string

    // ExpireAtNs is the Unix nanosecond timestamp when this lock expires.
    ExpireAtNs int64

    // RenewToken is a random opaque string issued on acquisition.
    // The holder must present this token on renewal or release.
    // This prevents stale holders from accidentally releasing a lock they lost to expiry.
    RenewToken string
}

// LockResult is returned by TryLock.
type LockResult struct {
    // RenewToken is valid only when Acquired is true.
    RenewToken string

    // Acquired is true if the lock was taken or renewed.
    Acquired bool

    // CurrentOwner is the current lock holder if Acquired is false.
    CurrentOwner string
}
```

### KV Storage Format

```
Key format:   "__lock__:" + lockName
Value format: JSON-encoded lockEntry

Examples:
  "__lock__:admin_shell"             Admin shell distributed lock
  "__lock__:filer_rename:/path/to"   Filer rename atomic lock
```

## 6. Public Interfaces

```go
package filer

// NewDistributedLockManager creates a DLM using the given filer store.
func NewDistributedLockManager(store FilerStore, logger *slog.Logger) *DistributedLockManager

// TryLock attempts to acquire or renew the named lock.
//   name:       lock identifier
//   owner:      caller identity string (e.g., "host:port")
//   expireNs:   lock TTL in nanoseconds from now
//   renewToken: empty string for first acquisition; the token from a previous TryLock for renewal
//
// Returns LockResult{Acquired: true, RenewToken: <token>} on success.
// Returns LockResult{Acquired: false, CurrentOwner: <owner>} if held by another.
func (dlm *DistributedLockManager) TryLock(ctx context.Context, name, owner string, expireNs int64, renewToken string) (LockResult, error)

// Unlock releases the named lock.
// Returns ErrNotLockOwner if renewToken does not match the stored token.
func (dlm *DistributedLockManager) Unlock(ctx context.Context, name, renewToken string) error

// IsLocked reports whether the named lock is currently held (not expired) by any owner.
func (dlm *DistributedLockManager) IsLocked(ctx context.Context, name string) (bool, string, error)

// Errors
var (
    ErrLockHeldByOther = errors.New("lock held by another owner")
    ErrNotLockOwner    = errors.New("renewToken mismatch: not the lock owner")
    ErrLockNotFound    = errors.New("lock not found")
)
```

## 7. Internal Algorithms

### TryLock
```
TryLock(ctx, name, owner, expireNs, renewToken):
  key = []byte("__lock__:" + name)
  now = time.Now().UnixNano()

  // Read existing lock entry
  existing, err = dlm.filerStore.KvGet(ctx, key)

  if err == nil and existing != nil:
    lock = parseLockEntry(existing)

    // Is the lock still valid?
    if lock.ExpireAtNs > now:
      // Held by someone else?
      if lock.Owner != owner and lock.RenewToken != renewToken:
        return LockResult{Acquired: false, CurrentOwner: lock.Owner}, nil

      // Held by us (renewal) — check token
      if renewToken != "" and lock.RenewToken != renewToken:
        return LockResult{}, ErrNotLockOwner

      // Our lock: renew it (fall through to write)

    // If lock.ExpireAtNs <= now: lock has expired, can be stolen

  // Write new lock entry
  newToken = generateSecureToken() // 32 random bytes, hex encoded
  newEntry = lockEntry{
    Owner:      owner,
    ExpireAtNs: now + expireNs,
    RenewToken: newToken,
  }
  err = dlm.filerStore.KvPut(ctx, key, marshalLockEntry(newEntry))
  if err != nil: return LockResult{}, err

  return LockResult{Acquired: true, RenewToken: newToken}, nil
```

### Unlock
```
Unlock(ctx, name, renewToken):
  key = []byte("__lock__:" + name)
  now = time.Now().UnixNano()

  existing, err = dlm.filerStore.KvGet(ctx, key)
  if err != nil or existing == nil:
    return ErrLockNotFound

  lock = parseLockEntry(existing)

  // Check token
  if lock.RenewToken != renewToken:
    return ErrNotLockOwner

  // If already expired, still delete (cleanup)
  return dlm.filerStore.KvDelete(ctx, key)
```

### IsLocked
```
IsLocked(ctx, name):
  key = []byte("__lock__:" + name)
  now = time.Now().UnixNano()

  existing, err = dlm.filerStore.KvGet(ctx, key)
  if err != nil or existing == nil:
    return false, "", nil

  lock = parseLockEntry(existing)
  if lock.ExpireAtNs <= now:
    return false, "", nil  // expired

  return true, lock.Owner, nil
```

### gRPC DistributedLock handler (on filer gRPC server)
```
DistributedLock(ctx, req):
  result, err = dlm.TryLock(ctx, req.Name, req.Owner, req.ExpireNs, req.RenewToken)
  if err != nil:
    return nil, status.Errorf(codes.Internal, err.Error())
  if !result.Acquired:
    return &filer_pb.DistributedLockResponse{
      Error: fmt.Sprintf("lock held by %s", result.CurrentOwner),
    }, nil
  return &filer_pb.DistributedLockResponse{RenewToken: result.RenewToken}, nil

DistributedUnlock(ctx, req):
  err = dlm.Unlock(ctx, req.Name, req.RenewToken)
  if err != nil:
    return &filer_pb.DistributedUnlockResponse{Error: err.Error()}, nil
  return &filer_pb.DistributedUnlockResponse{}, nil
```

### Lock Expiry Cleanup (background goroutine)
```
// Optional: periodic cleanup of expired lock entries to avoid KV bloat.
// Locks expire automatically (callers ignore expired entries), but entries
// accumulate in KV. Clean up every hour.

go cleanExpiredLocks(ctx, dlm, interval=1*time.Hour):
  for:
    select:
    case <-time.After(interval):
      // Scan all lock keys (prefix "__lock__:")
      // This is KV-backend-specific; for LevelDB, use prefix scan.
      // Delete any entry where ExpireAtNs < now.
    case <-ctx.Done(): return
```

## 8. Persistence Model

Lock state is persisted in the Filer KV store:

```
Key prefix: "__lock__:"
Key format: "__lock__:<lockName>"
Value:      JSON { "owner": "...", "expire_at_ns": 1700000000000000000, "renew_token": "abc123..." }
```

**Durability**: Lock entries survive Filer restarts because the Filer KV store (LevelDB2 by default) is durable.

**Cross-filer consistency**: The Filer KV store is local per filer instance. For cross-filer locks, callers must target a single, stable filer address — not a load-balanced endpoint — to avoid split-brain.

**Expiry**: Locks are not automatically deleted on expiry; they remain as tombstone entries until explicitly unlocked or cleaned up by the background goroutine. Callers check `ExpireAtNs` before treating an entry as held.

## 9. Concurrency Model

| Mechanism | Usage |
|-----------|-------|
| `localMu sync.Mutex` | Protects `localLocks` in-memory index for same-process callers |
| Filer KV store | Thread-safe (LevelDB single writer; SQL backends use transactions) |
| No CAS (compare-and-swap) | The KV store does not provide atomic compare-and-swap; the DLM reads, checks, then writes. This creates a race window: two concurrent acquirers may both read "no lock" and both try to write. Mitigation: for high-contention locks, callers should use a retry loop with random jitter. For low-contention locks (the primary use case), the window is negligible. |

## 10. Configuration

```go
// No separate configuration struct — DLM behavior is controlled per-call.

// Recommended TTL values for common use cases:
const (
    // Admin shell lock: 1 hour (admin sessions are short-lived)
    AdminShellLockExpireNs = int64(1 * time.Hour)
)
```

## 11. Observability

- `obs.DLMLockAcquireTotal.WithLabelValues("ok"/"held_by_other"/"error").Inc()` per TryLock call
- `obs.DLMLockReleaseTotal.WithLabelValues("ok"/"not_owner"/"not_found").Inc()` per Unlock call
- `obs.DLMActiveLockCount.Set(count)` updated by the background cleanup goroutine
- Lock acquisition and release logged at DEBUG with lock name, owner, and TTL
- Expired lock cleanup logged at DEBUG with count of deleted entries

## 12. Testing Strategy

- **Unit tests**:
  - `TestTryLockFresh`: no existing entry, assert acquired with valid token
  - `TestTryLockRenewal`: acquire then renew with correct token, assert new token issued and expiry extended
  - `TestTryLockRenewalWrongToken`: acquire then renew with wrong token, assert `ErrNotLockOwner`
  - `TestTryLockContention`: acquire by A, try acquire by B (different owner), assert `Acquired=false` with correct `CurrentOwner`
  - `TestTryLockExpiry`: acquire with 1ns TTL, sleep, try acquire by B, assert acquired (expired lock stolen)
  - `TestUnlockSuccess`: acquire then unlock with correct token, assert KV entry deleted
  - `TestUnlockWrongToken`: acquire by A, unlock with wrong token, assert `ErrNotLockOwner`
  - `TestUnlockNotFound`: unlock non-existent lock, assert `ErrLockNotFound`
  - `TestIsLockedHeld`: acquire lock, assert IsLocked returns (true, owner, nil)
  - `TestIsLockedExpired`: acquire with 1ns TTL, sleep, assert IsLocked returns (false, "", nil)
  - `TestIsLockedNotFound`: assert IsLocked returns (false, "", nil) for unknown name
  - `TestCleanupExpiredLocks`: populate expired entries, run cleanup, assert KV entries deleted

- **Integration tests**:
  - `TestDLMEndToEnd`: start filer with LevelDB, acquire lock via gRPC, renew, release, verify via IsLocked
  - `TestDLMConcurrent`: 10 goroutines race to acquire the same lock, assert exactly one wins at a time

## 13. Open Questions

None.
