package native

import (
	"encoding/json"
	"fmt"
	"net"
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

func TestTCPTransportConformance(t *testing.T) {
	conformance.Run(t, nativeTCPFactory{})
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

type nativeTCPFactory struct{}

func (nativeTCPFactory) NewCluster(t *testing.T, size int) conformance.Cluster {
	t.Helper()
	addrs := reserveTCPAddrs(t, size)
	cluster := &nativeTCPCluster{
		nodes: make([]*nativeTCPNode, size),
		peers: make([]string, size),
		addrs: make(map[string]string, size),
	}
	for i := range cluster.nodes {
		id := fmt.Sprintf("node-%d", i)
		cluster.peers[i] = id
		cluster.addrs[id] = addrs[i]
		cluster.nodes[i] = &nativeTCPNode{
			id:    id,
			addr:  addrs[i],
			store: NewMemoryStore(),
		}
	}
	for i := range cluster.nodes {
		cluster.startNode(t, i)
	}
	return cluster
}

type nativeTCPCluster struct {
	nodes             []*nativeTCPNode
	peers             []string
	addrs             map[string]string
	snapshotThreshold uint64
}

func (c *nativeTCPCluster) Nodes() []*conformance.Node {
	nodes := make([]*conformance.Node, 0, len(c.nodes))
	for _, node := range c.nodes {
		nodes = append(nodes, node.view)
	}
	return nodes
}

func (c *nativeTCPCluster) Command(maxFileID uint64) consensus.Command {
	return testCommand{maxFileID: maxFileID}
}

func (c *nativeTCPCluster) ShutdownNode(t *testing.T, index int) {
	t.Helper()
	node := c.nodes[index]
	if node.view.Engine == nil {
		return
	}
	_ = node.view.Engine.Shutdown()
	node.view.Engine = nil
}

func (c *nativeTCPCluster) RestartNode(t *testing.T, index int) {
	t.Helper()
	if c.nodes[index].view.Engine != nil {
		c.ShutdownNode(t, index)
	}
	c.startNode(t, index)
}

func (c *nativeTCPCluster) Shutdown() {
	for i := range c.nodes {
		if c.nodes[i].view != nil && c.nodes[i].view.Engine != nil {
			_ = c.nodes[i].view.Engine.Shutdown()
			c.nodes[i].view.Engine = nil
		}
	}
}

func (c *nativeTCPCluster) startNode(t *testing.T, index int) {
	t.Helper()
	node := c.nodes[index]
	transport, err := NewTCPTransport(node.addr, c.addrs, decodeTestCommand)
	if err != nil {
		t.Fatalf("NewTCPTransport(%s) error = %v", node.id, err)
	}
	transport.SetTimeout(300 * time.Millisecond)
	fsm := &testFSM{}
	engine, err := NewEngine(Config{
		ID:                node.id,
		Peers:             c.peers,
		Transport:         transport,
		Store:             node.store,
		FSM:               fsm,
		ElectionTimeout:   160 * time.Millisecond,
		HeartbeatInterval: 35 * time.Millisecond,
		SnapshotThreshold: c.snapshotThreshold,
	})
	if err != nil {
		t.Fatalf("start TCP node %s: %v", node.id, err)
	}
	node.fsm = fsm
	if node.view == nil {
		node.view = &conformance.Node{ID: node.id}
	}
	node.view.Engine = engine
	node.view.Probe = fsm
}

type nativeTCPNode struct {
	id    string
	addr  string
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

func TestTCPTransportInstallSnapshotCatchesUpFollower(t *testing.T) {
	addrs := reserveTCPAddrs(t, 3)
	cluster := &nativeTCPCluster{
		nodes:             make([]*nativeTCPNode, 3),
		peers:             make([]string, 3),
		addrs:             make(map[string]string, 3),
		snapshotThreshold: 2,
	}
	for i := range cluster.nodes {
		id := fmt.Sprintf("node-%d", i)
		cluster.peers[i] = id
		cluster.addrs[id] = addrs[i]
		cluster.nodes[i] = &nativeTCPNode{
			id:    id,
			addr:  addrs[i],
			store: NewMemoryStore(),
		}
	}
	for i := range cluster.nodes {
		cluster.startNode(t, i)
	}
	defer cluster.Shutdown()

	nodes := cluster.Nodes()
	leader := waitNativeClusterLeader(t, nodes)
	follower := firstNativeFollower(nodes, leader)
	if follower == nil {
		t.Fatal("no follower available")
	}
	followerIndex := indexOfNativeNode(nodes, follower)
	cluster.ShutdownNode(t, followerIndex)

	for _, value := range []uint64{10, 20, 30} {
		if err := leader.Engine.Apply(testCommand{maxFileID: value}, 5*time.Second); err != nil {
			t.Fatalf("Apply(%d) error = %v", value, err)
		}
	}
	waitForNativeValue(t, liveNativeNodes(nodes), 30, 5*time.Second)

	cluster.RestartNode(t, followerIndex)
	waitForNativeValue(t, nodes, 30, 5*time.Second)
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

func waitNativeClusterLeader(t *testing.T, nodes []*conformance.Node) *conformance.Node {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var leader *conformance.Node
		count := 0
		for _, node := range nodes {
			if node == nil || node.Engine == nil {
				continue
			}
			if node.Engine.IsLeader() {
				leader = node
				count++
			}
		}
		if count == 1 {
			return leader
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timeout waiting for native cluster leader")
	return nil
}

func waitForNativeValue(t *testing.T, nodes []*conformance.Node, want uint64, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ok := true
		for _, node := range nodes {
			if node == nil || node.Engine == nil || node.Probe == nil {
				continue
			}
			if node.Probe.MaxFileID() != want {
				ok = false
				break
			}
		}
		if ok {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	for _, node := range nodes {
		if node == nil || node.Engine == nil || node.Probe == nil {
			continue
		}
		t.Errorf("node %s: max_file_id=%d, want %d", node.ID, node.Probe.MaxFileID(), want)
	}
	t.Fatal("timeout waiting for native value")
}

func firstNativeFollower(nodes []*conformance.Node, leader *conformance.Node) *conformance.Node {
	for _, node := range nodes {
		if node == nil || node.Engine == nil || node == leader {
			continue
		}
		if !node.Engine.IsLeader() {
			return node
		}
	}
	return nil
}

func liveNativeNodes(nodes []*conformance.Node) []*conformance.Node {
	live := make([]*conformance.Node, 0, len(nodes))
	for _, node := range nodes {
		if node != nil && node.Engine != nil {
			live = append(live, node)
		}
	}
	return live
}

func indexOfNativeNode(nodes []*conformance.Node, target *conformance.Node) int {
	for i, node := range nodes {
		if node == target {
			return i
		}
	}
	return -1
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

func BenchmarkTransportHeartbeatRoundTrip(b *testing.B) {
	benchmarkTransports(b, func(b *testing.B, transport Transport, target string) {
		req := appendEntriesRequest{
			Term:         1,
			LeaderID:     "leader",
			LeaderCommit: 0,
		}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, ok := transport.AppendEntries(target, req); !ok {
				b.Fatal("AppendEntries heartbeat failed")
			}
		}
	})
}

func BenchmarkTransportAppendRoundTrip(b *testing.B) {
	benchmarkTransports(b, func(b *testing.B, transport Transport, target string) {
		req := appendEntriesRequest{
			Term:         1,
			LeaderID:     "leader",
			PrevLogIndex: 0,
			PrevLogTerm:  0,
			Entries: []LogEntry{{
				Index:   1,
				Term:    1,
				Command: testCommand{maxFileID: 99},
			}},
			LeaderCommit: 1,
		}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, ok := transport.AppendEntries(target, req); !ok {
				b.Fatal("AppendEntries failed")
			}
		}
	})
}

func BenchmarkTransportBatchAppendThroughput(b *testing.B) {
	const batchSize = 64
	entries := make([]LogEntry, 0, batchSize)
	for i := 0; i < batchSize; i++ {
		entries = append(entries, LogEntry{
			Index:   uint64(i + 1),
			Term:    1,
			Command: testCommand{maxFileID: uint64(i + 1)},
		})
	}
	benchmarkTransports(b, func(b *testing.B, transport Transport, target string) {
		req := appendEntriesRequest{
			Term:         1,
			LeaderID:     "leader",
			Entries:      entries,
			LeaderCommit: batchSize,
		}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, ok := transport.AppendEntries(target, req); !ok {
				b.Fatal("batch AppendEntries failed")
			}
		}
		b.ReportMetric(float64(b.N*batchSize)/b.Elapsed().Seconds(), "entries/s")
	})
}

func BenchmarkTransportSnapshotTransferThroughput(b *testing.B) {
	padding := make([]byte, 1024*1024)
	for i := range padding {
		padding[i] = 'a'
	}
	snapshot, err := json.Marshal(struct {
		MaxFileID uint64 `json:"max_file_id"`
		Padding   string `json:"padding"`
	}{MaxFileID: 128, Padding: string(padding)})
	if err != nil {
		b.Fatalf("marshal snapshot: %v", err)
	}
	benchmarkTransports(b, func(b *testing.B, transport Transport, target string) {
		req := installSnapshotRequest{
			Term:              1,
			LeaderID:          "leader",
			LastIncludedIndex: 128,
			LastIncludedTerm:  1,
			Data:              snapshot,
		}
		b.SetBytes(int64(len(snapshot)))
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			resp, ok := transport.InstallSnapshot(target, req)
			if !ok {
				b.Fatal("InstallSnapshot failed")
			}
			if !resp.Success {
				b.Fatal("InstallSnapshot response was not successful")
			}
		}
	})
}

func BenchmarkNativeAssignApply(b *testing.B) {
	b.Run("memory_single_node", func(b *testing.B) {
		network := NewMemoryNetwork()
		engine, err := NewEngine(Config{
			ID:                "node-0",
			Peers:             []string{"node-0"},
			Transport:         network,
			Store:             NewMemoryStore(),
			FSM:               &testFSM{},
			ElectionTimeout:   120 * time.Millisecond,
			HeartbeatInterval: 25 * time.Millisecond,
		})
		if err != nil {
			b.Fatalf("NewEngine() error = %v", err)
		}
		defer engine.Shutdown()
		waitBenchmarkLeader(b, []*Engine{engine})
		benchmarkAssignApply(b, engine)
	})

	b.Run("tcp_three_node", func(b *testing.B) {
		addrs := reserveTCPAddrsForBenchmark(b, 3)
		peers := make([]string, 3)
		peerAddrs := make(map[string]string, 3)
		engines := make([]*Engine, 3)
		for i := range peers {
			id := fmt.Sprintf("node-%d", i)
			peers[i] = id
			peerAddrs[id] = addrs[i]
		}
		for i, id := range peers {
			transport, err := NewTCPTransport(addrs[i], peerAddrs, decodeTestCommand)
			if err != nil {
				b.Fatalf("NewTCPTransport(%s) error = %v", id, err)
			}
			transport.SetTimeout(300 * time.Millisecond)
			engines[i], err = NewEngine(Config{
				ID:                id,
				Peers:             peers,
				Transport:         transport,
				Store:             NewMemoryStore(),
				FSM:               &testFSM{},
				ElectionTimeout:   160 * time.Millisecond,
				HeartbeatInterval: 35 * time.Millisecond,
			})
			if err != nil {
				b.Fatalf("NewEngine(%s) error = %v", id, err)
			}
			defer engines[i].Shutdown()
		}
		leader := waitBenchmarkLeader(b, engines)
		benchmarkAssignApply(b, leader)
	})
}

func benchmarkTransports(b *testing.B, run func(*testing.B, Transport, string)) {
	b.Helper()
	b.Run("memory", func(b *testing.B) {
		network := NewMemoryNetwork()
		follower, err := NewEngine(Config{
			ID:                "follower",
			Peers:             []string{"leader", "follower"},
			Transport:         network,
			Store:             NewMemoryStore(),
			FSM:               &testFSM{},
			ElectionTimeout:   time.Hour,
			HeartbeatInterval: time.Hour,
		})
		if err != nil {
			b.Fatalf("NewEngine() error = %v", err)
		}
		defer follower.Shutdown()
		run(b, network, "follower")
	})

	b.Run("tcp", func(b *testing.B) {
		addr := reserveTCPAddrsForBenchmark(b, 1)[0]
		transport, err := NewTCPTransport(addr, map[string]string{"follower": addr}, decodeTestCommand)
		if err != nil {
			b.Fatalf("NewTCPTransport() error = %v", err)
		}
		follower, err := NewEngine(Config{
			ID:                "follower",
			Peers:             []string{"leader", "follower"},
			Transport:         transport,
			Store:             NewMemoryStore(),
			FSM:               &testFSM{},
			ElectionTimeout:   time.Hour,
			HeartbeatInterval: time.Hour,
		})
		if err != nil {
			b.Fatalf("NewEngine() error = %v", err)
		}
		defer follower.Shutdown()
		run(b, transport, "follower")
	})
}

func benchmarkAssignApply(b *testing.B, leader *Engine) {
	b.Helper()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := leader.Apply(testCommand{maxFileID: uint64(i + 1)}, 5*time.Second); err != nil {
			b.Fatalf("Apply() error = %v", err)
		}
	}
	b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "assigns/s")
}

func waitBenchmarkLeader(b *testing.B, engines []*Engine) *Engine {
	b.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var leader *Engine
		count := 0
		for _, engine := range engines {
			if engine != nil && engine.IsLeader() {
				leader = engine
				count++
			}
		}
		if count == 1 {
			return leader
		}
		time.Sleep(20 * time.Millisecond)
	}
	b.Fatal("timeout waiting for benchmark leader")
	return nil
}

func reserveTCPAddrs(t *testing.T, size int) []string {
	t.Helper()
	listeners := make([]net.Listener, 0, size)
	addrs := make([]string, 0, size)
	for i := 0; i < size; i++ {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			for _, listener := range listeners {
				_ = listener.Close()
			}
			t.Fatalf("reserve TCP address: %v", err)
		}
		listeners = append(listeners, ln)
		addrs = append(addrs, ln.Addr().String())
	}
	for _, listener := range listeners {
		_ = listener.Close()
	}
	return addrs
}

func reserveTCPAddrsForBenchmark(b *testing.B, size int) []string {
	b.Helper()
	listeners := make([]net.Listener, 0, size)
	addrs := make([]string, 0, size)
	for i := 0; i < size; i++ {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			for _, listener := range listeners {
				_ = listener.Close()
			}
			b.Fatalf("reserve TCP address: %v", err)
		}
		listeners = append(listeners, ln)
		addrs = append(addrs, ln.Addr().String())
	}
	for _, listener := range listeners {
		_ = listener.Close()
	}
	return addrs
}
