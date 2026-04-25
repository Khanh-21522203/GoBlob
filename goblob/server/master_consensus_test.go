package server

import (
	"strings"
	"testing"
	"time"

	"GoBlob/goblob/raft"
)

func TestNewMasterConsensusEngineNativeSingleNode(t *testing.T) {
	opt := DefaultMasterOption()
	opt.Host = "127.0.0.1"
	opt.Port = 9333
	opt.MetaDir = t.TempDir()
	opt.RaftEngine = RaftEngineNative
	fsm := raft.NewMasterFSM()

	engine, err := newMasterConsensusEngine(opt, fsm)
	if err != nil {
		t.Fatalf("newMasterConsensusEngine() error = %v", err)
	}
	defer engine.Shutdown()

	waitForConsensusLeader(t, engine)
	if err := engine.Apply(raft.MaxFileIdCommand{MaxFileId: 12345}, 5*time.Second); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if err := engine.Barrier(5 * time.Second); err != nil {
		t.Fatalf("Barrier() error = %v", err)
	}
	if got := fsm.GetMaxFileId(); got != 12345 {
		t.Fatalf("fsm max_file_id=%d, want 12345", got)
	}
}

func TestNewMasterConsensusEngineNativeRejectsPeers(t *testing.T) {
	opt := DefaultMasterOption()
	opt.MetaDir = t.TempDir()
	opt.RaftEngine = RaftEngineNative
	opt.Peers = []string{"127.0.0.1:9333", "127.0.0.1:9335"}

	_, err := newMasterConsensusEngine(opt, raft.NewMasterFSM())
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "single-node") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNewMasterConsensusEngineRejectsUnknownEngine(t *testing.T) {
	opt := DefaultMasterOption()
	opt.MetaDir = t.TempDir()
	opt.RaftEngine = "bogus"

	_, err := newMasterConsensusEngine(opt, raft.NewMasterFSM())
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "unsupported raft engine") {
		t.Fatalf("unexpected error: %v", err)
	}
}

type leaderEngine interface {
	IsLeader() bool
}

func waitForConsensusLeader(t *testing.T, engine leaderEngine) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if engine.IsLeader() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timeout waiting for consensus leader")
}
