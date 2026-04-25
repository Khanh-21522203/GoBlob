package native

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"time"

	"GoBlob/goblob/consensus"
)

type State string

const (
	StateFollower  State = "follower"
	StateCandidate State = "candidate"
	StateLeader    State = "leader"
)

type StateMachine interface {
	Apply(consensus.Command)
}

type Config struct {
	ID                string
	Peers             []string
	Network           *MemoryNetwork
	Store             *MemoryStore
	FSM               StateMachine
	ElectionTimeout   time.Duration
	HeartbeatInterval time.Duration
}

type Engine struct {
	mu                sync.Mutex
	id                string
	peers             []string
	network           *MemoryNetwork
	store             *MemoryStore
	fsm               StateMachine
	state             State
	currentTerm       uint64
	votedFor          string
	log               []LogEntry
	commitIndex       uint64
	lastApplied       uint64
	leaderID          string
	nextIndex         map[string]uint64
	matchIndex        map[string]uint64
	electionReset     time.Time
	electionTimeout   time.Duration
	heartbeatInterval time.Duration
	stopCh            chan struct{}
	closed            bool
	events            eventBus
}

var _ consensus.Engine = (*Engine)(nil)

func NewEngine(cfg Config) (*Engine, error) {
	if cfg.ID == "" {
		return nil, errors.New("native raft: id is required")
	}
	if len(cfg.Peers) == 0 {
		return nil, errors.New("native raft: peers are required")
	}
	if cfg.Network == nil {
		return nil, errors.New("native raft: memory network is required")
	}
	if cfg.Store == nil {
		cfg.Store = NewMemoryStore()
	}
	if cfg.ElectionTimeout <= 0 {
		cfg.ElectionTimeout = 250 * time.Millisecond
	}
	if cfg.HeartbeatInterval <= 0 {
		cfg.HeartbeatInterval = 50 * time.Millisecond
	}

	term, votedFor, entries, commitIndex := cfg.Store.load()
	e := &Engine{
		id:                cfg.ID,
		peers:             append([]string(nil), cfg.Peers...),
		network:           cfg.Network,
		store:             cfg.Store,
		fsm:               cfg.FSM,
		state:             StateFollower,
		currentTerm:       term,
		votedFor:          votedFor,
		log:               entries,
		commitIndex:       commitIndex,
		lastApplied:       0,
		nextIndex:         make(map[string]uint64),
		matchIndex:        make(map[string]uint64),
		electionReset:     time.Now(),
		electionTimeout:   cfg.ElectionTimeout,
		heartbeatInterval: cfg.HeartbeatInterval,
		stopCh:            make(chan struct{}),
		events:            newEventBus(),
	}
	e.applyCommittedLocked()
	cfg.Network.Register(e)

	go e.electionLoop()
	go e.heartbeatLoop()
	return e, nil
}

func (e *Engine) IsLeader() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return !e.closed && e.state == StateLeader
}

func (e *Engine) LeaderAddress() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.leaderID
}

func (e *Engine) Apply(cmd consensus.Command, timeout time.Duration) error {
	if cmd == nil {
		return errors.New("native raft: nil command")
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return errors.New("native raft: engine is closed")
	}
	if e.state != StateLeader {
		leader := e.leaderID
		e.mu.Unlock()
		return fmt.Errorf("not leader, current leader: %s", leader)
	}
	index := e.lastIndexLocked() + 1
	entry := LogEntry{Index: index, Term: e.currentTerm, Command: cmd}
	e.log = append(e.log, entry)
	e.matchIndex[e.id] = index
	e.persistLocked()
	e.mu.Unlock()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		e.replicateAll()
		e.mu.Lock()
		if e.closed {
			e.mu.Unlock()
			return errors.New("native raft: engine is closed")
		}
		if e.state != StateLeader {
			leader := e.leaderID
			e.mu.Unlock()
			return fmt.Errorf("not leader, current leader: %s", leader)
		}
		if e.matchCountLocked(index) >= e.quorumLocked() {
			if index > e.commitIndex {
				e.commitIndex = index
				e.persistLocked()
				e.applyCommittedLocked()
			}
			e.mu.Unlock()
			return nil
		}
		e.mu.Unlock()
		time.Sleep(20 * time.Millisecond)
	}
	return errors.New("native raft: apply timed out waiting for quorum")
}

func (e *Engine) Barrier(timeout time.Duration) error {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		e.mu.Lock()
		ok := e.lastApplied >= e.commitIndex
		closed := e.closed
		e.mu.Unlock()
		if closed {
			return errors.New("native raft: engine is closed")
		}
		if ok {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return errors.New("native raft: barrier timeout")
}

func (e *Engine) AddPeer(addr string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, peer := range e.peers {
		if peer == addr {
			return nil
		}
	}
	e.peers = append(e.peers, addr)
	return nil
}

func (e *Engine) RemovePeer(addr string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	peers := e.peers[:0]
	for _, peer := range e.peers {
		if peer != addr {
			peers = append(peers, peer)
		}
	}
	e.peers = peers
	delete(e.nextIndex, addr)
	delete(e.matchIndex, addr)
	return nil
}

func (e *Engine) Stats() map[string]string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return map[string]string{
		"id":           e.id,
		"state":        string(e.state),
		"term":         fmt.Sprintf("%d", e.currentTerm),
		"commit_index": fmt.Sprintf("%d", e.commitIndex),
		"last_index":   fmt.Sprintf("%d", e.lastIndexLocked()),
		"leader":       e.leaderID,
	}
}

func (e *Engine) Shutdown() error {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil
	}
	e.closed = true
	close(e.stopCh)
	e.mu.Unlock()
	e.network.Unregister(e.id)
	return nil
}

func (e *Engine) Subscribe(name string, bufSize int) <-chan consensus.Event {
	return e.events.Subscribe(name, bufSize)
}

func (e *Engine) Unsubscribe(name string) {
	e.events.Unsubscribe(name)
}

func (e *Engine) electionLoop() {
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-e.stopCh:
			return
		case <-ticker.C:
			e.mu.Lock()
			if e.closed || e.state == StateLeader {
				e.mu.Unlock()
				continue
			}
			elapsed := time.Since(e.electionReset)
			timeout := e.randomizedElectionTimeoutLocked()
			e.mu.Unlock()
			if elapsed >= timeout {
				e.startElection()
			}
		}
	}
}

func (e *Engine) heartbeatLoop() {
	ticker := time.NewTicker(e.heartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-e.stopCh:
			return
		case <-ticker.C:
			if e.IsLeader() {
				e.replicateAll()
			}
		}
	}
}

func (e *Engine) startElection() {
	e.mu.Lock()
	if e.closed || e.state == StateLeader {
		e.mu.Unlock()
		return
	}
	e.state = StateCandidate
	e.currentTerm++
	term := e.currentTerm
	e.votedFor = e.id
	e.leaderID = ""
	e.electionReset = time.Now()
	lastIndex := e.lastIndexLocked()
	lastTerm := e.termAtLocked(lastIndex)
	e.persistLocked()
	peers := e.peerSnapshotLocked()
	e.mu.Unlock()

	votes := 1
	for _, peer := range peers {
		if peer == e.id {
			continue
		}
		resp, ok := e.network.RequestVote(peer, requestVoteRequest{
			Term:         term,
			CandidateID:  e.id,
			LastLogIndex: lastIndex,
			LastLogTerm:  lastTerm,
		})
		if !ok {
			continue
		}
		if resp.Term > term {
			e.mu.Lock()
			e.stepDownLocked(resp.Term, "")
			e.mu.Unlock()
			return
		}
		if resp.VoteGranted {
			votes++
		}
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed || e.state != StateCandidate || e.currentTerm != term {
		return
	}
	if votes >= e.quorumLocked() {
		e.becomeLeaderLocked()
	}
}

func (e *Engine) handleRequestVote(req requestVoteRequest) requestVoteResponse {
	e.mu.Lock()
	defer e.mu.Unlock()

	if req.Term < e.currentTerm {
		return requestVoteResponse{Term: e.currentTerm}
	}
	if req.Term > e.currentTerm {
		e.stepDownLocked(req.Term, "")
	}
	upToDate := req.LastLogTerm > e.termAtLocked(e.lastIndexLocked()) ||
		(req.LastLogTerm == e.termAtLocked(e.lastIndexLocked()) && req.LastLogIndex >= e.lastIndexLocked())
	if (e.votedFor == "" || e.votedFor == req.CandidateID) && upToDate {
		e.votedFor = req.CandidateID
		e.electionReset = time.Now()
		e.persistLocked()
		return requestVoteResponse{Term: e.currentTerm, VoteGranted: true}
	}
	return requestVoteResponse{Term: e.currentTerm}
}

func (e *Engine) handleAppendEntries(req appendEntriesRequest) appendEntriesResponse {
	e.mu.Lock()
	defer e.mu.Unlock()

	if req.Term < e.currentTerm {
		return appendEntriesResponse{Term: e.currentTerm, MatchIndex: e.lastIndexLocked()}
	}
	if req.Term > e.currentTerm || e.state != StateFollower {
		e.stepDownLocked(req.Term, req.LeaderID)
	}
	e.leaderID = req.LeaderID
	e.electionReset = time.Now()

	if req.PrevLogIndex > e.lastIndexLocked() {
		return appendEntriesResponse{Term: e.currentTerm, MatchIndex: e.lastIndexLocked()}
	}
	if req.PrevLogIndex > 0 && e.termAtLocked(req.PrevLogIndex) != req.PrevLogTerm {
		e.truncateAfterLocked(req.PrevLogIndex - 1)
		e.persistLocked()
		return appendEntriesResponse{Term: e.currentTerm, MatchIndex: e.lastIndexLocked()}
	}

	for _, entry := range req.Entries {
		if entry.Index <= e.lastIndexLocked() {
			if e.termAtLocked(entry.Index) != entry.Term {
				e.truncateAfterLocked(entry.Index - 1)
				e.log = append(e.log, entry)
			}
			continue
		}
		e.log = append(e.log, entry)
	}
	if req.LeaderCommit > e.commitIndex {
		e.commitIndex = min(req.LeaderCommit, e.lastIndexLocked())
	}
	e.persistLocked()
	e.applyCommittedLocked()
	return appendEntriesResponse{Term: e.currentTerm, Success: true, MatchIndex: e.lastIndexLocked()}
}

func (e *Engine) replicateAll() {
	e.mu.Lock()
	peers := e.peerSnapshotLocked()
	e.mu.Unlock()
	for _, peer := range peers {
		if peer == e.id {
			continue
		}
		e.replicateTo(peer)
	}
}

func (e *Engine) replicateTo(peer string) {
	e.mu.Lock()
	if e.closed || e.state != StateLeader {
		e.mu.Unlock()
		return
	}
	next := e.nextIndex[peer]
	if next == 0 {
		next = e.lastIndexLocked() + 1
	}
	prev := next - 1
	req := appendEntriesRequest{
		Term:         e.currentTerm,
		LeaderID:     e.id,
		PrevLogIndex: prev,
		PrevLogTerm:  e.termAtLocked(prev),
		Entries:      e.entriesFromLocked(next),
		LeaderCommit: e.commitIndex,
	}
	term := e.currentTerm
	e.mu.Unlock()

	resp, ok := e.network.AppendEntries(peer, req)
	if !ok {
		return
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed || e.currentTerm != term || e.state != StateLeader {
		return
	}
	if resp.Term > e.currentTerm {
		e.stepDownLocked(resp.Term, "")
		return
	}
	if resp.Success {
		e.matchIndex[peer] = resp.MatchIndex
		e.nextIndex[peer] = resp.MatchIndex + 1
		e.advanceCommitLocked()
		return
	}
	if e.nextIndex[peer] > 1 {
		e.nextIndex[peer]--
	}
}

func (e *Engine) becomeLeaderLocked() {
	e.state = StateLeader
	e.leaderID = e.id
	next := e.lastIndexLocked() + 1
	for _, peer := range e.peers {
		e.nextIndex[peer] = next
		e.matchIndex[peer] = 0
	}
	e.matchIndex[e.id] = e.lastIndexLocked()
	e.events.publish(consensus.Event{Kind: consensus.EventLeaderChange, IsLeader: true})
}

func (e *Engine) stepDownLocked(term uint64, leader string) {
	wasLeader := e.state == StateLeader
	e.state = StateFollower
	e.currentTerm = term
	e.votedFor = ""
	e.leaderID = leader
	e.electionReset = time.Now()
	e.persistLocked()
	if wasLeader {
		e.events.publish(consensus.Event{Kind: consensus.EventLeaderChange, IsLeader: false})
	}
}

func (e *Engine) advanceCommitLocked() {
	for idx := e.lastIndexLocked(); idx > e.commitIndex; idx-- {
		if e.termAtLocked(idx) == e.currentTerm && e.matchCountLocked(idx) >= e.quorumLocked() {
			e.commitIndex = idx
			e.persistLocked()
			e.applyCommittedLocked()
			return
		}
	}
}

func (e *Engine) applyCommittedLocked() {
	for e.lastApplied < e.commitIndex {
		e.lastApplied++
		entry, ok := e.entryAtLocked(e.lastApplied)
		if !ok || entry.Command == nil {
			continue
		}
		if e.fsm != nil {
			e.fsm.Apply(entry.Command)
		}
	}
}

func (e *Engine) persistLocked() {
	e.store.save(e.currentTerm, e.votedFor, e.log, e.commitIndex)
}

func (e *Engine) peerSnapshotLocked() []string {
	return append([]string(nil), e.peers...)
}

func (e *Engine) quorumLocked() int {
	return len(e.peers)/2 + 1
}

func (e *Engine) matchCountLocked(index uint64) int {
	count := 0
	for _, peer := range e.peers {
		if peer == e.id {
			if e.lastIndexLocked() >= index {
				count++
			}
			continue
		}
		if e.matchIndex[peer] >= index {
			count++
		}
	}
	return count
}

func (e *Engine) lastIndexLocked() uint64 {
	if len(e.log) == 0 {
		return 0
	}
	return e.log[len(e.log)-1].Index
}

func (e *Engine) termAtLocked(index uint64) uint64 {
	if index == 0 {
		return 0
	}
	for _, entry := range e.log {
		if entry.Index == index {
			return entry.Term
		}
	}
	return 0
}

func (e *Engine) entryAtLocked(index uint64) (LogEntry, bool) {
	for _, entry := range e.log {
		if entry.Index == index {
			return entry, true
		}
	}
	return LogEntry{}, false
}

func (e *Engine) entriesFromLocked(index uint64) []LogEntry {
	if index == 0 {
		index = 1
	}
	entries := make([]LogEntry, 0)
	for _, entry := range e.log {
		if entry.Index >= index {
			entries = append(entries, entry)
		}
	}
	return entries
}

func (e *Engine) truncateAfterLocked(index uint64) {
	kept := e.log[:0]
	for _, entry := range e.log {
		if entry.Index <= index {
			kept = append(kept, entry)
		}
	}
	e.log = kept
	if e.commitIndex > index {
		e.commitIndex = index
	}
	if e.lastApplied > index {
		e.lastApplied = index
	}
}

func (e *Engine) randomizedElectionTimeoutLocked() time.Duration {
	jitter := time.Duration(rand.Int63n(int64(e.electionTimeout)))
	return e.electionTimeout + jitter
}

func min(a, b uint64) uint64 {
	if a < b {
		return a
	}
	return b
}
