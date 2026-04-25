package consensus

import (
	"io"
	"time"
)

// Command is a domain command that can be replicated through a consensus log.
// Consensus engines only require a stable command type and encoded payload.
type Command interface {
	Type() string
	Encode() ([]byte, error)
}

// EventKind identifies a consensus or replicated-state event.
type EventKind uint8

const (
	// EventLeaderChange is published when leadership changes.
	EventLeaderChange EventKind = iota
)

// Event is an immutable description of a consensus runtime event.
type Event struct {
	Kind     EventKind
	IsLeader bool // valid for EventLeaderChange
}

// Engine is the runtime boundary GoBlob components use for replicated
// control-plane state. Implementations may be backed by HashiCorp Raft today or
// a native engine later.
type Engine interface {
	IsLeader() bool
	// LeaderAddress returns the client-facing address of the current leader, or
	// an empty string when the leader is unknown.
	LeaderAddress() string
	// Apply submits a command to the replicated log and waits for consensus.
	Apply(cmd Command, timeout time.Duration) error
	// Barrier waits until all log entries up to this point are applied.
	Barrier(timeout time.Duration) error
	AddPeer(addr string) error
	RemovePeer(addr string) error
	Stats() map[string]string
	Shutdown() error
	Subscribe(name string, bufSize int) <-chan Event
	Unsubscribe(name string)
}

// FSM is the engine-neutral state machine contract used by future consensus
// engines. The current HashiCorp adapter still uses hashicorp/raft.FSM directly.
type FSM interface {
	Apply(log []byte) any
	Snapshot() (Snapshot, error)
	Restore(io.ReadCloser) error
}

// Snapshot is the engine-neutral snapshot contract paired with FSM.
type Snapshot interface {
	Persist(SnapshotSink) error
	Release()
}

// SnapshotSink is the write side for a durable FSM snapshot.
type SnapshotSink interface {
	io.Writer
	Close() error
	Cancel() error
}
