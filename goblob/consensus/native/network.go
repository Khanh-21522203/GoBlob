package native

import "sync"

type MemoryNetwork struct {
	mu    sync.RWMutex
	nodes map[string]*Engine
}

func NewMemoryNetwork() *MemoryNetwork {
	return &MemoryNetwork{nodes: make(map[string]*Engine)}
}

func (n *MemoryNetwork) Register(engine *Engine) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.nodes[engine.id] = engine
}

func (n *MemoryNetwork) Unregister(id string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	delete(n.nodes, id)
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
