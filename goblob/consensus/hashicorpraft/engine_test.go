package hashicorpraft

import (
	"net"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"GoBlob/goblob/consensus"
	"GoBlob/goblob/consensus/conformance"
	"GoBlob/goblob/raft"
)

func TestConformance(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping consensus conformance tests in short mode")
	}
	conformance.Run(t, hashicorpFactory{})
}

type hashicorpFactory struct{}

func (hashicorpFactory) NewCluster(t *testing.T, size int) conformance.Cluster {
	t.Helper()
	cluster := &hashicorpCluster{
		nodes: make([]*hashicorpNode, size),
		peers: make([]string, size),
	}
	for i := range cluster.nodes {
		httpAddr, raftAddr := freeAddressPair(t)
		cluster.nodes[i] = &hashicorpNode{
			httpAddr: httpAddr,
			raftAddr: raftAddr,
			metaDir:  filepath.Join(t.TempDir(), "raft"),
		}
		cluster.peers[i] = httpAddr
	}
	for i := range cluster.nodes {
		cluster.startNode(t, i)
	}
	return cluster
}

type hashicorpCluster struct {
	nodes []*hashicorpNode
	peers []string
}

func (c *hashicorpCluster) Nodes() []*conformance.Node {
	nodes := make([]*conformance.Node, 0, len(c.nodes))
	for _, node := range c.nodes {
		nodes = append(nodes, node.view)
	}
	return nodes
}

func (c *hashicorpCluster) Command(maxFileID uint64) consensus.Command {
	return raft.MaxFileIdCommand{MaxFileId: maxFileID}
}

func (c *hashicorpCluster) ShutdownNode(t *testing.T, index int) {
	t.Helper()
	node := c.nodes[index]
	if node.view.Engine == nil {
		return
	}
	_ = node.view.Engine.Shutdown()
	node.view.Engine = nil
}

func (c *hashicorpCluster) RestartNode(t *testing.T, index int) {
	t.Helper()
	if c.nodes[index].view.Engine != nil {
		c.ShutdownNode(t, index)
	}
	c.startNode(t, index)
}

func (c *hashicorpCluster) Shutdown() {
	for i := range c.nodes {
		if c.nodes[i].view != nil && c.nodes[i].view.Engine != nil {
			_ = c.nodes[i].view.Engine.Shutdown()
			c.nodes[i].view.Engine = nil
		}
	}
}

func (c *hashicorpCluster) startNode(t *testing.T, index int) {
	t.Helper()
	node := c.nodes[index]
	fsm := raft.NewMasterFSM()
	cfg := &Config{
		NodeId:             node.httpAddr,
		BindAddr:           node.raftAddr,
		MetaDir:            node.metaDir,
		Peers:              c.peers,
		SingleMode:         len(c.peers) == 1,
		SnapshotThreshold:  100,
		SnapshotInterval:   time.Minute,
		HeartbeatTimeout:   200 * time.Millisecond,
		ElectionTimeout:    200 * time.Millisecond,
		LeaderLeaseTimeout: 100 * time.Millisecond,
		CommitTimeout:      50 * time.Millisecond,
		MaxAppendEntries:   32,
	}
	engine, err := NewEngine(cfg, fsm)
	if err != nil {
		t.Fatalf("start node %d: %v", index, err)
	}
	node.fsm = fsm
	if node.view == nil {
		node.view = &conformance.Node{ID: node.httpAddr}
	}
	node.view.Engine = engine
	node.view.Probe = maxFileIDProbe{fsm: fsm}
}

type hashicorpNode struct {
	httpAddr string
	raftAddr string
	metaDir  string
	fsm      *raft.MasterFSM
	view     *conformance.Node
}

type maxFileIDProbe struct {
	fsm *raft.MasterFSM
}

func (p maxFileIDProbe) MaxFileID() uint64 {
	return p.fsm.GetMaxFileId()
}

func freeAddressPair(t *testing.T) (httpAddr string, raftAddr string) {
	t.Helper()
	for attempts := 0; attempts < 100; attempts++ {
		httpListener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen http candidate: %v", err)
		}
		host, portText, err := net.SplitHostPort(httpListener.Addr().String())
		if err != nil {
			_ = httpListener.Close()
			t.Fatalf("split candidate address: %v", err)
		}
		port, err := strconv.Atoi(portText)
		if err != nil || port >= 65535 {
			_ = httpListener.Close()
			continue
		}
		raftCandidate := net.JoinHostPort(host, strconv.Itoa(port+1))
		raftListener, err := net.Listen("tcp", raftCandidate)
		if err != nil {
			_ = httpListener.Close()
			continue
		}
		httpAddr = httpListener.Addr().String()
		raftAddr = raftListener.Addr().String()
		_ = raftListener.Close()
		_ = httpListener.Close()
		return httpAddr, raftAddr
	}
	t.Fatal("failed to allocate adjacent HTTP/Raft test ports")
	return "", ""
}
