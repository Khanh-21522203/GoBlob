# Feature: Raft Consensus Layer

## 1. Purpose

The Raft Consensus Layer provides leader election and log replication across Master Server nodes, ensuring that the GoBlob cluster has a single, authoritative leader at all times. It guarantees that:

- Only one master node acts as leader and accepts write requests
- The sequencer state (max file ID) is replicated to all master nodes
- The topology's max volume ID is replicated (so it survives full cluster restarts)
- Split-brain is prevented even under network partitions

GoBlob requires an odd number of master nodes (1, 3, or 5) to form a Raft quorum.

## 2. Responsibilities

- Implement leader election via the Raft consensus algorithm
- Replicate critical state changes as Raft log entries (commands)
- Notify the rest of the system when leadership changes (leader gain/loss callbacks)
- Ensure the Raft log and snapshots are persisted to the master's metadata directory
- Provide a `IsLeader()` check for request routing
- Provide a `LeaderAddress()` for follower-to-leader redirect
- Handle single-master mode (`-peers=none`) without requiring quorum
- Expose a Raft barrier operation to wait for log replay completion

## 3. Non-Responsibilities

- Does not replicate topology tree (rebuilt from heartbeats)
- Does not replicate volume data (volumes are replicated by the Replication Engine)
- Does not provide a generic distributed KV store; only replicates specific GoBlob commands
- Does not perform DNS-based peer discovery (that is handled by configuration/SRV records)

## 4. Architecture Design

```
Master 1 (Leader)         Master 2 (Follower)     Master 3 (Follower)
+----------------+        +----------------+       +----------------+
| RaftServer     |        | RaftServer     |       | RaftServer     |
|   IsLeader=true|        |   IsLeader=false       |   IsLeader=false
|   Log: [1,2,3] |<------>|   Log: [1,2,3] |<----->|   Log: [1,2,3] |
|   State: 1000  |        |   State: 1000  |       |   State: 1000  |
+-------+--------+        +----------------+       +----------------+
        |
  Leader handles:
  - /dir/assign
  - volume growth
  - sequencer.NextFileId()

Followers:
  - Proxy non-read requests to leader
  - Can answer /dir/lookup (read-only, eventually consistent)
```

### Log Commands
GoBlob defines the following Raft log commands:

| Command | Payload | Purpose |
|---------|---------|---------|
| `MaxVolumeIdCommand` | `{MaxFileId uint64}` | Advance the sequencer's max ID |
| `MaxVolumeIdCommand` | `{VolumeId uint32}` | Record highest allocated VolumeId |
| `TopologyIdCommand` | `{TopologyId string}` | Set the cluster topology identity |

## 5. Core Data Structures (Go)

```go
package raft

import (
    "sync"
    "encoding/json"
)

// RaftServer is the interface the rest of the system uses to interact with Raft.
// It abstracts over the underlying Raft library (goblob/raft or hashicorp/raft).
type RaftServer interface {
    // IsLeader reports whether this node is currently the Raft leader.
    IsLeader() bool
    // LeaderAddress returns the gRPC address of the current leader.
    // Returns "" if leader is unknown.
    LeaderAddress() string
    // Apply submits a command to the Raft log and waits for consensus.
    // Returns an error if not leader, or if consensus times out.
    Apply(cmd RaftCommand, timeout time.Duration) error
    // Barrier waits until all entries in the Raft log up to the current point
    // have been applied to the FSM. Used after leader election to ensure
    // topology ID is set before handling requests.
    Barrier(timeout time.Duration) error
    // AddPeer adds a new peer to the Raft cluster.
    AddPeer(addr string) error
    // RemovePeer removes a peer from the Raft cluster.
    RemovePeer(addr string) error
    // Stats returns diagnostic information about the Raft state.
    Stats() map[string]string
    // Shutdown stops the Raft server gracefully.
    Shutdown() error
}

// RaftCommand is the common interface for all state machine commands.
type RaftCommand interface {
    // Type returns a string identifier for this command type.
    Type() string
    // Encode serializes the command to bytes for the Raft log.
    Encode() ([]byte, error)
}

// MaxFileIdCommand advances the sequencer's max file ID.
type MaxFileIdCommand struct {
    MaxFileId uint64 `json:"max_file_id"`
}
func (c MaxFileIdCommand) Type() string { return "max_file_id" }
func (c MaxFileIdCommand) Encode() ([]byte, error) { return json.Marshal(c) }

// MaxVolumeIdCommand records the highest VolumeId allocated.
type MaxVolumeIdCommand struct {
    MaxVolumeId uint32 `json:"max_volume_id"`
}
func (c MaxVolumeIdCommand) Type() string { return "max_volume_id" }
func (c MaxVolumeIdCommand) Encode() ([]byte, error) { return json.Marshal(c) }

// TopologyIdCommand sets the cluster's unique identity.
type TopologyIdCommand struct {
    TopologyId string `json:"topology_id"`
}
func (c TopologyIdCommand) Type() string { return "topology_id" }
func (c TopologyIdCommand) Encode() ([]byte, error) { return json.Marshal(c) }

// LogEntry wraps a command with type information for the Raft FSM.
type LogEntry struct {
    Type    string          `json:"type"`
    Payload json.RawMessage `json:"payload"`
}

// FSM (Finite State Machine) is applied to each committed Raft log entry.
// The master server implements this interface to update its state.
type FSM interface {
    // Apply is called by the Raft library for each committed log entry.
    Apply(log *raft.Log) interface{}
    // Snapshot creates a point-in-time snapshot of the FSM state.
    Snapshot() (raft.FSMSnapshot, error)
    // Restore applies a snapshot, replacing current FSM state.
    Restore(snapshot io.ReadCloser) error
}

// MasterFSM implements raft.FSM for the GoBlob master state.
type MasterFSM struct {
    // maxFileId is the highest file ID ever issued.
    maxFileId uint64
    // maxVolumeId is the highest volume ID ever allocated.
    maxVolumeId uint32
    // topologyId is the cluster identity.
    topologyId string
    mu sync.Mutex
    // callbacks invoked after applying commands
    onMaxFileIdUpdate   func(uint64)
    onMaxVolumeIdUpdate func(uint32)
    onTopologyIdSet     func(string)
}

// RaftConfig holds configuration for the Raft layer.
type RaftConfig struct {
    // NodeId is this node's identifier (typically "ip:port").
    NodeId string
    // BindAddr is the Raft transport bind address.
    BindAddr string
    // MetaDir is the directory for Raft log and snapshot storage.
    MetaDir string
    // Peers is the list of all master node addresses (including self).
    Peers []string
    // HeartbeatTimeout is how long a follower waits before starting an election. Default: 1s.
    HeartbeatTimeout time.Duration
    // ElectionTimeout is how long a candidate waits for votes. Default: 1s.
    ElectionTimeout time.Duration
    // CommitTimeout is max time to wait for a commit. Default: 50ms.
    CommitTimeout time.Duration
    // MaxAppendEntries is the max log entries per AppendEntries RPC. Default: 64.
    MaxAppendEntries int
    // SnapshotInterval is how often to create snapshots. Default: 120s.
    SnapshotInterval time.Duration
    // SnapshotThreshold is log entries between snapshots. Default: 8192.
    SnapshotThreshold uint64
    // SingleMode skips quorum wait (for -peers=none). Default: false.
    SingleMode bool
}
```

## 6. Public Interfaces

```go
package raft

// NewRaftServer creates and starts a Raft server.
// The FSM is the application state machine to apply committed entries to.
// onLeaderChange is called whenever leadership status changes (true = became leader).
func NewRaftServer(cfg RaftConfig, fsm FSM, onLeaderChange func(isLeader bool)) (RaftServer, error)

// RaftServer interface (see §5 above)

// MasterFSM constructor
func NewMasterFSM(
    onMaxFileIdUpdate func(uint64),
    onMaxVolumeIdUpdate func(uint32),
    onTopologyIdSet func(string),
) *MasterFSM
```

## 7. Internal Algorithms

### Startup and Bootstrap
```
NewRaftServer(cfg):
  // 1. Open Raft log store (BoltDB or in-memory for tests)
  logStore = openBoltDB(cfg.MetaDir + "/raft-log.db")
  stableStore = openBoltDB(cfg.MetaDir + "/raft-stable.db")
  snapshotStore = raft.NewFileSnapshotStore(cfg.MetaDir, 3, os.Stderr)

  // 2. Create TCP transport
  transport = raft.NewTCPTransport(cfg.BindAddr, ...)

  // 3. Create Raft config
  raftCfg = raft.DefaultConfig()
  raftCfg.LocalID = cfg.NodeId
  raftCfg.HeartbeatTimeout = cfg.HeartbeatTimeout
  raftCfg.ElectionTimeout = cfg.ElectionTimeout
  ...

  // 4. Bootstrap if no existing state
  if !hasExistingState(logStore, stableStore, snapshotStore):
    if cfg.SingleMode:
      raft.BootstrapCluster(raftCfg, logStore, stableStore, snapshotStore,
        transport, raft.Configuration{Servers: [{Voter, cfg.NodeId, cfg.BindAddr}]})
    else:
      configuration = {Servers: [{Voter, peer, peer} for peer in cfg.Peers]}
      raft.BootstrapCluster(raftCfg, ..., configuration)

  // 5. Create Raft instance
  r = raft.NewRaft(raftCfg, fsm, logStore, stableStore, snapshotStore, transport)

  // 6. Monitor leadership changes
  go func():
    for isLeader := range r.LeaderCh():
      onLeaderChange(isLeader)

  return &raftServerImpl{r: r}, nil
```

### Log Application (FSM.Apply)
```
MasterFSM.Apply(log):
  entry = unmarshalLogEntry(log.Data)
  switch entry.Type:
  case "max_file_id":
    cmd = unmarshal MaxFileIdCommand(entry.Payload)
    mu.Lock()
    if cmd.MaxFileId > maxFileId:
      maxFileId = cmd.MaxFileId
      onMaxFileIdUpdate(maxFileId)
    mu.Unlock()

  case "max_volume_id":
    cmd = unmarshal MaxVolumeIdCommand(entry.Payload)
    mu.Lock()
    if cmd.MaxVolumeId > maxVolumeId:
      maxVolumeId = cmd.MaxVolumeId
      onMaxVolumeIdUpdate(maxVolumeId)
    mu.Unlock()

  case "topology_id":
    cmd = unmarshal TopologyIdCommand(entry.Payload)
    mu.Lock()
    topologyId = cmd.TopologyId
    onTopologyIdSet(topologyId)
    mu.Unlock()
```

### Leader Change Handler (Master Server)
When this node becomes leader:
1. Call `Barrier(10s)` to ensure log is fully applied
2. Generate topology ID if not set: `Apply(TopologyIdCommand{uuid.New()}, 5s)`
3. Start the `ProcessGrowRequest` goroutine
4. Start the `startAdminScripts` goroutine

When this node loses leadership:
1. Stop the grow request processing goroutine
2. Stop admin scripts goroutine
3. Log at INFO: `"lost leadership, stopping volume growth and maintenance"`

### Snapshot
```
MasterFSM.Snapshot():
  mu.Lock()
  state = {maxFileId, maxVolumeId, topologyId}
  mu.Unlock()
  return &fsmSnapshot{data: json.Marshal(state)}

fsmSnapshot.Persist(sink):
  sink.Write(data)
  sink.Close()

MasterFSM.Restore(snapshot):
  data = readAll(snapshot)
  state = json.Unmarshal(data)
  mu.Lock()
  maxFileId = state.maxFileId
  maxVolumeId = state.maxVolumeId
  topologyId = state.topologyId
  mu.Unlock()
  // Invoke callbacks to sync dependent state
  onMaxFileIdUpdate(maxFileId)
  onMaxVolumeIdUpdate(maxVolumeId)
  if topologyId != "": onTopologyIdSet(topologyId)
```

### Follower Request Redirect
Non-leader masters reverse-proxy requests that require leadership (e.g., `POST /dir/assign`):
```
if !raftServer.IsLeader():
  leaderAddr = raftServer.LeaderAddress()
  if leaderAddr == "":
    return 503 "No leader elected yet"
  http.Redirect(w, r, "http://"+leaderAddr+r.URL.Path, http.StatusTemporaryRedirect)
  return
```

## 8. Persistence Model

### Raft Data Directory Structure
```
$meta_dir/
  raft-log.db        # BoltDB: Raft log entries
  raft-stable.db     # BoltDB: stable store (current term, voted for)
  snapshots/
    <term>-<index>/
      state.bin      # FSM snapshot binary
      meta.json      # snapshot metadata
```

Raft handles all log persistence automatically. GoBlob only provides the directory path.

### Recovery After Full Cluster Restart
1. Raft replays the log from the last snapshot
2. `MasterFSM.Apply` restores `maxFileId` and `maxVolumeId`
3. Sequencer calls `SetMax(maxFileId)` to avoid ID reuse
4. Topology rebuilds from incoming heartbeats

## 9. Concurrency Model

The Raft library handles its own concurrency internally. From GoBlob's perspective:

- `RaftServer.Apply()` is safe for concurrent calls; blocks until consensus
- `RaftServer.IsLeader()` is a lock-free atomic read
- `MasterFSM` protects its state with a `sync.Mutex`
- The `onLeaderChange` callback is invoked from the Raft library's internal goroutine; it must not block

## 10. Configuration

```go
type RaftConfig struct {
    NodeId           string
    BindAddr         string
    MetaDir          string
    Peers            []string
    HeartbeatTimeout time.Duration `default:"1s"`
    ElectionTimeout  time.Duration `default:"1s"`
    CommitTimeout    time.Duration `default:"50ms"`
    MaxAppendEntries int           `default:"64"`
    SnapshotInterval time.Duration `default:"120s"`
    SnapshotThreshold uint64       `default:"8192"`
    SingleMode       bool          `default:"false"`
}
```

## 11. Observability

- `obs.MasterLeadershipGauge.Set(1)` when becoming leader; `.Set(0)` when losing it
- Raft stats (term, last log index, commit index, applied index) exposed via `GET /raft/stats` debug endpoint
- Leadership transitions logged at INFO with term and node ID
- `Apply` timeouts logged at WARN with command type and duration

## 12. Testing Strategy

- **Unit tests**:
  - `TestSingleModeStartup`: start Raft with `SingleMode=true`, assert immediately becomes leader
  - `TestFSMApplyMaxFileId`: apply command, assert callback invoked with new value
  - `TestFSMSnapshot`: create snapshot, restore to new FSM, assert state equal
  - `TestFollowerRedirect`: non-leader server, assert redirect to leader address
- **Integration tests** (3-node in-process cluster):
  - `TestLeaderElection`: start 3 Raft nodes, assert exactly one leader
  - `TestLogReplication`: leader applies command, assert all followers have same state
  - `TestLeaderFailover`: stop leader, assert new leader elected within 5s, state preserved
  - `TestPartitionTolerance`: partition 1 node, assert remaining 2 still elect leader
- All integration tests use in-memory transport for speed

## 13. Open Questions

None.
