package memory

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"GoBlob/goblob/filer"
)

// Store is an in-memory filer store used by backend adapters and tests.
type Store struct {
	name string

	mu      sync.RWMutex
	entries map[string]*filer.Entry
	kv      map[string][]byte
}

func New(name string) *Store {
	if strings.TrimSpace(name) == "" {
		name = "memory"
	}
	return &Store{
		name:    name,
		entries: make(map[string]*filer.Entry),
		kv:      make(map[string][]byte),
	}
}

func (s *Store) GetName() string { return s.name }

func (s *Store) Initialize(config map[string]string, prefix string) error {
	_ = config
	_ = prefix
	return nil
}

func (s *Store) Shutdown() {}

func (s *Store) InsertEntry(ctx context.Context, entry *filer.Entry) error {
	return s.upsert(entry)
}

func (s *Store) UpdateEntry(ctx context.Context, entry *filer.Entry) error {
	return s.upsert(entry)
}

func (s *Store) upsert(entry *filer.Entry) error {
	if entry == nil {
		return fmt.Errorf("entry is nil")
	}
	key := string(entry.FullPath)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[key] = cloneEntry(entry)
	return nil
}

func (s *Store) FindEntry(ctx context.Context, fp filer.FullPath) (*filer.Entry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entry, ok := s.entries[string(fp)]
	if !ok {
		return nil, filer.ErrNotFound
	}
	return cloneEntry(entry), nil
}

func (s *Store) DeleteEntry(ctx context.Context, fp filer.FullPath) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.entries, string(fp))
	return nil
}

func (s *Store) DeleteFolderChildren(ctx context.Context, fp filer.FullPath) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	prefix := string(fp)
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	for key := range s.entries {
		if strings.HasPrefix(key, prefix) {
			delete(s.entries, key)
		}
	}
	return nil
}

func (s *Store) ListDirectoryEntries(ctx context.Context, dirPath filer.FullPath, startFileName string, includeStart bool, limit int64, fn func(*filer.Entry) bool) (string, error) {
	return s.listEntries(dirPath, startFileName, includeStart, limit, "", fn)
}

func (s *Store) ListDirectoryPrefixedEntries(ctx context.Context, dirPath filer.FullPath, startFileName string, includeStart bool, limit int64, prefix string, fn func(*filer.Entry) bool) (string, error) {
	return s.listEntries(dirPath, startFileName, includeStart, limit, prefix, fn)
}

func (s *Store) listEntries(dirPath filer.FullPath, startFileName string, includeStart bool, limit int64, prefix string, fn func(*filer.Entry) bool) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	type namedEntry struct {
		name  string
		entry *filer.Entry
	}
	var arr []namedEntry
	for _, entry := range s.entries {
		dir, name := entry.FullPath.DirAndName()
		if dir != dirPath {
			continue
		}
		if prefix != "" && !strings.HasPrefix(name, prefix) {
			continue
		}
		arr = append(arr, namedEntry{name: name, entry: entry})
	}
	sort.Slice(arr, func(i, j int) bool { return arr[i].name < arr[j].name })

	last := ""
	count := int64(0)
	for _, item := range arr {
		if startFileName != "" {
			if includeStart {
				if item.name < startFileName {
					continue
				}
			} else {
				if item.name <= startFileName {
					continue
				}
			}
		}
		last = item.name
		count++
		if fn != nil {
			if !fn(cloneEntry(item.entry)) {
				break
			}
		}
		if limit > 0 && count >= limit {
			break
		}
	}
	return last, nil
}

func (s *Store) BeginTransaction(ctx context.Context) (context.Context, error) { return ctx, nil }
func (s *Store) CommitTransaction(ctx context.Context) error                   { return nil }
func (s *Store) RollbackTransaction(ctx context.Context) error                 { return nil }

func (s *Store) KvPut(ctx context.Context, key []byte, value []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.kv[string(key)] = append([]byte(nil), value...)
	return nil
}

func (s *Store) KvGet(ctx context.Context, key []byte) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.kv[string(key)]
	if !ok {
		return nil, filer.ErrNotFound
	}
	return append([]byte(nil), v...), nil
}

func (s *Store) KvDelete(ctx context.Context, key []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.kv, string(key))
	return nil
}

func cloneEntry(in *filer.Entry) *filer.Entry {
	if in == nil {
		return nil
	}
	out := *in
	if in.Content != nil {
		out.Content = append([]byte(nil), in.Content...)
	}
	if len(in.Chunks) > 0 {
		out.Chunks = make([]*filer.FileChunk, len(in.Chunks))
		for i, c := range in.Chunks {
			if c == nil {
				continue
			}
			cc := *c
			out.Chunks[i] = &cc
		}
	}
	if in.Extended != nil {
		out.Extended = make(map[string][]byte, len(in.Extended))
		for k, v := range in.Extended {
			out.Extended[k] = append([]byte(nil), v...)
		}
	}
	if in.HardLinkId != nil {
		out.HardLinkId = append([]byte(nil), in.HardLinkId...)
	}
	if in.Remote != nil {
		rc := *in.Remote
		out.Remote = &rc
	}
	return &out
}
