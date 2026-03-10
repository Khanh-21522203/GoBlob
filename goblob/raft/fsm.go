package raft

import (
	"encoding/json"
	"fmt"
	"io"
	"sync"

	"github.com/hashicorp/raft"
)

// RaftCommand is a command that can be submitted to the Raft log.
type RaftCommand interface {
	Type() string
	Encode() ([]byte, error)
}

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

// MasterFSM implements the raft.FSM interface for the master server.
// It maintains the cluster's replicated state.
type MasterFSM struct {
	mu          sync.Mutex
	maxFileId   uint64
	maxVolumeId uint32
	topologyId  string

	// Callbacks invoked after each Apply (on leader and followers alike).
	// These must not block — they are called while the FSM lock is held.
	onMaxFileIdUpdate   func(uint64)
	onMaxVolumeIdUpdate func(uint32)
	onTopologyIdSet     func(string)
}

// NewMasterFSM creates a new MasterFSM with the given state-change callbacks.
// Callbacks are invoked after each log entry is applied; pass nil to skip.
func NewMasterFSM(
	onMaxFileIdUpdate func(uint64),
	onMaxVolumeIdUpdate func(uint32),
	onTopologyIdSet func(string),
) *MasterFSM {
	return &MasterFSM{
		onMaxFileIdUpdate:   onMaxFileIdUpdate,
		onMaxVolumeIdUpdate: onMaxVolumeIdUpdate,
		onTopologyIdSet:     onTopologyIdSet,
	}
}

// Apply is called by the Raft library for every committed log entry.
func (m *MasterFSM) Apply(log *raft.Log) interface{} {
	m.mu.Lock()
	defer m.mu.Unlock()

	var entry LogEntry
	if err := json.Unmarshal(log.Data, &entry); err != nil {
		return fmt.Errorf("failed to unmarshal log entry: %w", err)
	}

	switch entry.Type {
	case "max_file_id":
		var cmd MaxFileIdCommand
		if err := json.Unmarshal(entry.Payload, &cmd); err != nil {
			return fmt.Errorf("failed to unmarshal max_file_id: %w", err)
		}
		if cmd.MaxFileId > m.maxFileId {
			m.maxFileId = cmd.MaxFileId
			if m.onMaxFileIdUpdate != nil {
				m.onMaxFileIdUpdate(m.maxFileId)
			}
		}

	case "max_volume_id":
		var cmd MaxVolumeIdCommand
		if err := json.Unmarshal(entry.Payload, &cmd); err != nil {
			return fmt.Errorf("failed to unmarshal max_volume_id: %w", err)
		}
		if cmd.MaxVolumeId > m.maxVolumeId {
			m.maxVolumeId = cmd.MaxVolumeId
			if m.onMaxVolumeIdUpdate != nil {
				m.onMaxVolumeIdUpdate(m.maxVolumeId)
			}
		}

	case "topology_id":
		var cmd TopologyIdCommand
		if err := json.Unmarshal(entry.Payload, &cmd); err != nil {
			return fmt.Errorf("failed to unmarshal topology_id: %w", err)
		}
		m.topologyId = cmd.TopologyId
		if m.onTopologyIdSet != nil {
			m.onTopologyIdSet(m.topologyId)
		}

	default:
		return fmt.Errorf("unknown command type: %s", entry.Type)
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

	if m.onMaxFileIdUpdate != nil {
		m.onMaxFileIdUpdate(state.MaxFileId)
	}
	if m.onMaxVolumeIdUpdate != nil {
		m.onMaxVolumeIdUpdate(state.MaxVolumeId)
	}
	if state.TopologyId != "" && m.onTopologyIdSet != nil {
		m.onTopologyIdSet(state.TopologyId)
	}
	return nil
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
func encodeLogEntry(cmd RaftCommand) ([]byte, error) {
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
