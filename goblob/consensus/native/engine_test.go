package native

import (
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
	return nil, nil
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
