package sequence

import (
	"fmt"
	"log/slog"
	"sync"
	"time"

	"GoBlob/goblob/consensus"
	"GoBlob/goblob/obs"
	"GoBlob/goblob/raft"
)

// RaftApplier is the subset of raft.RaftServer used by the sequencer.
type RaftApplier interface {
	Apply(cmd consensus.Command, timeout time.Duration) error
}

// RaftSequencer wraps FileSequencer and replicates the max file ID via Raft.
// This ensures all master replicas agree on the watermark, preventing ID reuse
// after a leader failover.
type RaftSequencer struct {
	mu           sync.Mutex
	wrapped      *FileSequencer
	raftServer   RaftApplier
	lastSyncedId uint64
	logger       *slog.Logger
}

// NewRaftSequencer creates a new RaftSequencer.
func NewRaftSequencer(cfg *Config, raftServer RaftApplier, logger *slog.Logger) (*RaftSequencer, error) {
	fileSeq, err := NewFileSequencer(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create file sequencer: %w", err)
	}

	return &RaftSequencer{
		wrapped:      fileSeq,
		raftServer:   raftServer,
		lastSyncedId: fileSeq.GetMax(),
		logger:       logger,
	}, nil
}

// NextFileId returns the start of a batch of count unique IDs.
// Before allocating a new batch it submits a MaxFileIdCommand to the Raft log,
// so all replicas know the high-water mark.
//
// The mutex is released before calling Raft.Apply to prevent a deadlock:
// the Raft FSM calls SetMax which tries to acquire the same mutex.
func (rs *RaftSequencer) NextFileId(count uint64) uint64 {
	if count == 0 {
		count = 1
	}

	rs.mu.Lock()
	// The new high-water mark that will result from this allocation.
	newMax := rs.wrapped.current + count
	needSync := newMax > rs.lastSyncedId
	rs.mu.Unlock() // release before Raft.Apply to avoid FSM deadlock

	// Submit to Raft before allocating so replicas stay informed.
	if needSync {
		cmd := raft.MaxFileIdCommand{MaxFileId: newMax}
		if err := rs.raftServer.Apply(cmd, 5*time.Second); err != nil {
			// The file sequencer still prevents local ID reuse; on failover the new
			// leader calls Barrier + SyncToRaft to recover the high-water mark.
			rs.logger.Warn("raft apply failed; sequencer continues locally", "err", err)
			obs.RaftApplyErrors.Inc()
		} else {
			rs.mu.Lock()
			if newMax > rs.lastSyncedId {
				rs.lastSyncedId = newMax
			}
			rs.mu.Unlock()
		}
	}

	rs.mu.Lock()
	defer rs.mu.Unlock()
	return rs.wrapped.NextFileId(count)
}

// SetMax advances the sequencer to at least maxId.
func (rs *RaftSequencer) SetMax(maxId uint64) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	rs.wrapped.SetMax(maxId)
	if maxId > rs.lastSyncedId {
		rs.lastSyncedId = maxId
	}
}

// GetMax returns the current maximum issued ID.
func (rs *RaftSequencer) GetMax() uint64 {
	return rs.wrapped.GetMax()
}

// Close gracefully shuts down the sequencer.
func (rs *RaftSequencer) Close() error {
	return rs.wrapped.Close()
}

// SyncToRaft manually syncs the current max file ID to Raft.
// Useful on startup to ensure replicas know about pre-existing IDs.
func (rs *RaftSequencer) SyncToRaft() error {
	rs.mu.Lock()
	defer rs.mu.Unlock()

	maxId := rs.wrapped.GetMax()
	if maxId == 0 {
		return nil
	}
	cmd := raft.MaxFileIdCommand{MaxFileId: maxId}
	if err := rs.raftServer.Apply(cmd, 5*time.Second); err != nil {
		return fmt.Errorf("failed to sync max file id to raft: %w", err)
	}
	rs.lastSyncedId = maxId
	return nil
}
