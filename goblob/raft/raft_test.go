package raft

import (
	"io"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/hashicorp/raft"
)

// getFreeAddr finds an available TCP port on localhost.
func getFreeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to find free port: %v", err)
	}
	addr := l.Addr().String()
	l.Close()
	return addr
}

func TestRaftConfigValidation(t *testing.T) {
	t.Run("valid config", func(t *testing.T) {
		cfg := DefaultRaftConfig()
		if err := cfg.Validate(); err != nil {
			t.Errorf("expected valid config, got error: %v", err)
		}
	})

	t.Run("empty meta_dir", func(t *testing.T) {
		cfg := DefaultRaftConfig()
		cfg.MetaDir = ""
		if err := cfg.Validate(); err == nil {
			t.Error("expected error for empty meta_dir")
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
	t.Run("apply max_file_id", func(t *testing.T) {
		fsm := NewMasterFSM()
		ch := fsm.Subscribe("test", 10)

		data, err := encodeLogEntry(MaxFileIdCommand{MaxFileId: 1000})
		if err != nil {
			t.Fatalf("failed to encode: %v", err)
		}
		result := fsm.Apply(&raft.Log{Type: raft.LogCommand, Data: data})
		if result != nil {
			t.Errorf("expected nil result, got: %v", result)
		}
		if fsm.GetMaxFileId() != 1000 {
			t.Errorf("expected max_file_id=1000, got %d", fsm.GetMaxFileId())
		}

		select {
		case evt := <-ch:
			if evt.Kind != EventMaxFileId || evt.MaxFileId != 1000 {
				t.Errorf("unexpected event: %+v", evt)
			}
		case <-time.After(time.Second):
			t.Error("timeout waiting for EventMaxFileId")
		}
	})

	t.Run("max_file_id only increases", func(t *testing.T) {
		fsm := NewMasterFSM()

		data, _ := encodeLogEntry(MaxFileIdCommand{MaxFileId: 1000})
		fsm.Apply(&raft.Log{Type: raft.LogCommand, Data: data})

		data, _ = encodeLogEntry(MaxFileIdCommand{MaxFileId: 500})
		fsm.Apply(&raft.Log{Type: raft.LogCommand, Data: data})

		if got := fsm.GetMaxFileId(); got != 1000 {
			t.Errorf("expected max_file_id to remain 1000, got %d", got)
		}
	})

	t.Run("apply max_volume_id", func(t *testing.T) {
		fsm := NewMasterFSM()
		ch := fsm.Subscribe("test", 10)

		data, _ := encodeLogEntry(MaxVolumeIdCommand{MaxVolumeId: 42})
		fsm.Apply(&raft.Log{Type: raft.LogCommand, Data: data})

		if fsm.GetMaxVolumeId() != 42 {
			t.Errorf("expected max_volume_id=42, got %d", fsm.GetMaxVolumeId())
		}

		select {
		case evt := <-ch:
			if evt.Kind != EventMaxVolumeId || evt.MaxVolumeId != 42 {
				t.Errorf("unexpected event: %+v", evt)
			}
		case <-time.After(time.Second):
			t.Error("timeout waiting for EventMaxVolumeId")
		}
	})

	t.Run("apply topology_id", func(t *testing.T) {
		fsm := NewMasterFSM()
		ch := fsm.Subscribe("test", 10)

		data, _ := encodeLogEntry(TopologyIdCommand{TopologyId: "cluster-uuid-123"})
		fsm.Apply(&raft.Log{Type: raft.LogCommand, Data: data})

		if fsm.GetTopologyId() != "cluster-uuid-123" {
			t.Errorf("expected topology_id=cluster-uuid-123, got %s", fsm.GetTopologyId())
		}

		select {
		case evt := <-ch:
			if evt.Kind != EventTopologyId || evt.TopologyId != "cluster-uuid-123" {
				t.Errorf("unexpected event: %+v", evt)
			}
		case <-time.After(time.Second):
			t.Error("timeout waiting for EventTopologyId")
		}
	})

	t.Run("unknown command returns error", func(t *testing.T) {
		fsm := NewMasterFSM()
		data, _ := encodeLogEntry(unknownCmd{})
		result := fsm.Apply(&raft.Log{Type: raft.LogCommand, Data: data})
		if result == nil {
			t.Error("expected error for unknown command type")
		}
	})
}

func TestMasterFSMSnapshot(t *testing.T) {
	t.Run("snapshot and restore", func(t *testing.T) {
		fsm := NewMasterFSM()

		data, _ := encodeLogEntry(MaxFileIdCommand{MaxFileId: 5000})
		fsm.Apply(&raft.Log{Type: raft.LogCommand, Data: data})
		data, _ = encodeLogEntry(MaxVolumeIdCommand{MaxVolumeId: 7})
		fsm.Apply(&raft.Log{Type: raft.LogCommand, Data: data})
		data, _ = encodeLogEntry(TopologyIdCommand{TopologyId: "topo-abc"})
		fsm.Apply(&raft.Log{Type: raft.LogCommand, Data: data})

		snap, err := fsm.Snapshot()
		if err != nil {
			t.Fatalf("failed to create snapshot: %v", err)
		}

		// Restore to a new FSM and observe via EventRestored.
		newFSM := NewMasterFSM()
		ch := newFSM.Subscribe("test", 10)
		rc := newInMemReadCloser(snap.(*masterFSMSnapshot))
		if err := newFSM.Restore(rc); err != nil {
			t.Fatalf("failed to restore: %v", err)
		}

		if newFSM.GetMaxFileId() != 5000 {
			t.Errorf("expected restored max_file_id=5000, got %d", newFSM.GetMaxFileId())
		}
		if newFSM.GetMaxVolumeId() != 7 {
			t.Errorf("expected restored max_volume_id=7, got %d", newFSM.GetMaxVolumeId())
		}
		if newFSM.GetTopologyId() != "topo-abc" {
			t.Errorf("expected restored topology_id=topo-abc, got %s", newFSM.GetTopologyId())
		}

		select {
		case evt := <-ch:
			if evt.Kind != EventRestored {
				t.Errorf("expected EventRestored, got %v", evt.Kind)
			}
			if evt.MaxFileId != 5000 {
				t.Errorf("EventRestored: expected MaxFileId=5000, got %d", evt.MaxFileId)
			}
			if evt.MaxVolumeId != 7 {
				t.Errorf("EventRestored: expected MaxVolumeId=7, got %d", evt.MaxVolumeId)
			}
			if evt.TopologyId != "topo-abc" {
				t.Errorf("EventRestored: expected TopologyId=topo-abc, got %s", evt.TopologyId)
			}
		case <-time.After(time.Second):
			t.Error("timeout waiting for EventRestored")
		}
	})
}

func TestSingleModeRaftServer(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping raft test in short mode")
	}

	t.Run("single node becomes leader", func(t *testing.T) {
		tmpDir := t.TempDir()
		bindAddr := getFreeAddr(t)

		cfg := &RaftConfig{
			MetaDir:            filepath.Join(tmpDir, "raft"),
			BindAddr:           bindAddr,
			SingleMode:         true,
			SnapshotThreshold:  100,
			SnapshotInterval:   1 * time.Minute,
			HeartbeatTimeout:   200 * time.Millisecond,
			ElectionTimeout:    200 * time.Millisecond,
			LeaderLeaseTimeout: 100 * time.Millisecond,
			CommitTimeout:      50 * time.Millisecond,
		}

		fsm := NewMasterFSM()
		rs, err := NewRaftServer(cfg, fsm)
		if err != nil {
			t.Fatalf("failed to create raft server: %v", err)
		}
		defer rs.Shutdown()

		leaderCh := rs.Subscribe("test", 10)
		select {
		case evt := <-leaderCh:
			if !evt.IsLeader {
				t.Error("expected to become leader")
			}
		case <-time.After(3 * time.Second):
			t.Error("timeout waiting for leadership")
		}

		if !rs.IsLeader() {
			t.Error("IsLeader() returned false")
		}
	})

	t.Run("apply all three command types", func(t *testing.T) {
		tmpDir := t.TempDir()
		bindAddr := getFreeAddr(t)

		cfg := &RaftConfig{
			MetaDir:            filepath.Join(tmpDir, "raft"),
			BindAddr:           bindAddr,
			SingleMode:         true,
			SnapshotThreshold:  100,
			SnapshotInterval:   1 * time.Minute,
			HeartbeatTimeout:   200 * time.Millisecond,
			ElectionTimeout:    200 * time.Millisecond,
			LeaderLeaseTimeout: 100 * time.Millisecond,
			CommitTimeout:      50 * time.Millisecond,
		}

		fsm := NewMasterFSM()
		rs, err := NewRaftServer(cfg, fsm)
		if err != nil {
			t.Fatalf("failed to create raft server: %v", err)
		}
		defer rs.Shutdown()

		leaderCh := rs.Subscribe("leader", 10)
		fsmCh := fsm.Subscribe("test", 32)

		select {
		case <-leaderCh:
		case <-time.After(3 * time.Second):
			t.Fatal("timeout waiting for leadership")
		}

		if err := rs.Apply(MaxFileIdCommand{MaxFileId: 9999}, 5*time.Second); err != nil {
			t.Errorf("failed to apply MaxFileIdCommand: %v", err)
		}
		if err := rs.Apply(MaxVolumeIdCommand{MaxVolumeId: 77}, 5*time.Second); err != nil {
			t.Errorf("failed to apply MaxVolumeIdCommand: %v", err)
		}
		if err := rs.Apply(TopologyIdCommand{TopologyId: "cluster-xyz"}, 5*time.Second); err != nil {
			t.Errorf("failed to apply TopologyIdCommand: %v", err)
		}

		// Drain events and collect into a map by kind.
		received := make(map[EventKind]StateEvent)
		deadline := time.After(2 * time.Second)
		for len(received) < 3 {
			select {
			case evt := <-fsmCh:
				received[evt.Kind] = evt
			case <-deadline:
				t.Fatalf("timeout: only got %d/3 events", len(received))
			}
		}

		if got := received[EventMaxFileId].MaxFileId; got != 9999 {
			t.Errorf("expected MaxFileId=9999, got %d", got)
		}
		if got := received[EventMaxVolumeId].MaxVolumeId; got != 77 {
			t.Errorf("expected MaxVolumeId=77, got %d", got)
		}
		if got := received[EventTopologyId].TopologyId; got != "cluster-xyz" {
			t.Errorf("expected TopologyId=cluster-xyz, got %s", got)
		}

		if fsm.GetMaxFileId() != 9999 {
			t.Errorf("FSM: expected max_file_id=9999, got %d", fsm.GetMaxFileId())
		}
		if fsm.GetMaxVolumeId() != 77 {
			t.Errorf("FSM: expected max_volume_id=77, got %d", fsm.GetMaxVolumeId())
		}
		if fsm.GetTopologyId() != "cluster-xyz" {
			t.Errorf("FSM: expected topology_id=cluster-xyz, got %s", fsm.GetTopologyId())
		}
	})

	t.Run("barrier waits for log application", func(t *testing.T) {
		tmpDir := t.TempDir()
		bindAddr := getFreeAddr(t)

		cfg := &RaftConfig{
			MetaDir:            filepath.Join(tmpDir, "raft"),
			BindAddr:           bindAddr,
			SingleMode:         true,
			SnapshotThreshold:  100,
			SnapshotInterval:   1 * time.Minute,
			HeartbeatTimeout:   200 * time.Millisecond,
			ElectionTimeout:    200 * time.Millisecond,
			LeaderLeaseTimeout: 100 * time.Millisecond,
			CommitTimeout:      50 * time.Millisecond,
		}

		fsm := NewMasterFSM()
		rs, err := NewRaftServer(cfg, fsm)
		if err != nil {
			t.Fatalf("failed to create raft server: %v", err)
		}
		defer rs.Shutdown()

		leaderCh := rs.Subscribe("test", 10)
		select {
		case <-leaderCh:
		case <-time.After(3 * time.Second):
			t.Fatal("timeout waiting for leadership")
		}

		if err := rs.Barrier(5 * time.Second); err != nil {
			t.Errorf("Barrier failed: %v", err)
		}
	})
}

func TestFollowerRedirect(t *testing.T) {
	// RedirectToLeader should return false when we ARE the leader.
	// We test with a mock that always reports not-leader.
	mock := &mockRaftServer{isLeader: false, leaderAddr: "10.0.0.2:9333"}

	w := &captureResponseWriter{}
	req, _ := newGetRequest("/test")

	redirected := RedirectToLeader(mock, w, req)
	if !redirected {
		t.Error("expected redirect when not leader")
	}
	if w.code != http.StatusTemporaryRedirect {
		t.Errorf("expected 307, got %d", w.code)
	}

	// When leader address is unknown, return 503.
	mock.leaderAddr = ""
	w2 := &captureResponseWriter{}
	req2, _ := newGetRequest("/test")
	RedirectToLeader(mock, w2, req2)
	if w2.code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", w2.code)
	}
}

// --- test helpers ---

// unknownCmd is a test RaftCommand with an unrecognized type.
type unknownCmd struct{}

func (unknownCmd) Type() string            { return "unknown_command_type" }
func (unknownCmd) Encode() ([]byte, error) { return []byte("{}"), nil }

// inMemReadCloser wraps a masterFSMSnapshot as an io.ReadCloser for tests.
type inMemReadCloser struct {
	data    []byte
	readIdx int
}

func newInMemReadCloser(snap *masterFSMSnapshot) *inMemReadCloser {
	return &inMemReadCloser{data: snap.data}
}

func (r *inMemReadCloser) Read(p []byte) (int, error) {
	if r.readIdx >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.readIdx:])
	r.readIdx += n
	return n, nil
}

func (r *inMemReadCloser) Close() error { return nil }

// mockRaftServer satisfies the RaftServer interface for redirect tests.
type mockRaftServer struct {
	isLeader   bool
	leaderAddr string
}

func (m *mockRaftServer) IsLeader() bool                             { return m.isLeader }
func (m *mockRaftServer) LeaderAddress() string                      { return m.leaderAddr }
func (m *mockRaftServer) Apply(_ RaftCommand, _ time.Duration) error { return nil }
func (m *mockRaftServer) Barrier(_ time.Duration) error              { return nil }
func (m *mockRaftServer) AddPeer(_ string) error                     { return nil }
func (m *mockRaftServer) RemovePeer(_ string) error                  { return nil }
func (m *mockRaftServer) Stats() map[string]string                   { return nil }
func (m *mockRaftServer) Shutdown() error                            { return nil }
func (m *mockRaftServer) Subscribe(_ string, bufSize int) <-chan StateEvent {
	return make(chan StateEvent, bufSize)
}
func (m *mockRaftServer) Unsubscribe(_ string) {}

// captureResponseWriter records the HTTP status code written.
type captureResponseWriter struct {
	code    int
	headers http.Header
	body    []byte
}

func (w *captureResponseWriter) Header() http.Header {
	if w.headers == nil {
		w.headers = make(http.Header)
	}
	return w.headers
}
func (w *captureResponseWriter) Write(b []byte) (int, error) {
	w.body = append(w.body, b...)
	return len(b), nil
}
func (w *captureResponseWriter) WriteHeader(code int) { w.code = code }

func newGetRequest(path string) (*http.Request, error) {
	return http.NewRequest(http.MethodGet, "http://localhost"+path, nil)
}
