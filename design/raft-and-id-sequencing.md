# Raft and ID Sequencing

### Purpose

Keep master leadership and ID allocation consistent across replicas by replicating high-water marks (`max_file_id`, `max_volume_id`, `topology_id`) through Raft.

### Scope

**In scope:**
- Raft server lifecycle in `goblob/raft/raft_server.go` and config in `goblob/raft/config.go`.
- Master FSM command apply/snapshot/restore in `goblob/raft/fsm.go`.
- Event bus for state and leadership changes in `goblob/raft/events.go`.
- File and raft sequencer implementations in `goblob/sequence/file_sequencer.go` and `goblob/sequence/raft_sequencer.go`.

**Out of scope:**
- Master HTTP route semantics.
- Topology placement logic itself.

### Primary User Flow

1. Master leader receives assign requests and needs new file IDs.
2. Sequencer computes new max ID and applies `MaxFileIdCommand` through Raft before handing out IDs.
3. FSM commits command, updates state, and publishes events.
4. Followers restore committed state through log replay/snapshot and maintain consistent high-water marks.
5. On leadership change, master sync logic (barrier + topology ID initialization) uses Raft state to avoid divergence.

### System Flow

1. `NewRaftServer` validates config, initializes transport + bolt stores + snapshots, and bootstraps cluster if no existing state.
2. `Apply(cmd,timeout)` encodes command JSON (`{type,payload}`) and submits to HashiCorp Raft.
3. `MasterFSM.Apply` decodes command type and mutates state (`maxFileId`, `maxVolumeId`, `topologyId`), then publishes `StateEvent`.
4. Sequencer path:
   - `RaftSequencer.NextFileId(count)` calculates `newMax`.
   - applies `MaxFileIdCommand` to Raft (warns and continues locally if apply fails).
   - allocates IDs via wrapped `FileSequencer`.
5. Snapshot path:
   - `MasterFSM.Snapshot` serializes state.
   - `Restore` hydrates state and emits single `EventRestored` with all fields.

```
Assign request
  -> RaftSequencer.NextFileId
     -> Raft.Apply(MaxFileIdCommand)
        -> FSM.Apply -> state update + event publish
     -> FileSequencer returns start ID

Restart/failover
  -> FSM.Restore(snapshot)
     -> publish EventRestored
```

### Data Model

- Raft config (`RaftConfig`): `NodeId`, `BindAddr`, `MetaDir`, `Peers[]`, timeouts, snapshot settings.
- FSM state (`fsmState` / `ClusterState`):
  - `max_file_id (uint64)`
  - `max_volume_id (uint32)`
  - `topology_id (string)`
- Sequencer persistent file (`max_needle_id` in `FileSequencer.DataDir`): stores max allocated ID as decimal text.
- Event bus message (`StateEvent`): `Kind`, optional scalar fields, `IsLeader`.

### Interfaces and Contracts

- `raft.RaftServer` interface contracts:
  - leadership queries: `IsLeader()`, `LeaderAddress()`.
  - mutation: `Apply(cmd,timeout)`.
  - cluster membership: `AddPeer(addr)`, `RemovePeer(addr)`.
  - observability: `Stats()`.
  - lifecycle: `Shutdown()`.
  - event subscription: `Subscribe(name,bufSize) <-chan StateEvent`, `Unsubscribe(name)` — named observers receive events fan-out without blocking the publisher (full channels drop).
- `sequence.RaftApplier` interface (in `goblob/sequence/raft_sequencer.go`):
  - `Apply(cmd RaftCommand, timeout time.Duration) error` — subset of `RaftServer` used by sequencer to avoid a full dependency on the Raft package.
- Supported replicated command types:
  - `max_file_id`, `max_volume_id`, `topology_id`.
- Sequencer contract:
  - `NextFileId(count)` returns start of contiguous ID range `[start, start+count-1]`.
  - `SetMax(maxId)` monotonically advances ID floor.
  - `SyncToRaft()` forces an immediate `MaxFileIdCommand` apply for the current max — called on leader startup to resync followers after a crash.

### Dependencies

**Internal modules:**
- `goblob/raft` used by master runtime.
- `goblob/sequence` used by master assign path.
- `goblob/obs` metrics (`RaftApplyErrors`, raft gauges).

**External services/libraries:**
- HashiCorp Raft and BoltDB stores.
- Disk write access for `MetaDir` and sequencer data directory.

### Failure Modes and Edge Cases

- `Apply` on follower returns `not leader` error.
- Raft apply failure in `RaftSequencer.NextFileId` logs warning and continues local allocation (potentially requiring later reconciliation).
- Corrupt snapshot or log entry payload returns decode errors and can block state restore.
- File sequencer persistence write failures are logged; IDs remain unique in-memory but crash can waste a larger ID range.
- `AddPeer`/`RemovePeer` errors propagate directly from Raft futures.

### Observability and Debugging

- Raft stats exported by master runtime loop (`commit_index`, `last_log_index`, `last_snapshot_index`, `num_peers`, `last_contact`).
- Error counter: `goblob_raft_apply_errors_total` increments on sequencer Raft apply failure.
- Debug entry points:
  - `raft_server.go:Apply` for command submission failures.
  - `fsm.go:Apply` for command decode and state transitions.
  - `raft_sequencer.go:NextFileId` for allocation-vs-sync behavior.

### Risks and Notes

- `RaftSequencer` fail-open behavior prioritizes availability over strict replication success for each allocation attempt.
- Peer address conventions assume related port offsets in other layers; inconsistent addressing can break leader targeting.
- Event bus drops events when subscriber channels are full (non-blocking publish), so observers must handle missed transitions.

Changes:

