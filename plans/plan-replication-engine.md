# Feature: Replication Engine

## 1. Purpose

The Replication Engine ensures that every blob written to a primary volume server is synchronously copied to all replica volume servers before the write is acknowledged to the client. It implements GoBlob's strong consistency guarantee: a successful `PUT` response means all replicas have durable copies.

It runs inside the Volume Server, wrapping the local needle write with a set of parallel gRPC `WriteNeedle` calls to peer servers.

## 2. Responsibilities

- After a successful local write, issue parallel gRPC `WriteNeedle` calls to all replica servers
- Wait for all replicas to confirm (synchronous replication)
- Return error if any replica fails (the client must retry; data may be partially replicated)
- Pass the file data only once: stream the needle body to each replica without re-reading disk
- Skip re-replication on writes that are themselves replicas (avoid infinite loops)
- Consult topology (via master) to find replica locations for a given VolumeId

## 3. Non-Responsibilities

- Does not implement async or eventually-consistent replication
- Does not repair replicas that fall behind (that is `volume.fix.replication` in the admin shell)
- Does not perform checksumming or comparison of replica data (trusts gRPC transport)
- Does not decide replication factor (that is part of volume creation via ReplicaPlacement)

## 4. Architecture Design

```
Client
  |
  | PUT /{fid}
  v
Volume Server (primary)
  |
  | 1. WriteNeedle to local volume (.dat file)
  |
  | 2. ReplicatedWrite():
  |    +-- gRPC WriteNeedle --> Volume Server (replica 1)
  |    +-- gRPC WriteNeedle --> Volume Server (replica 2)
  |    (parallel, wait for both)
  |
  | 3. Return success to client only after all replicas confirm
  v
HTTP 201 Created
```

### Replica Location Discovery
The primary volume server knows from the master's topology which other servers hold the same volume. This information is included in the heartbeat response (`VolumeLocationList` sent via a separate lookup, or cached locally). When a write arrives, the volume server:
1. Looks up `VolumeId → [ServerAddress]` from its local replica location cache
2. Strips its own address (no self-replication)
3. Issues writes to the remaining addresses

The replica location cache is populated from the `KeepConnected` heartbeat response.

## 5. Core Data Structures (Go)

```go
package replication

import (
    "sync"
    "goblob/core/types"
    "goblob/storage/needle"
)

// ReplicaLocations caches the set of replica servers for each volume.
// Updated from heartbeat responses.
type ReplicaLocations struct {
    mu        sync.RWMutex
    // locations maps VolumeId to the list of ALL servers holding that volume,
    // including the local server.
    locations map[types.VolumeId][]types.ServerAddress
}

// ReplicateRequest contains everything needed to replicate a needle.
type ReplicateRequest struct {
    VolumeId    types.VolumeId
    Needle      *needle.Needle
    // NeedleBytes is the pre-serialized needle (to avoid re-encoding per replica).
    NeedleBytes []byte
    // JWTToken is the write authorization token for the replica servers.
    JWTToken    string
}

// ReplicateResult holds the outcome for a single replica.
type ReplicateResult struct {
    ServerAddr types.ServerAddress
    Err        error
}
```

## 6. Public Interfaces

```go
package replication

// Replicator performs synchronous replication to peer volume servers.
type Replicator interface {
    // ReplicatedWrite writes the needle to all replica servers for the given volume.
    // selfAddr is this server's address (excluded from replica list).
    // Returns error if any replica fails.
    ReplicatedWrite(
        ctx context.Context,
        req ReplicateRequest,
        replicaAddrs []types.ServerAddress,
    ) error
}

// NewHTTPReplicator creates a Replicator that sends needle data via HTTP PUT.
func NewHTTPReplicator(client *http.Client, jwtSigningKey []byte) Replicator

// ReplicaLocations methods
func NewReplicaLocations() *ReplicaLocations
func (rl *ReplicaLocations) Update(vid types.VolumeId, addrs []types.ServerAddress)
func (rl *ReplicaLocations) Get(vid types.VolumeId) []types.ServerAddress
```

## 7. Internal Algorithms

### ReplicatedWrite
```
ReplicatedWrite(ctx, req, replicaAddrs):
  if len(replicaAddrs) == 0: return nil  // no replicas needed

  type result struct{ addr string; err error }
  results = make(chan result, len(replicaAddrs))

  for each addr in replicaAddrs:
    go func(target ServerAddress):
      err = writeToReplica(ctx, target, req)
      results <- result{target, err}
    (addr)

  // Collect all results
  var errs []error
  for i := 0; i < len(replicaAddrs); i++:
    r = <-results
    if r.err != nil:
      errs = append(errs, fmt.Errorf("replica %s: %w", r.addr, r.err))

  if len(errs) > 0:
    return fmt.Errorf("replication failed: %v", errs)
  return nil
```

### writeToReplica (HTTP)
Replica writes use HTTP PUT to keep the protocol simple and consistent with client uploads:
```
writeToReplica(ctx, targetAddr, req):
  url = "http://" + targetAddr + "/" + req.VolumeId.String() + "," + req.Needle.Id.String()
  httpReq = http.NewRequestWithContext(ctx, "PUT", url, bytes.NewReader(req.NeedleBytes))
  httpReq.Header.Set("Authorization", "Bearer " + req.JWTToken)
  httpReq.Header.Set("X-Replication", "true")  // prevents double-replication on target

  resp, err = client.Do(httpReq)
  if err: return err
  if resp.StatusCode != 201: return fmt.Errorf("replica returned %d", resp.StatusCode)
  return nil
```

The `X-Replication: true` header tells the target volume server to skip further replication.

### Replica Loop Prevention
The volume server write handler checks:
```
if r.Header.Get("X-Replication") == "true":
  // This is a replica write; store locally only, do not re-replicate
  writeLocalOnly(vid, needle)
  return
```

### Error Handling Strategy
All replicas must succeed. If any fail:
1. Return error to the volume server's HTTP handler
2. HTTP handler returns 500 to the client
3. Client must retry the entire write
4. The partial replication state (some replicas written, some not) will be resolved by `volume.fix.replication` (admin shell)

This is the same model as GoBlob: strong consistency with manual repair for partial failures.

### JWT Token for Replicas
The primary volume server uses the write JWT token it received from the client (forwarded via `Authorization` header). The JWT is bound to the FileId (`GoBlobFileIdClaims.Fid`), so it is valid on replica servers as well.

## 8. Persistence Model

No persistence in the replication layer itself. Each replica is a full, independent volume server with its own `.dat`/`.idx` files. The replication engine only handles the write-time transport.

## 9. Concurrency Model

Each replica write is issued in a separate goroutine. All goroutines write to a buffered result channel. The caller waits for all goroutines to complete before returning.

No shared mutable state within a single `ReplicatedWrite` call. The HTTP client is shared across calls; `net/http.Client` is safe for concurrent use.

`ReplicaLocations` uses `sync.RWMutex`: RLock for lookups (frequent, during every write), Lock only during heartbeat updates (infrequent).

## 10. Configuration

```go
type ReplicationConfig struct {
    // RequestTimeout is the per-replica HTTP request timeout. Default: 30s.
    RequestTimeout time.Duration `mapstructure:"request_timeout"`
    // MaxIdleConnsPerHost for the replica HTTP client. Default: 100.
    MaxIdleConnsPerHost int `mapstructure:"max_idle_conns_per_host"`
}
```

## 11. Observability

- Replication success logged at DEBUG per replica (too verbose for INFO)
- Replication failure logged at WARN: `"replication failed to %s for vid=%d: %v"`
- Metrics:
  - `obs.VolumeServerReplicationAttempts.WithLabelValues(vid).Inc()`
  - `obs.VolumeServerReplicationFailures.WithLabelValues(vid).Inc()` on failure
  - `obs.VolumeServerReplicationLatency.Observe(duration)` per replica write

## 12. Testing Strategy

- **Unit tests**:
  - `TestReplicatedWriteSuccess`: mock HTTP servers for 2 replicas, assert both receive the needle
  - `TestReplicatedWriteOneFailure`: 2 replicas, one returns 500, assert error returned
  - `TestReplicatedWriteNoReplicas`: empty replica list, assert returns nil (no-op)
  - `TestReplicaLoopPrevention`: send write with X-Replication header, assert local-only write
  - `TestReplicaLocationsUpdate`: update and read-back concurrently
- **Integration tests**:
  - `TestEndToEndReplication`: start 2 in-process volume servers, write to primary, read from replica

## 13. Open Questions

None.
