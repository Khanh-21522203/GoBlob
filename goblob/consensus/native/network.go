package native

import (
	"sync"
)

type Transport interface {
	Start(engine *Engine) error
	Shutdown(id string) error
	RequestVote(target string, req requestVoteRequest) (requestVoteResponse, bool)
	AppendEntries(target string, req appendEntriesRequest) (appendEntriesResponse, bool)
	InstallSnapshot(target string, req installSnapshotRequest) (installSnapshotResponse, bool)
}

type MemoryNetwork struct {
	mu    sync.RWMutex
	nodes map[string]*Engine
}

func NewMemoryNetwork() *MemoryNetwork {
	return &MemoryNetwork{nodes: make(map[string]*Engine)}
}

func (n *MemoryNetwork) Start(engine *Engine) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.nodes[engine.id] = engine
	return nil
}

func (n *MemoryNetwork) Shutdown(id string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	delete(n.nodes, id)
	return nil
}

func (n *MemoryNetwork) RequestVote(target string, req requestVoteRequest) (requestVoteResponse, bool) {
	n.mu.RLock()
	engine := n.nodes[target]
	n.mu.RUnlock()
	if engine == nil {
		return requestVoteResponse{}, false
	}
	return engine.handleRequestVote(req), true
}

func (n *MemoryNetwork) AppendEntries(target string, req appendEntriesRequest) (appendEntriesResponse, bool) {
	n.mu.RLock()
	engine := n.nodes[target]
	n.mu.RUnlock()
	if engine == nil {
		return appendEntriesResponse{}, false
	}
	return engine.handleAppendEntries(req), true
}

func (n *MemoryNetwork) InstallSnapshot(target string, req installSnapshotRequest) (installSnapshotResponse, bool) {
	n.mu.RLock()
	engine := n.nodes[target]
	n.mu.RUnlock()
	if engine == nil {
		return installSnapshotResponse{}, false
	}
	return engine.handleInstallSnapshot(req), true
}

type requestVoteRequest struct {
	Term         uint64
	CandidateID  string
	LastLogIndex uint64
	LastLogTerm  uint64
}

type requestVoteResponse struct {
	Term        uint64
	VoteGranted bool
}

type appendEntriesRequest struct {
	Term         uint64
	LeaderID     string
	PrevLogIndex uint64
	PrevLogTerm  uint64
	Entries      []LogEntry
	LeaderCommit uint64
}

type appendEntriesResponse struct {
	Term       uint64
	Success    bool
	MatchIndex uint64
}

type installSnapshotRequest struct {
	Term              uint64
	LeaderID          string
	LastIncludedIndex uint64
	LastIncludedTerm  uint64
	Data              []byte
}

type installSnapshotResponse struct {
	Term    uint64
	Success bool
}
