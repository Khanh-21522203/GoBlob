package sequence

import (
	"context"
	"fmt"
	"sync"

	"GoBlob/goblob/raft"
)

// RaftSequencer wraps a FileSequencer and replicates max file ID changes via Raft.
// This ensures that all masters agree on the max file ID, preventing duplicates
// in case of failover.
type RaftSequencer struct {
	mu           sync.Mutex
	sequencer    *FileSequencer
	raftServer   *raft.RaftServer
	lastSyncedId uint64
}

// NewRaftSequencer creates a new RaftSequencer.
func NewRaftSequencer(cfg *Config, raftServer *raft.RaftServer) (*RaftSequencer, error) {
	fileSeq, err := NewFileSequencer(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create file sequencer: %w", err)
	}

	rs := &RaftSequencer{
		sequencer:    fileSeq,
		raftServer:   raftServer,
		lastSyncedId: fileSeq.GetMaxFileId(),
	}

	return rs, nil
}

// NextFileId returns the next unique file ID.
// Before allocating a new batch, it submits a MaxFileIdCommand to Raft.
func (rs *RaftSequencer) NextFileId(ctx context.Context) (uint64, error) {
	rs.mu.Lock()
	defer rs.mu.Unlock()

	// Check if we need to sync with Raft before allocating new batch
	maxId := rs.sequencer.GetMaxFileId()
	if maxId > rs.lastSyncedId {
		// Submit MaxFileIdCommand to Raft before allocating new batch
		cmd := &raft.LogCommand{
			Type: raft.CommandSetMaxFileId,
			Data: raft.SetMaxFileIdData{MaxFileId: maxId},
		}

		// Use a short timeout for Raft apply
		applyCtx, cancel := context.WithTimeout(ctx, 5*1000_000_000) // 5 seconds
		defer cancel()

		if err := rs.raftServer.ApplyCommand(applyCtx, cmd); err != nil {
			return 0, fmt.Errorf("failed to apply max file id to raft: %w", err)
		}

		rs.lastSyncedId = maxId
	}

	return rs.sequencer.NextFileId(ctx)
}

// GetMaxFileId returns the current maximum file ID that has been allocated.
func (rs *RaftSequencer) GetMaxFileId() uint64 {
	return rs.sequencer.GetMaxFileId()
}

// Close gracefully shuts down the sequencer.
func (rs *RaftSequencer) Close() error {
	return rs.sequencer.Close()
}

// SyncToRaft manually syncs the current max file ID to Raft.
// This is useful on startup to ensure Raft knows about pre-existing IDs.
func (rs *RaftSequencer) SyncToRaft(ctx context.Context) error {
	rs.mu.Lock()
	defer rs.mu.Unlock()

	maxId := rs.sequencer.GetMaxFileId()
	if maxId == 0 {
		return nil // Nothing to sync
	}

	cmd := &raft.LogCommand{
		Type: raft.CommandSetMaxFileId,
		Data: raft.SetMaxFileIdData{MaxFileId: maxId},
	}

	if err := rs.raftServer.ApplyCommand(ctx, cmd); err != nil {
		return fmt.Errorf("failed to sync max file id to raft: %w", err)
	}

	rs.lastSyncedId = maxId
	return nil
}
