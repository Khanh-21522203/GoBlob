package raft

import (
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/hashicorp/raft"
)

func TestRaftConfigValidation(t *testing.T) {
	t.Run("valid config", func(t *testing.T) {
		cfg := DefaultRaftConfig()
		if err := cfg.Validate(); err != nil {
			t.Errorf("expected valid config, got error: %v", err)
		}
	})

	t.Run("empty data_dir", func(t *testing.T) {
		cfg := DefaultRaftConfig()
		cfg.DataDir = ""
		if err := cfg.Validate(); err == nil {
			t.Error("expected error for empty data_dir")
		}
	})

	t.Run("empty bind_addr", func(t *testing.T) {
		cfg := DefaultRaftConfig()
		cfg.BindAddr = ""
		if err := cfg.Validate(); err == nil {
			t.Error("expected error for empty bind_addr")
		}
	})

	t.Run("zero snapshot_threshold", func(t *testing.T) {
		cfg := DefaultRaftConfig()
		cfg.SnapshotThreshold = 0
		if err := cfg.Validate(); err == nil {
			t.Error("expected error for zero snapshot_threshold")
		}
	})

	t.Run("negative heartbeat_timeout", func(t *testing.T) {
		cfg := DefaultRaftConfig()
		cfg.HeartbeatTimeout = -1 * time.Second
		if err := cfg.Validate(); err == nil {
			t.Error("expected error for negative heartbeat_timeout")
		}
	})
}

func TestMasterFSM(t *testing.T) {
	t.Run("apply set_max_file_id", func(t *testing.T) {
		fsm := NewMasterFSM()

		cmd := &LogCommand{
			Type: CommandSetMaxFileId,
			Data: SetMaxFileIdData{MaxFileId: 1000},
		}

		data, err := cmd.Marshal()
		if err != nil {
			t.Fatalf("failed to marshal command: %v", err)
		}

		log := &raft.Log{
			Type: raft.LogCommand,
			Data: data,
		}
		result := fsm.Apply(log)
		if result != nil {
			t.Errorf("expected nil result, got: %v", result)
		}

		if got := fsm.GetMaxFileId(); got != 1000 {
			t.Errorf("expected max_file_id=1000, got %d", got)
		}
	})

	t.Run("apply set_max_file_id increases only", func(t *testing.T) {
		fsm := NewMasterFSM()

		// Set to 1000
		cmd := &LogCommand{
			Type: CommandSetMaxFileId,
			Data: SetMaxFileIdData{MaxFileId: 1000},
		}
		data, _ := cmd.Marshal()
		fsm.Apply(&raft.Log{Type: raft.LogCommand, Data: data})

		// Try to set to 500 (should not decrease)
		cmd = &LogCommand{
			Type: CommandSetMaxFileId,
			Data: SetMaxFileIdData{MaxFileId: 500},
		}
		data, _ = cmd.Marshal()
		fsm.Apply(&raft.Log{Type: raft.LogCommand, Data: data})

		if got := fsm.GetMaxFileId(); got != 1000 {
			t.Errorf("expected max_file_id to remain 1000, got %d", got)
		}
	})

	t.Run("apply unknown command", func(t *testing.T) {
		fsm := NewMasterFSM()

		cmd := &LogCommand{
			Type: LogCommandType("unknown"),
		}
		data, _ := cmd.Marshal()
		result := fsm.Apply(&raft.Log{Type: raft.LogCommand, Data: data})
		if result == nil {
			t.Error("expected error for unknown command type")
		}
	})
}

func TestMasterFSMSnapshot(t *testing.T) {
	t.Run("snapshot and restore", func(t *testing.T) {
		fsm := NewMasterFSM()

		// Set some state
		cmd := &LogCommand{
			Type: CommandSetMaxFileId,
			Data: SetMaxFileIdData{MaxFileId: 5000},
		}
		data, _ := cmd.Marshal()
		fsm.Apply(&raft.Log{Type: raft.LogCommand, Data: data})

		// Create snapshot
		snap, err := fsm.Snapshot()
		if err != nil {
			t.Fatalf("failed to create snapshot: %v", err)
		}

		// Create new FSM and restore
		newFSM := NewMasterFSM()
		rc := newTestReadCloser(snap.(*masterFSMSnapshot))
		if err := newFSM.Restore(rc); err != nil {
			t.Fatalf("failed to restore: %v", err)
		}

		if got := newFSM.GetMaxFileId(); got != 5000 {
			t.Errorf("expected restored max_file_id=5000, got %d", got)
		}
	})
}

func TestSingleModeRaftServer(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping raft test in short mode")
	}

	t.Run("single node becomes leader", func(t *testing.T) {
		tmpDir := t.TempDir()

		cfg := &RaftConfig{
			DataDir:           filepath.Join(tmpDir, "raft"),
			BindAddr:          "127.0.0.1:0", // Use random port
			Bootstrap:         true,
			SnapshotThreshold: 100,
			SnapshotInterval:  1 * time.Minute,
			HeartbeatTimeout:  500 * time.Millisecond,
			ElectionTimeout:   500 * time.Millisecond,
			LeaderLeaseTimeout: 250 * time.Millisecond,
			CommitTimeout:     50 * time.Millisecond,
		}

		fsm := NewMasterFSM()
		leaderCh := make(chan bool, 10)

		rs, err := NewRaftServer(cfg, fsm, func(isLeader bool) {
			leaderCh <- isLeader
		})
		if err != nil {
			t.Fatalf("failed to create raft server: %v", err)
		}
		defer rs.Shutdown()

		// Wait for leadership (should happen within election timeout)
		select {
		case isLeader := <-leaderCh:
			if !isLeader {
				t.Error("expected to become leader")
			}
		case <-time.After(3 * time.Second):
			t.Error("timeout waiting for leadership")
		}

		// Verify we are leader
		if !rs.IsLeader() {
			t.Error("IsLeader() returned false")
		}
	})

	t.Run("apply command as leader", func(t *testing.T) {
		tmpDir := t.TempDir()

		cfg := &RaftConfig{
			DataDir:           filepath.Join(tmpDir, "raft"),
			BindAddr:          "127.0.0.1:0",
			Bootstrap:         true,
			SnapshotThreshold: 100,
			SnapshotInterval:  1 * time.Minute,
			HeartbeatTimeout:  500 * time.Millisecond,
			ElectionTimeout:   500 * time.Millisecond,
			LeaderLeaseTimeout: 250 * time.Millisecond,
			CommitTimeout:     50 * time.Millisecond,
		}

		fsm := NewMasterFSM()
		leaderCh := make(chan bool, 10)

		rs, err := NewRaftServer(cfg, fsm, func(isLeader bool) {
			leaderCh <- isLeader
		})
		if err != nil {
			t.Fatalf("failed to create raft server: %v", err)
		}
		defer rs.Shutdown()

		// Wait for leadership
		select {
		case <-leaderCh:
		case <-time.After(3 * time.Second):
			t.Fatal("timeout waiting for leadership")
		}

		// Apply a command
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		cmd := &LogCommand{
			Type: CommandSetMaxFileId,
			Data: SetMaxFileIdData{MaxFileId: 9999},
		}

		if err := rs.ApplyCommand(ctx, cmd); err != nil {
			t.Errorf("failed to apply command: %v", err)
		}

		// Wait for command to be applied
		time.Sleep(100 * time.Millisecond)

		if got := rs.GetMaxFileId(); got != 9999 {
			t.Errorf("expected max_file_id=9999, got %d", got)
		}
	})
}

func TestLogCommandMarshal(t *testing.T) {
	t.Run("marshal and unmarshal", func(t *testing.T) {
		cmd := &LogCommand{
			Type: CommandSetMaxFileId,
			Data: SetMaxFileIdData{MaxFileId: 12345},
		}

		data, err := cmd.Marshal()
		if err != nil {
			t.Fatalf("failed to marshal: %v", err)
		}

		decoded, err := UnmarshalLogCommand(data)
		if err != nil {
			t.Fatalf("failed to unmarshal: %v", err)
		}

		if decoded.Type != cmd.Type {
			t.Errorf("expected type %s, got %s", cmd.Type, decoded.Type)
		}
	})
}

// test helpers

type testReadCloser struct {
	snap    *masterFSMSnapshot
	data    []byte
	closed  bool
	readIdx int
}

func newTestReadCloser(snap *masterFSMSnapshot) *testReadCloser {
	// Encode the snapshot
	data, err := json.Marshal(struct {
		MaxFileId uint64 `json:"max_file_id"`
	}{
		MaxFileId: snap.maxFileId,
	})
	if err != nil {
		panic(err)
	}
	return &testReadCloser{
		snap: snap,
		data: data,
	}
}

func (rc *testReadCloser) Read(p []byte) (n int, err error) {
	if rc.closed {
		return 0, io.EOF
	}
	if rc.readIdx >= len(rc.data) {
		return 0, io.EOF
	}
	n = copy(p, rc.data[rc.readIdx:])
	rc.readIdx += n
	return n, nil
}

func (rc *testReadCloser) Close() error {
	rc.closed = true
	return nil
}
