package volume

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"GoBlob/goblob/core/types"
)

// NeedleMap defines the interface for the needle index.
// Each volume has one NeedleMap that tracks NeedleId → (Offset, Size).
type NeedleMap interface {
	// Put records or updates the location of a needle.
	Put(key types.NeedleId, offset types.Offset, size types.Size) error
	// Get returns the offset and size for the given key, or error if not found/deleted.
	Get(key types.NeedleId) (types.Offset, types.Size, error)
	// Delete marks a needle as deleted (writes a tombstone with Size=0).
	Delete(key types.NeedleId) error
	// Close flushes pending writes and releases resources.
	Close() error
	// Load replays an .idx file into the map (used on startup).
	Load(filename string) error

	// Statistics
	ContentSize() uint64
	DeletedSize() uint64
	FileCount() int64
	DeletedCount() int64
}

// needleValue holds one index entry in memory.
type needleValue struct {
	offset types.Offset
	size   types.Size
}

func (nv needleValue) isDeleted() bool { return nv.size == types.TombstoneFileSize }

// MemDb is an in-memory NeedleMap that appends entries to an .idx file.
type MemDb struct {
	mu           sync.RWMutex
	m            map[types.NeedleId]*needleValue
	file         *os.File // .idx file opened for append; may be nil if no persistence
	contentSize  uint64
	deletedSize  uint64
	fileCount    int64
	deletedCount int64
}

// NewMemDb creates a new in-memory needle map with no backing file.
func NewMemDb() *MemDb {
	return &MemDb{
		m: make(map[types.NeedleId]*needleValue),
	}
}

// Get retrieves a needle's location. Returns error if not found or deleted.
func (m *MemDb) Get(id types.NeedleId) (types.Offset, types.Size, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	v, ok := m.m[id]
	if !ok {
		return 0, 0, fmt.Errorf("needle %d not found", id)
	}
	if v.isDeleted() {
		return 0, 0, fmt.Errorf("needle %d is deleted", id)
	}
	return v.offset, v.size, nil
}

// Put stores a needle's location and appends a 16-byte entry to the .idx file.
func (m *MemDb) Put(key types.NeedleId, offset types.Offset, size types.Size) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	old, exists := m.m[key]

	// Update statistics
	if exists {
		if old.isDeleted() {
			m.deletedCount--
			m.deletedSize -= uint64(old.size)
		} else {
			m.fileCount--
			m.contentSize -= uint64(old.size)
		}
	}
	if size == types.TombstoneFileSize {
		m.deletedCount++
		// deletedSize tracks what was deleted; for a tombstone write size=0
	} else {
		m.fileCount++
		m.contentSize += uint64(size)
	}

	m.m[key] = &needleValue{offset: offset, size: size}

	// Append to .idx file if open
	if m.file != nil {
		return m.appendEntry(key, offset, size)
	}
	return nil
}

// Delete marks a needle as deleted by writing a tombstone entry (Size=0).
func (m *MemDb) Delete(key types.NeedleId) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	old, exists := m.m[key]
	if !exists {
		return fmt.Errorf("needle %d not found", key)
	}
	if old.isDeleted() {
		return nil // already deleted
	}

	// Move from live to deleted statistics
	m.fileCount--
	m.contentSize -= uint64(old.size)
	m.deletedCount++
	m.deletedSize += uint64(old.size)

	// Set tombstone in map
	m.m[key] = &needleValue{offset: old.offset, size: types.TombstoneFileSize}

	// Append tombstone to .idx file
	if m.file != nil {
		return m.appendEntry(key, old.offset, types.TombstoneFileSize)
	}
	return nil
}

// appendEntry writes a single 16-byte index record to the .idx file.
// Must be called with m.mu held.
func (m *MemDb) appendEntry(key types.NeedleId, offset types.Offset, size types.Size) error {
	var buf [types.NeedleIndexSize]byte
	binary.BigEndian.PutUint64(buf[0:8], uint64(key))
	binary.BigEndian.PutUint32(buf[8:12], uint32(offset))
	binary.BigEndian.PutUint32(buf[12:16], uint32(size))
	_, err := m.file.Write(buf[:])
	return err
}

// Close closes the .idx file if open.
func (m *MemDb) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.file != nil {
		err := m.file.Close()
		m.file = nil
		return err
	}
	return nil
}

// Load replays all 16-byte entries from an .idx file into the in-memory map.
// The last entry for any NeedleId wins (append-only semantics).
func (m *MemDb) Load(filename string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	f, err := os.Open(filename)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("open index file: %w", err)
	}
	defer f.Close()

	var buf [types.NeedleIndexSize]byte
	for {
		_, err := io.ReadFull(f, buf[:])
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			break
		}
		if err != nil {
			return fmt.Errorf("reading index: %w", err)
		}

		key := types.NeedleId(binary.BigEndian.Uint64(buf[0:8]))
		offset := types.Offset(binary.BigEndian.Uint32(buf[8:12]))
		size := types.Size(binary.BigEndian.Uint32(buf[12:16]))

		// Update statistics for existing entry
		if old, exists := m.m[key]; exists {
			if old.isDeleted() {
				m.deletedCount--
				m.deletedSize -= uint64(old.size)
			} else {
				m.fileCount--
				m.contentSize -= uint64(old.size)
			}
		}

		m.m[key] = &needleValue{offset: offset, size: size}

		if size == types.TombstoneFileSize {
			m.deletedCount++
		} else {
			m.fileCount++
			m.contentSize += uint64(size)
		}
	}
	return nil
}

// OpenIndexFile opens the .idx file for appending. Call after Load.
func (m *MemDb) OpenIndexFile(filename string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	f, err := os.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("open index for append: %w", err)
	}
	m.file = f
	return nil
}

// ContentSize returns total bytes of live needle data.
func (m *MemDb) ContentSize() uint64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.contentSize
}

// DeletedSize returns total bytes of deleted needle data.
func (m *MemDb) DeletedSize() uint64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.deletedSize
}

// FileCount returns number of live needles.
func (m *MemDb) FileCount() int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.fileCount
}

// DeletedCount returns number of deleted needles.
func (m *MemDb) DeletedCount() int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.deletedCount
}

// LoadNeedleMap loads a needle map from an .idx file.
func LoadNeedleMap(filename string) (NeedleMap, error) {
	db := NewMemDb()
	if err := db.Load(filename); err != nil {
		return nil, err
	}
	if err := db.OpenIndexFile(filename); err != nil {
		return nil, err
	}
	return db, nil
}

// CompactIndex writes only live (non-deleted) entries from inputFile to outputFile.
func CompactIndex(inputFile, outputFile string) error {
	src := NewMemDb()
	if err := src.Load(inputFile); err != nil {
		return fmt.Errorf("load input index: %w", err)
	}

	dst, err := os.Create(outputFile)
	if err != nil {
		return fmt.Errorf("create output index: %w", err)
	}
	defer dst.Close()

	src.mu.RLock()
	defer src.mu.RUnlock()
	for id, v := range src.m {
		if v.isDeleted() {
			continue
		}
		var buf [types.NeedleIndexSize]byte
		binary.BigEndian.PutUint64(buf[0:8], uint64(id))
		binary.BigEndian.PutUint32(buf[8:12], uint32(v.offset))
		binary.BigEndian.PutUint32(buf[12:16], uint32(v.size))
		if _, err := dst.Write(buf[:]); err != nil {
			return fmt.Errorf("write output index: %w", err)
		}
	}
	return dst.Sync()
}

// iterateIndex calls fn for every NeedleId/Offset/Size in the in-memory map.
// Used by compaction.
func (m *MemDb) iterateIndex(fn func(key types.NeedleId, offset types.Offset, size types.Size)) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for k, v := range m.m {
		fn(k, v.offset, v.size)
	}
}

// CreateVolumeIndex rebuilds an .idx file by scanning a .dat file.
func CreateVolumeIndex(dataFile, indexFile string) error {
	f, err := os.Open(dataFile)
	if err != nil {
		return fmt.Errorf("open data file: %w", err)
	}
	defer f.Close()

	idx, err := os.Create(indexFile)
	if err != nil {
		return fmt.Errorf("create index file: %w", err)
	}
	defer idx.Close()

	// Skip the superblock
	offset := int64(SuperBlockSize)
	for {
		header := make([]byte, 16)
		_, err := f.ReadAt(header, offset)
		if err != nil {
			break
		}

		needleId := binary.BigEndian.Uint64(header[4:12])
		bodySize := binary.BigEndian.Uint32(header[12:16])

		unpaddedSize := int64(16 + bodySize)
		rem := unpaddedSize % 8
		if rem != 0 {
			unpaddedSize += int64(8 - rem)
		}
		totalSize := unpaddedSize

		encodedOffset := types.ToEncoded(offset)

		var buf [types.NeedleIndexSize]byte
		binary.BigEndian.PutUint64(buf[0:8], needleId)
		binary.BigEndian.PutUint32(buf[8:12], uint32(encodedOffset))
		binary.BigEndian.PutUint32(buf[12:16], uint32(totalSize))
		if _, err := idx.Write(buf[:]); err != nil {
			return err
		}

		offset += totalSize
	}
	return idx.Sync()
}

// RebuildIndex rebuilds the index file from the data file.
func RebuildIndex(dataFile, indexFile string) error {
	os.Remove(indexFile)
	dir := filepath.Dir(indexFile)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	return CreateVolumeIndex(dataFile, indexFile)
}
