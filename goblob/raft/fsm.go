package raft

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/hashicorp/raft"
)

// LogCommandType represents different types of Raft log commands.
type LogCommandType string

const (
	// CommandSetMaxFileId sets the maximum file ID (needle ID).
	CommandSetMaxFileId LogCommandType = "set_max_file_id"
	// CommandRegisterVolume registers a volume in the cluster.
	CommandRegisterVolume LogCommandType = "register_volume"
	// CommandUnregisterVolume unregisters a volume from the cluster.
	CommandUnregisterVolume LogCommandType = "unregister_volume"
)

// LogCommand is a command that gets replicated via Raft.
type LogCommand struct {
	Type LogCommandType `json:"type"`
	Data any            `json:"data"`
}

// SetMaxFileIdData is the data for CommandSetMaxFileId.
type SetMaxFileIdData struct {
	MaxFileId uint64 `json:"max_file_id"`
}

// RegisterVolumeData is the data for CommandRegisterVolume.
type RegisterVolumeData struct {
	VolumeId uint32 `json:"volume_id"`
	DataNode string `json:"data_node"`
}

// UnregisterVolumeData is the data for CommandUnregisterVolume.
type UnregisterVolumeData struct {
	VolumeId uint32 `json:"volume_id"`
}

// MasterFSM implements the raft.FSM interface for the master server.
// It maintains the cluster's replicated state.
type MasterFSM struct {
	mu        sync.RWMutex
	maxFileId uint64
	// TODO: Add volume registry, topology state, etc.
}

// NewMasterFSM creates a new MasterFSM.
func NewMasterFSM() *MasterFSM {
	return &MasterFSM{}
}

// Apply applies a Raft log entry to the finite state machine.
// This is called when a log entry is committed.
func (m *MasterFSM) Apply(log *raft.Log) interface{} {
	m.mu.Lock()
	defer m.mu.Unlock()

	var cmd LogCommand
	if err := json.Unmarshal(log.Data, &cmd); err != nil {
		return fmt.Errorf("failed to unmarshal command: %w", err)
	}

	switch cmd.Type {
	case CommandSetMaxFileId:
		data, ok := cmd.Data.(map[string]any)
		if !ok {
			return fmt.Errorf("invalid data type for set_max_file_id")
		}
		maxFileIdFloat, ok := data["max_file_id"].(float64)
		if !ok {
			return fmt.Errorf("invalid max_file_id type")
		}
		maxFileId := uint64(maxFileIdFloat)
		if maxFileId > m.maxFileId {
			m.maxFileId = maxFileId
		}
		return nil

	case CommandRegisterVolume:
		// TODO: Implement volume registration
		return nil

	case CommandUnregisterVolume:
		// TODO: Implement volume unregistration
		return nil

	default:
		return fmt.Errorf("unknown command type: %s", cmd.Type)
	}
}

// Snapshot is used to support log compaction. This method should
// return an FSMSnapshot that can be used to save a point-in-time
// snapshot of the FSM.
func (m *MasterFSM) Snapshot() (raft.FSMSnapshot, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return &masterFSMSnapshot{
		maxFileId: m.maxFileId,
	}, nil
}

// Restore is used to restore an FSM from a snapshot.
func (m *MasterFSM) Restore(rc io.ReadCloser) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	defer rc.Close()

	var snapshot struct {
		MaxFileId uint64 `json:"max_file_id"`
	}

	if err := json.NewDecoder(rc).Decode(&snapshot); err != nil {
		return fmt.Errorf("failed to decode snapshot: %w", err)
	}

	m.maxFileId = snapshot.MaxFileId
	return nil
}

// GetMaxFileId returns the current maximum file ID.
func (m *MasterFSM) GetMaxFileId() uint64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.maxFileId
}

// masterFSMSnapshot is a snapshot of the MasterFSM state.
type masterFSMSnapshot struct {
	maxFileId uint64
}

// Persist persists the snapshot to the given sink.
func (s *masterFSMSnapshot) Persist(sink raft.SnapshotSink) error {
	err := json.NewEncoder(sink).Encode(struct {
		MaxFileId uint64 `json:"max_file_id"`
	}{
		MaxFileId: s.maxFileId,
	})
	if err != nil {
		sink.Cancel()
		return fmt.Errorf("failed to encode snapshot: %w", err)
	}
	return sink.Close()
}

// Release is called when we are finished with the snapshot.
func (s *masterFSMSnapshot) Release() {}

// ensureFileExists creates a file if it doesn't exist.
func ensureFileExists(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer file.Close()
	return nil
}
