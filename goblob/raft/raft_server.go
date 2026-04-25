package raft

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"GoBlob/goblob/consensus"

	"github.com/hashicorp/raft"
	raftboltdb "github.com/hashicorp/raft-boltdb/v2"
)

type RaftServer = consensus.Engine

type consensusEventBus struct {
	mu          sync.RWMutex
	subscribers map[string]chan consensus.Event
}

func newConsensusEventBus() consensusEventBus {
	return consensusEventBus{subscribers: make(map[string]chan consensus.Event)}
}

func (b *consensusEventBus) Subscribe(name string, bufSize int) <-chan consensus.Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	if old, ok := b.subscribers[name]; ok {
		close(old)
	}
	ch := make(chan consensus.Event, bufSize)
	b.subscribers[name] = ch
	return ch
}

func (b *consensusEventBus) Unsubscribe(name string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if ch, ok := b.subscribers[name]; ok {
		close(ch)
		delete(b.subscribers, name)
	}
}

func (b *consensusEventBus) publish(e consensus.Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, ch := range b.subscribers {
		select {
		case ch <- e:
		default:
		}
	}
}

// raftServerImpl is the concrete implementation of RaftServer.
type raftServerImpl struct {
	eventBus      consensusEventBus
	cfg           *RaftConfig
	raft          *raft.Raft
	fsm           *MasterFSM
	transport     raft.Transport
	logStore      *raftboltdb.BoltStore
	stableStore   *raftboltdb.BoltStore
	leaderChClose chan struct{}
	closeOnce     sync.Once
}

// NewRaftServer creates and starts a new Raft server.
func NewRaftServer(cfg *RaftConfig, fsm *MasterFSM) (RaftServer, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	if err := os.MkdirAll(cfg.MetaDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create meta dir: %w", err)
	}

	rs := &raftServerImpl{
		eventBus:      newConsensusEventBus(),
		cfg:           cfg,
		fsm:           fsm,
		leaderChClose: make(chan struct{}),
	}

	if err := rs.setup(); err != nil {
		return nil, err
	}

	go rs.observeLeader()
	return rs, nil
}

func (rs *raftServerImpl) setup() error {
	// Create TCP transport.
	addr, err := net.ResolveTCPAddr("tcp", rs.cfg.BindAddr)
	if err != nil {
		return fmt.Errorf("failed to resolve bind address: %w", err)
	}
	transport, err := raft.NewTCPTransport(rs.cfg.BindAddr, addr, 3, 10*time.Second, os.Stderr)
	if err != nil {
		return fmt.Errorf("failed to create TCP transport: %w", err)
	}
	rs.transport = transport

	// Separate BoltDB stores for log and stable state (spec requirement).
	logStore, err := raftboltdb.NewBoltStore(filepath.Join(rs.cfg.MetaDir, "raft-log.bolt"))
	if err != nil {
		return fmt.Errorf("failed to create log store: %w", err)
	}
	rs.logStore = logStore

	stableStore, err := raftboltdb.NewBoltStore(filepath.Join(rs.cfg.MetaDir, "raft-stable.bolt"))
	if err != nil {
		logStore.Close()
		return fmt.Errorf("failed to create stable store: %w", err)
	}
	rs.stableStore = stableStore

	// Snapshot store — retain 3 snapshots.
	snapshots, err := raft.NewFileSnapshotStore(filepath.Join(rs.cfg.MetaDir, "snapshots"), 3, os.Stderr)
	if err != nil {
		return fmt.Errorf("failed to create snapshot store: %w", err)
	}

	// Build Raft configuration.
	raftCfg := raft.DefaultConfig()
	// Use the actual bound address as LocalID so it survives port=0 tests.
	nodeId := rs.cfg.NodeId
	if nodeId == "" {
		nodeId = string(transport.LocalAddr())
	}
	raftCfg.LocalID = raft.ServerID(nodeId)
	raftCfg.SnapshotThreshold = rs.cfg.SnapshotThreshold
	raftCfg.SnapshotInterval = rs.cfg.SnapshotInterval
	raftCfg.HeartbeatTimeout = rs.cfg.HeartbeatTimeout
	raftCfg.ElectionTimeout = rs.cfg.ElectionTimeout
	raftCfg.LeaderLeaseTimeout = rs.cfg.LeaderLeaseTimeout
	raftCfg.CommitTimeout = rs.cfg.CommitTimeout
	if rs.cfg.MaxAppendEntries > 0 {
		raftCfg.MaxAppendEntries = rs.cfg.MaxAppendEntries
	}

	// Bootstrap if no existing state.
	hasState, err := raft.HasExistingState(logStore, stableStore, snapshots)
	if err != nil {
		return fmt.Errorf("failed to check existing state: %w", err)
	}
	if !hasState {
		configuration := rs.buildBootstrapConfig(raftCfg.LocalID, transport.LocalAddr())
		if err := raft.BootstrapCluster(raftCfg, logStore, stableStore, snapshots, transport, configuration); err != nil {
			return fmt.Errorf("failed to bootstrap: %w", err)
		}
	}

	r, err := raft.NewRaft(raftCfg, rs.fsm, logStore, stableStore, snapshots, transport)
	if err != nil {
		return fmt.Errorf("failed to create raft: %w", err)
	}
	rs.raft = r
	return nil
}

// buildBootstrapConfig constructs the initial cluster configuration.
// Peers are HTTP addresses (host:httpPort); the Raft transport address is
// derived as host:httpPort+1, matching the convention in master_server.go.
func (rs *raftServerImpl) buildBootstrapConfig(localID raft.ServerID, localAddr raft.ServerAddress) raft.Configuration {
	if rs.cfg.SingleMode || len(rs.cfg.Peers) == 0 {
		// Single-node: only self as voter; becomes leader immediately.
		return raft.Configuration{
			Servers: []raft.Server{
				{ID: localID, Address: localAddr},
			},
		}
	}

	// Multi-node: peers are HTTP addresses.
	// ID = HTTP address (same as NodeId format so LeaderAddress() is usable).
	// Address = Raft transport address (httpPort+1).
	servers := make([]raft.Server, 0, len(rs.cfg.Peers))
	for _, peer := range rs.cfg.Peers {
		servers = append(servers, raft.Server{
			ID:      raft.ServerID(peer),
			Address: raft.ServerAddress(httpAddrToRaftAddr(peer)),
		})
	}
	return raft.Configuration{Servers: servers}
}

// httpAddrToRaftAddr derives the Raft transport address from an HTTP address.
// The Raft port is httpPort+1 — the same convention used in master_server.go.
func httpAddrToRaftAddr(httpAddr string) string {
	host, portStr, err := net.SplitHostPort(httpAddr)
	if err != nil {
		return httpAddr
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return httpAddr
	}
	return net.JoinHostPort(host, strconv.Itoa(port+1))
}

// observeLeader watches for leader transitions and publishes EventLeaderChange.
func (rs *raftServerImpl) observeLeader() {
	for {
		select {
		case isLeader := <-rs.raft.LeaderCh():
			rs.eventBus.publish(consensus.Event{Kind: consensus.EventLeaderChange, IsLeader: isLeader})
		case <-rs.leaderChClose:
			return
		}
	}
}

// IsLeader returns true if this node is the current Raft leader.
func (rs *raftServerImpl) IsLeader() bool {
	return rs.raft.State() == raft.Leader
}

// LeaderAddress returns the HTTP address of the current cluster leader.
// We store the node's HTTP address as the Raft ServerID (see master_server.go),
// so the ServerID is what clients should use with ToGrpcAddress() to get the
// gRPC endpoint. The Raft ServerAddress is the internal Raft-transport port and
// is not useful to external callers.
func (rs *raftServerImpl) LeaderAddress() string {
	_, id := rs.raft.LeaderWithID()
	return string(id)
}

// Apply submits a command to the Raft log and waits for consensus.
func (rs *raftServerImpl) Apply(cmd consensus.Command, timeout time.Duration) error {
	if !rs.IsLeader() {
		return fmt.Errorf("not leader, current leader: %s", rs.LeaderAddress())
	}

	data, err := encodeLogEntry(cmd)
	if err != nil {
		return fmt.Errorf("failed to encode command: %w", err)
	}

	future := rs.raft.Apply(data, timeout)
	if err := future.Error(); err != nil {
		return fmt.Errorf("failed to apply command: %w", err)
	}
	if resp := future.Response(); resp != nil {
		if respErr, ok := resp.(error); ok && respErr != nil {
			return fmt.Errorf("fsm returned error: %w", respErr)
		}
	}
	return nil
}

// Barrier waits until all log entries up to this point are applied to the FSM.
func (rs *raftServerImpl) Barrier(timeout time.Duration) error {
	future := rs.raft.Barrier(timeout)
	return future.Error()
}

// AddPeer adds a voting node to the Raft cluster.
func (rs *raftServerImpl) AddPeer(addr string) error {
	future := rs.raft.AddVoter(raft.ServerID(addr), raft.ServerAddress(addr), 0, 0)
	return future.Error()
}

// RemovePeer removes a node from the Raft cluster.
func (rs *raftServerImpl) RemovePeer(addr string) error {
	future := rs.raft.RemoveServer(raft.ServerID(addr), 0, 0)
	return future.Error()
}

// Stats returns Raft runtime statistics.
func (rs *raftServerImpl) Stats() map[string]string {
	return rs.raft.Stats()
}

func (rs *raftServerImpl) Subscribe(name string, bufSize int) <-chan consensus.Event {
	return rs.eventBus.Subscribe(name, bufSize)
}

func (rs *raftServerImpl) Unsubscribe(name string) {
	rs.eventBus.Unsubscribe(name)
}

// Shutdown gracefully shuts down the Raft server and closes all stores.
func (rs *raftServerImpl) Shutdown() error {
	rs.closeOnce.Do(func() { close(rs.leaderChClose) })
	var firstErr error
	if rs.raft != nil {
		if err := rs.raft.Shutdown().Error(); err != nil {
			firstErr = err
		}
	}
	// Close bolt stores after raft is shut down so file locks are released.
	if rs.logStore != nil {
		rs.logStore.Close()
	}
	if rs.stableStore != nil {
		rs.stableStore.Close()
	}
	return firstErr
}

// RedirectToLeader redirects an HTTP request to the current Raft leader.
// Returns true if the request was redirected (caller should stop processing).
func RedirectToLeader(rs RaftServer, w http.ResponseWriter, r *http.Request) bool {
	if rs.IsLeader() {
		return false
	}
	leaderAddr := rs.LeaderAddress()
	if leaderAddr == "" {
		http.Error(w, "no leader elected", http.StatusServiceUnavailable)
		return true
	}
	http.Redirect(w, r, "http://"+leaderAddr+r.RequestURI, http.StatusTemporaryRedirect)
	return true
}
