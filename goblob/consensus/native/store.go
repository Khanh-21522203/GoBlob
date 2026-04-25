package native

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"GoBlob/goblob/consensus"
)

type LogEntry struct {
	Index   uint64
	Term    uint64
	Command consensus.Command
}

type CommandDecoder func(commandType string, payload []byte) (consensus.Command, error)

type SnapshotRecord struct {
	Index uint64
	Term  uint64
	Data  []byte
}

type persistedState struct {
	CurrentTerm uint64
	VotedFor    string
	Log         []LogEntry
	CommitIndex uint64
	Snapshot    SnapshotRecord
}

type Store interface {
	load() (persistedState, error)
	save(persistedState) error
}

type MemoryStore struct {
	mu          sync.Mutex
	currentTerm uint64
	votedFor    string
	log         []LogEntry
	commitIndex uint64
	snapshot    SnapshotRecord
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{}
}

func (s *MemoryStore) load() (persistedState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return persistedState{
		CurrentTerm: s.currentTerm,
		VotedFor:    s.votedFor,
		Log:         append([]LogEntry(nil), s.log...),
		CommitIndex: s.commitIndex,
		Snapshot:    copySnapshot(s.snapshot),
	}, nil
}

func (s *MemoryStore) save(state persistedState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.currentTerm = state.CurrentTerm
	s.votedFor = state.VotedFor
	s.log = append([]LogEntry(nil), state.Log...)
	s.commitIndex = state.CommitIndex
	s.snapshot = copySnapshot(state.Snapshot)
	return nil
}

type FileStore struct {
	mu      sync.Mutex
	path    string
	decoder CommandDecoder
}

func NewFileStore(path string, decoder CommandDecoder) (*FileStore, error) {
	if path == "" {
		return nil, fmt.Errorf("native raft file store: path is required")
	}
	if decoder == nil {
		return nil, fmt.Errorf("native raft file store: command decoder is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}
	return &FileStore{path: path, decoder: decoder}, nil
}

func (s *FileStore) load() (persistedState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return persistedState{}, nil
	}
	if err != nil {
		return persistedState{}, err
	}
	var disk diskState
	if err := json.Unmarshal(raw, &disk); err != nil {
		return persistedState{}, err
	}
	entries := make([]LogEntry, 0, len(disk.Log))
	for _, de := range disk.Log {
		cmd, err := s.decoder(de.Type, de.Payload)
		if err != nil {
			return persistedState{}, err
		}
		entries = append(entries, LogEntry{Index: de.Index, Term: de.Term, Command: cmd})
	}
	return persistedState{
		CurrentTerm: disk.CurrentTerm,
		VotedFor:    disk.VotedFor,
		Log:         entries,
		CommitIndex: disk.CommitIndex,
		Snapshot: SnapshotRecord{
			Index: disk.Snapshot.Index,
			Term:  disk.Snapshot.Term,
			Data:  append([]byte(nil), disk.Snapshot.Data...),
		},
	}, nil
}

func (s *FileStore) save(state persistedState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	disk := diskState{
		CurrentTerm: state.CurrentTerm,
		VotedFor:    state.VotedFor,
		CommitIndex: state.CommitIndex,
		Snapshot: diskSnapshot{
			Index: state.Snapshot.Index,
			Term:  state.Snapshot.Term,
			Data:  append([]byte(nil), state.Snapshot.Data...),
		},
	}
	for _, entry := range state.Log {
		if entry.Command == nil {
			continue
		}
		payload, err := entry.Command.Encode()
		if err != nil {
			return err
		}
		disk.Log = append(disk.Log, diskEntry{
			Index:   entry.Index,
			Term:    entry.Term,
			Type:    entry.Command.Type(),
			Payload: payload,
		})
	}
	raw, err := json.MarshalIndent(disk, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

type diskState struct {
	CurrentTerm uint64       `json:"current_term"`
	VotedFor    string       `json:"voted_for"`
	Log         []diskEntry  `json:"log"`
	CommitIndex uint64       `json:"commit_index"`
	Snapshot    diskSnapshot `json:"snapshot"`
}

type diskEntry struct {
	Index   uint64          `json:"index"`
	Term    uint64          `json:"term"`
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

type diskSnapshot struct {
	Index uint64 `json:"index"`
	Term  uint64 `json:"term"`
	Data  []byte `json:"data"`
}

func copySnapshot(s SnapshotRecord) SnapshotRecord {
	return SnapshotRecord{
		Index: s.Index,
		Term:  s.Term,
		Data:  append([]byte(nil), s.Data...),
	}
}
