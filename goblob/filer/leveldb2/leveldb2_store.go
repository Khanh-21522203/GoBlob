package leveldb2

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/util"

	"GoBlob/goblob/filer"
)

// LevelDB2Store implements filer.FilerStore using LevelDB.
type LevelDB2Store struct {
	dir string
	db  *leveldb.DB
}

// NewLevelDB2Store opens (or creates) a LevelDB database at dir/filer.
func NewLevelDB2Store(dir string) (*LevelDB2Store, error) {
	dbPath := filepath.Join(dir, "filer")
	db, err := leveldb.OpenFile(dbPath, nil)
	if err != nil {
		return nil, fmt.Errorf("leveldb2: open %s: %w", dbPath, err)
	}
	return &LevelDB2Store{dir: dir, db: db}, nil
}

// GetName returns the store name.
func (s *LevelDB2Store) GetName() string { return "leveldb2" }

// Initialize opens (or creates) the LevelDB database using config values.
// Recognized config key: "<prefix>dir" for the database directory.
func (s *LevelDB2Store) Initialize(config map[string]string, prefix string) error {
	dir, ok := config[prefix+"dir"]
	if !ok || dir == "" {
		return fmt.Errorf("leveldb2: config key %qdir is required", prefix)
	}
	dbPath := filepath.Join(dir, "filer")
	db, err := leveldb.OpenFile(dbPath, nil)
	if err != nil {
		return fmt.Errorf("leveldb2: open %s: %w", dbPath, err)
	}
	if s.db != nil {
		s.db.Close()
	}
	s.dir = dir
	s.db = db
	return nil
}

// Shutdown closes the database.
func (s *LevelDB2Store) Shutdown() { s.db.Close() }

// pathKey encodes a (dir, name) pair as a LevelDB key.
func pathKey(dir filer.FullPath, name string) []byte {
	return []byte(string(dir) + "\x00" + name)
}

// kvKey encodes a KV key with a prefix byte to avoid collision with path keys.
func kvKey(key []byte) []byte {
	k := make([]byte, 1+len(key))
	k[0] = 0xff
	copy(k[1:], key)
	return k
}

// marshalEntry serializes an Entry to JSON.
func marshalEntry(entry *filer.Entry) ([]byte, error) {
	return json.Marshal(entry)
}

// unmarshalEntry deserializes an Entry from JSON.
func unmarshalEntry(data []byte) (*filer.Entry, error) {
	var e filer.Entry
	if err := json.Unmarshal(data, &e); err != nil {
		return nil, err
	}
	return &e, nil
}

// InsertEntry writes a new entry to the store.
func (s *LevelDB2Store) InsertEntry(ctx context.Context, entry *filer.Entry) error {
	dir, name := entry.FullPath.DirAndName()
	data, err := marshalEntry(entry)
	if err != nil {
		return fmt.Errorf("leveldb2: marshal entry: %w", err)
	}
	return s.db.Put(pathKey(dir, name), data, nil)
}

// UpdateEntry overwrites an existing entry.
func (s *LevelDB2Store) UpdateEntry(ctx context.Context, entry *filer.Entry) error {
	dir, name := entry.FullPath.DirAndName()
	data, err := marshalEntry(entry)
	if err != nil {
		return fmt.Errorf("leveldb2: marshal entry: %w", err)
	}
	return s.db.Put(pathKey(dir, name), data, nil)
}

// FindEntry retrieves an entry by its full path.
func (s *LevelDB2Store) FindEntry(ctx context.Context, fp filer.FullPath) (*filer.Entry, error) {
	dir, name := fp.DirAndName()
	data, err := s.db.Get(pathKey(dir, name), nil)
	if err == leveldb.ErrNotFound {
		return nil, filer.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("leveldb2: get entry: %w", err)
	}
	return unmarshalEntry(data)
}

// DeleteEntry removes an entry by its full path.
func (s *LevelDB2Store) DeleteEntry(ctx context.Context, fp filer.FullPath) error {
	dir, name := fp.DirAndName()
	return s.db.Delete(pathKey(dir, name), nil)
}

// DeleteFolderChildren deletes all direct children of a directory.
func (s *LevelDB2Store) DeleteFolderChildren(ctx context.Context, fp filer.FullPath) error {
	prefix := []byte(string(fp) + "\x00")
	iter := s.db.NewIterator(util.BytesPrefix(prefix), nil)
	defer iter.Release()

	var keys [][]byte
	for iter.Next() {
		k := make([]byte, len(iter.Key()))
		copy(k, iter.Key())
		keys = append(keys, k)
	}
	if err := iter.Error(); err != nil {
		return fmt.Errorf("leveldb2: iterate folder children: %w", err)
	}

	for _, k := range keys {
		if err := s.db.Delete(k, nil); err != nil {
			return fmt.Errorf("leveldb2: delete child key: %w", err)
		}
	}
	return nil
}

// ListDirectoryEntries lists entries in a directory with pagination support.
func (s *LevelDB2Store) ListDirectoryEntries(ctx context.Context, dirPath filer.FullPath, startFileName string, includeStart bool, limit int64, fn func(*filer.Entry) bool) (string, error) {
	return s.listEntries(ctx, dirPath, startFileName, includeStart, limit, "", fn)
}

// ListDirectoryPrefixedEntries lists entries in a directory filtered by name prefix.
func (s *LevelDB2Store) ListDirectoryPrefixedEntries(ctx context.Context, dirPath filer.FullPath, startFileName string, includeStart bool, limit int64, prefix string, fn func(*filer.Entry) bool) (string, error) {
	return s.listEntries(ctx, dirPath, startFileName, includeStart, limit, prefix, fn)
}

// listEntries is the shared implementation for listing directory entries.
func (s *LevelDB2Store) listEntries(ctx context.Context, dirPath filer.FullPath, startFileName string, includeStart bool, limit int64, namePrefix string, fn func(*filer.Entry) bool) (string, error) {
	dirPrefix := []byte(string(dirPath) + "\x00")

	// Determine seek position.
	var seekKey []byte
	if startFileName == "" {
		seekKey = dirPrefix
	} else {
		seekKey = pathKey(dirPath, startFileName)
	}

	iter := s.db.NewIterator(util.BytesPrefix(dirPrefix), nil)
	defer iter.Release()

	iter.Seek(seekKey)

	// If not includeStart and startFileName is set, skip the exact start key.
	if startFileName != "" && !includeStart && iter.Valid() {
		if bytes.Equal(iter.Key(), pathKey(dirPath, startFileName)) {
			iter.Next()
		}
	}

	var lastName string
	var count int64

	for ; iter.Valid() && count < limit; iter.Next() {
		key := iter.Key()
		if !bytes.HasPrefix(key, dirPrefix) {
			break
		}

		// Extract the file name from the key (everything after the null byte).
		name := string(key[len(dirPrefix):])

		// Apply name prefix filter if specified.
		if namePrefix != "" && !hasPrefix(name, namePrefix) {
			continue
		}

		entry, err := unmarshalEntry(iter.Value())
		if err != nil {
			return lastName, fmt.Errorf("leveldb2: unmarshal entry: %w", err)
		}

		lastName = name
		count++

		if !fn(entry) {
			break
		}
	}

	if err := iter.Error(); err != nil {
		return lastName, fmt.Errorf("leveldb2: iterate directory: %w", err)
	}

	return lastName, nil
}

// hasPrefix returns true if s starts with prefix.
func hasPrefix(s, prefix string) bool {
	if len(s) < len(prefix) {
		return false
	}
	return s[:len(prefix)] == prefix
}

// BeginTransaction is a no-op for LevelDB (no transaction support).
func (s *LevelDB2Store) BeginTransaction(ctx context.Context) (context.Context, error) {
	return ctx, nil
}

// CommitTransaction is a no-op for LevelDB.
func (s *LevelDB2Store) CommitTransaction(ctx context.Context) error {
	return nil
}

// RollbackTransaction is a no-op for LevelDB.
func (s *LevelDB2Store) RollbackTransaction(ctx context.Context) error {
	return nil
}

// KvPut stores a raw key-value pair.
func (s *LevelDB2Store) KvPut(ctx context.Context, key []byte, value []byte) error {
	return s.db.Put(kvKey(key), value, nil)
}

// KvGet retrieves a raw value by key.
func (s *LevelDB2Store) KvGet(ctx context.Context, key []byte) ([]byte, error) {
	data, err := s.db.Get(kvKey(key), nil)
	if err == leveldb.ErrNotFound {
		return nil, filer.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("leveldb2: kv get: %w", err)
	}
	return data, nil
}

// KvDelete removes a raw key-value pair.
func (s *LevelDB2Store) KvDelete(ctx context.Context, key []byte) error {
	return s.db.Delete(kvKey(key), nil)
}
