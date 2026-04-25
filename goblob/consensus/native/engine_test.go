package native

import (
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"GoBlob/goblob/consensus"
	"GoBlob/goblob/consensus/conformance"
)

func TestConformance(t *testing.T) {
	conformance.Run(t, nativeFactory{})
}

type nativeFactory struct{}

func (nativeFactory) NewCluster(t *testing.T, size int) conformance.Cluster {
	t.Helper()
	cluster := &nativeCluster{
		network: NewMemoryNetwork(),
		nodes:   make([]*nativeNode, size),
		peers:   make([]string, size),
	}
	for i := range cluster.nodes {
		id := string(rune('a' + i))
		cluster.peers[i] = id
		cluster.nodes[i] = &nativeNode{
			id:    id,
			store: NewMemoryStore(),
		}
	}
	for i := range cluster.nodes {
		cluster.startNode(t, i)
	}
	return cluster
}

type nativeCluster struct {
	network *MemoryNetwork
	nodes   []*nativeNode
	peers   []string
}

func (c *nativeCluster) Nodes() []*conformance.Node {
	nodes := make([]*conformance.Node, 0, len(c.nodes))
	for _, node := range c.nodes {
		nodes = append(nodes, node.view)
	}
	return nodes
}

func (c *nativeCluster) Command(maxFileID uint64) consensus.Command {
	return testCommand{maxFileID: maxFileID}
}

func (c *nativeCluster) ShutdownNode(t *testing.T, index int) {
	t.Helper()
	node := c.nodes[index]
	if node.view.Engine == nil {
		return
	}
	_ = node.view.Engine.Shutdown()
	node.view.Engine = nil
}

func (c *nativeCluster) RestartNode(t *testing.T, index int) {
	t.Helper()
	if c.nodes[index].view.Engine != nil {
		c.ShutdownNode(t, index)
	}
	c.startNode(t, index)
}

func (c *nativeCluster) Shutdown() {
	for i := range c.nodes {
		if c.nodes[i].view != nil && c.nodes[i].view.Engine != nil {
			_ = c.nodes[i].view.Engine.Shutdown()
			c.nodes[i].view.Engine = nil
		}
	}
}

func (c *nativeCluster) startNode(t *testing.T, index int) {
	t.Helper()
	node := c.nodes[index]
	fsm := &testFSM{}
	engine, err := NewEngine(Config{
		ID:                node.id,
		Peers:             c.peers,
		Network:           c.network,
		Store:             node.store,
		FSM:               fsm,
		ElectionTimeout:   120 * time.Millisecond,
		HeartbeatInterval: 25 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("start node %s: %v", node.id, err)
	}
	node.fsm = fsm
	if node.view == nil {
		node.view = &conformance.Node{ID: node.id}
	}
	node.view.Engine = engine
	node.view.Probe = fsm
}

type nativeNode struct {
	id    string
	store *MemoryStore
	fsm   *testFSM
	view  *conformance.Node
}

type testCommand struct {
	maxFileID uint64
}

func (c testCommand) Type() string {
	return "max_file_id"
}

func (c testCommand) Encode() ([]byte, error) {
	return json.Marshal(struct {
		MaxFileID uint64 `json:"max_file_id"`
	}{MaxFileID: c.maxFileID})
}

type testFSM struct {
	mu        sync.Mutex
	maxFileID uint64
}

func (f *testFSM) Apply(cmd consensus.Command) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if c, ok := cmd.(testCommand); ok && c.maxFileID > f.maxFileID {
		f.maxFileID = c.maxFileID
	}
}

func (f *testFSM) MaxFileID() uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.maxFileID
}

func (f *testFSM) Snapshot() ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return json.Marshal(struct {
		MaxFileID uint64 `json:"max_file_id"`
	}{MaxFileID: f.maxFileID})
}

func (f *testFSM) Restore(data []byte) error {
	var state struct {
		MaxFileID uint64 `json:"max_file_id"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.maxFileID = state.MaxFileID
	return nil
}

func TestFileStoreRestartRecovery(t *testing.T) {
	network := NewMemoryNetwork()
	peers := []string{"a"}
	store, err := NewFileStore(filepath.Join(t.TempDir(), "raft.json"), decodeTestCommand)
	if err != nil {
		t.Fatalf("NewFileStore() error = %v", err)
	}
	fsm := &testFSM{}
	engine, err := NewEngine(Config{
		ID:                "a",
		Peers:             peers,
		Network:           network,
		Store:             store,
		FSM:               fsm,
		ElectionTimeout:   120 * time.Millisecond,
		HeartbeatInterval: 25 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewEngine() error = %v", err)
	}
	leader := waitNativeLeader(t, engine)
	if err := leader.Apply(testCommand{maxFileID: 42}, 5*time.Second); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if err := engine.Shutdown(); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}

	restartedFSM := &testFSM{}
	restarted, err := NewEngine(Config{
		ID:                "a",
		Peers:             peers,
		Network:           network,
		Store:             store,
		FSM:               restartedFSM,
		ElectionTimeout:   120 * time.Millisecond,
		HeartbeatInterval: 25 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("restart NewEngine() error = %v", err)
	}
	defer restarted.Shutdown()
	if got := restartedFSM.MaxFileID(); got != 42 {
		t.Fatalf("restarted FSM max_file_id=%d, want 42", got)
	}
}

func TestFileStoreSnapshotRestoreAndCompaction(t *testing.T) {
	network := NewMemoryNetwork()
	peers := []string{"a"}
	store, err := NewFileStore(filepath.Join(t.TempDir(), "raft.json"), decodeTestCommand)
	if err != nil {
		t.Fatalf("NewFileStore() error = %v", err)
	}
	fsm := &testFSM{}
	engine, err := NewEngine(Config{
		ID:                "a",
		Peers:             peers,
		Network:           network,
		Store:             store,
		FSM:               fsm,
		ElectionTimeout:   120 * time.Millisecond,
		HeartbeatInterval: 25 * time.Millisecond,
		SnapshotThreshold: 2,
	})
	if err != nil {
		t.Fatalf("NewEngine() error = %v", err)
	}
	leader := waitNativeLeader(t, engine)
	for _, value := range []uint64{10, 20, 30} {
		if err := leader.Apply(testCommand{maxFileID: value}, 5*time.Second); err != nil {
			t.Fatalf("Apply(%d) error = %v", value, err)
		}
	}
	if err := engine.Shutdown(); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}

	persisted, err := store.load()
	if err != nil {
		t.Fatalf("store.load() error = %v", err)
	}
	if persisted.Snapshot.Index == 0 {
		t.Fatal("expected snapshot to be persisted")
	}
	for _, entry := range persisted.Log {
		if entry.Index <= persisted.Snapshot.Index {
			t.Fatalf("log was not compacted: entry index %d <= snapshot index %d", entry.Index, persisted.Snapshot.Index)
		}
	}

	restartedFSM := &testFSM{}
	restarted, err := NewEngine(Config{
		ID:                "a",
		Peers:             peers,
		Network:           network,
		Store:             store,
		FSM:               restartedFSM,
		ElectionTimeout:   120 * time.Millisecond,
		HeartbeatInterval: 25 * time.Millisecond,
		SnapshotThreshold: 2,
	})
	if err != nil {
		t.Fatalf("restart NewEngine() error = %v", err)
	}
	defer restarted.Shutdown()
	if got := restartedFSM.MaxFileID(); got != 30 {
		t.Fatalf("restarted FSM max_file_id=%d, want 30", got)
	}
}

func waitNativeLeader(t *testing.T, engine *Engine) *Engine {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if engine.IsLeader() {
			return engine
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timeout waiting for native leader")
	return nil
}

func decodeTestCommand(commandType string, payload []byte) (consensus.Command, error) {
	var decoded struct {
		MaxFileID uint64 `json:"max_file_id"`
	}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return nil, err
	}
	return testCommand{maxFileID: decoded.MaxFileID}, nil
}
