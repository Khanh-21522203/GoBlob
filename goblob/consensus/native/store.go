package native

import (
	"sync"

	"GoBlob/goblob/consensus"
)

type LogEntry struct {
	Index   uint64
	Term    uint64
	Command consensus.Command
}

type MemoryStore struct {
	mu          sync.Mutex
	currentTerm uint64
	votedFor    string
	log         []LogEntry
	commitIndex uint64
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{}
}

func (s *MemoryStore) load() (uint64, string, []LogEntry, uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.currentTerm, s.votedFor, append([]LogEntry(nil), s.log...), s.commitIndex
}

func (s *MemoryStore) save(term uint64, votedFor string, log []LogEntry, commitIndex uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.currentTerm = term
	s.votedFor = votedFor
	s.log = append([]LogEntry(nil), log...)
	s.commitIndex = commitIndex
}
