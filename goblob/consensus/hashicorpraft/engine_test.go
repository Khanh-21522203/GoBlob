package hashicorpraft

import (
	"net"
	"path/filepath"
	"testing"
	"time"

	"GoBlob/goblob/consensus"
	"GoBlob/goblob/raft"
)

func TestNewEngineWrapsExistingRaftImplementation(t *testing.T) {
	bindAddr := freeAddr(t)
	cfg := &Config{
		MetaDir:            filepath.Join(t.TempDir(), "raft"),
		BindAddr:           bindAddr,
		SingleMode:         true,
		SnapshotThreshold:  100,
		SnapshotInterval:   time.Minute,
		HeartbeatTimeout:   200 * time.Millisecond,
		ElectionTimeout:    200 * time.Millisecond,
		LeaderLeaseTimeout: 100 * time.Millisecond,
		CommitTimeout:      50 * time.Millisecond,
	}
	fsm := raft.NewMasterFSM()

	engine, err := NewEngine(cfg, fsm)
	if err != nil {
		t.Fatalf("NewEngine() error = %v", err)
	}
	defer engine.Shutdown()

	leaderCh := engine.Subscribe("test", 1)
	select {
	case evt := <-leaderCh:
		if evt.Kind != consensus.EventLeaderChange || !evt.IsLeader {
			t.Fatalf("unexpected leader event: %+v", evt)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for leader event")
	}

	if !engine.IsLeader() {
		t.Fatal("engine did not become leader")
	}
	if err := engine.Apply(raft.MaxFileIdCommand{MaxFileId: 1234}, 5*time.Second); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if got := fsm.GetMaxFileId(); got != 1234 {
		t.Fatalf("fsm max file id = %d, want 1234", got)
	}
}

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	defer l.Close()
	return l.Addr().String()
}
