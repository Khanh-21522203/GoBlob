package raft

import (
	"encoding/json"
	"fmt"
	"io"
	"sync"

	"GoBlob/goblob/consensus"

	"github.com/hashicorp/raft"
)

type RaftCommand = consensus.Command

// MaxFileIdCommand advances the sequencer's max file ID.
// Applied whenever the master issues a batch of file IDs.
type MaxFileIdCommand struct {
	MaxFileId uint64 `json:"max_file_id"`
}

func (c MaxFileIdCommand) Type() string            { return "max_file_id" }
func (c MaxFileIdCommand) Encode() ([]byte, error) { return json.Marshal(c) }

// MaxVolumeIdCommand records the highest VolumeId ever allocated.
type MaxVolumeIdCommand struct {
	MaxVolumeId uint32 `json:"max_volume_id"`
}

func (c MaxVolumeIdCommand) Type() string            { return "max_volume_id" }
func (c MaxVolumeIdCommand) Encode() ([]byte, error) { return json.Marshal(c) }

// TopologyIdCommand sets the cluster's unique identity (UUID).
type TopologyIdCommand struct {
	TopologyId string `json:"topology_id"`
}

func (c TopologyIdCommand) Type() string            { return "topology_id" }
func (c TopologyIdCommand) Encode() ([]byte, error) { return json.Marshal(c) }

// LogEntry wraps a command with type info for the FSM.
type LogEntry struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

// ClusterState is a consistent point-in-time snapshot of FSM state.
// All fields are read under a single lock acquisition.
type ClusterState struct {
	MaxFileId   uint64
	MaxVolumeId uint32
	TopologyId  string
}

// MasterFSM implements the raft.FSM interface for the master server.
// It publishes StateEvents on every Apply/Restore; subscribers react asynchronously.
type MasterFSM struct {
	EventBus
	mu          sync.Mutex
	maxFileId   uint64
	maxVolumeId uint32
	topologyId  string
}

// NewMasterFSM creates a new MasterFSM with no callbacks.
// Callers subscribe to events via Subscribe after construction.
func NewMasterFSM() *MasterFSM {
	return &MasterFSM{EventBus: newEventBus()}
}

// Apply is called by the Raft library for every committed log entry.
func (m *MasterFSM) Apply(log *raft.Log) interface{} {
	var entry LogEntry
	if err := json.Unmarshal(log.Data, &entry); err != nil {
		return fmt.Errorf("failed to unmarshal log entry: %w", err)
	}

	var (
		evt    StateEvent
		notify bool
	)

	m.mu.Lock()
	switch entry.Type {
	case "max_file_id":
		var cmd MaxFileIdCommand
		if err := json.Unmarshal(entry.Payload, &cmd); err != nil {
			m.mu.Unlock()
			return fmt.Errorf("failed to unmarshal max_file_id: %w", err)
		}
		if cmd.MaxFileId > m.maxFileId {
			m.maxFileId = cmd.MaxFileId
			evt = StateEvent{Kind: EventMaxFileId, MaxFileId: m.maxFileId}
			notify = true
		}

	case "max_volume_id":
		var cmd MaxVolumeIdCommand
		if err := json.Unmarshal(entry.Payload, &cmd); err != nil {
			m.mu.Unlock()
			return fmt.Errorf("failed to unmarshal max_volume_id: %w", err)
		}
		if cmd.MaxVolumeId > m.maxVolumeId {
			m.maxVolumeId = cmd.MaxVolumeId
			evt = StateEvent{Kind: EventMaxVolumeId, MaxVolumeId: m.maxVolumeId}
			notify = true
		}

	case "topology_id":
		var cmd TopologyIdCommand
		if err := json.Unmarshal(entry.Payload, &cmd); err != nil {
			m.mu.Unlock()
			return fmt.Errorf("failed to unmarshal topology_id: %w", err)
		}
		m.topologyId = cmd.TopologyId
		evt = StateEvent{Kind: EventTopologyId, TopologyId: m.topologyId}
		notify = true

	default:
		m.mu.Unlock()
		return fmt.Errorf("unknown command type: %s", entry.Type)
	}
	m.mu.Unlock()

	// Publish after releasing the lock to avoid deadlock if subscribers call CommittedState.
	if notify {
		m.publish(evt)
	}
	return nil
}

// Snapshot creates a point-in-time snapshot of FSM state.
func (m *MasterFSM) Snapshot() (raft.FSMSnapshot, error) {
	m.mu.Lock()
	state := fsmState{
		MaxFileId:   m.maxFileId,
		MaxVolumeId: m.maxVolumeId,
		TopologyId:  m.topologyId,
	}
	m.mu.Unlock()

	data, err := json.Marshal(state)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal snapshot: %w", err)
	}
	return &masterFSMSnapshot{data: data}, nil
}

// Restore applies a snapshot, replacing all current FSM state.
func (m *MasterFSM) Restore(rc io.ReadCloser) error {
	defer rc.Close()

	data, err := io.ReadAll(rc)
	if err != nil {
		return fmt.Errorf("failed to read snapshot: %w", err)
	}

	var state fsmState
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("failed to unmarshal snapshot: %w", err)
	}

	m.mu.Lock()
	m.maxFileId = state.MaxFileId
	m.maxVolumeId = state.MaxVolumeId
	m.topologyId = state.TopologyId
	m.mu.Unlock()

	// Publish a single EventRestored with all fields so subscribers can sync in one shot.
	m.publish(StateEvent{
		Kind:        EventRestored,
		MaxFileId:   state.MaxFileId,
		MaxVolumeId: state.MaxVolumeId,
		TopologyId:  state.TopologyId,
	})
	return nil
}

// CommittedState returns a consistent snapshot of all committed FSM state
// under a single lock acquisition.
func (m *MasterFSM) CommittedState() ClusterState {
	m.mu.Lock()
	defer m.mu.Unlock()
	return ClusterState{
		MaxFileId:   m.maxFileId,
		MaxVolumeId: m.maxVolumeId,
		TopologyId:  m.topologyId,
	}
}

// GetMaxFileId returns the current maximum file ID.
func (m *MasterFSM) GetMaxFileId() uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.maxFileId
}

// GetMaxVolumeId returns the current maximum volume ID.
func (m *MasterFSM) GetMaxVolumeId() uint32 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.maxVolumeId
}

// GetTopologyId returns the cluster topology ID.
func (m *MasterFSM) GetTopologyId() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.topologyId
}

// fsmState is the serialized form of MasterFSM for snapshots.
type fsmState struct {
	MaxFileId   uint64 `json:"max_file_id"`
	MaxVolumeId uint32 `json:"max_volume_id"`
	TopologyId  string `json:"topology_id"`
}

// masterFSMSnapshot holds a serialized FSM snapshot.
type masterFSMSnapshot struct {
	data []byte
}

// Persist writes the snapshot to the sink.
func (s *masterFSMSnapshot) Persist(sink raft.SnapshotSink) error {
	if _, err := sink.Write(s.data); err != nil {
		_ = sink.Cancel()
		return fmt.Errorf("failed to write snapshot: %w", err)
	}
	return sink.Close()
}

// Release is called when we are finished with the snapshot.
func (s *masterFSMSnapshot) Release() {}

// encodeLogEntry serializes a RaftCommand into a LogEntry JSON byte slice
// suitable for passing to raft.Raft.Apply.
func encodeLogEntry(cmd consensus.Command) ([]byte, error) {
	payload, err := cmd.Encode()
	if err != nil {
		return nil, fmt.Errorf("failed to encode command payload: %w", err)
	}
	entry := LogEntry{
		Type:    cmd.Type(),
		Payload: json.RawMessage(payload),
	}
	return json.Marshal(entry)
}
