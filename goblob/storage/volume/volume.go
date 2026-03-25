package volume

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"GoBlob/goblob/core/types"
	"GoBlob/goblob/storage/needle"
)

const (
	VolumeFileExtension = ".dat"
	IndexFileExtension  = ".idx"
)

// Volume manages one .dat + .idx file pair.
// All needle reads, writes, and deletes go through Volume methods.
type Volume struct {
	id         types.VolumeId
	dir        string
	collection string
	dataFile   *os.File
	nm         NeedleMap
	SuperBlock SuperBlock
	ReadOnly   bool

	noWriteOrDelete    bool
	dataFileAccessLock sync.RWMutex
}

// NewVolume opens or creates a volume in the given directory.
// The .dat filename is "{id}.dat", the .idx filename is "{id}.idx".
func NewVolume(dir string, collection string, id types.VolumeId, version types.NeedleVersion) (*Volume, error) {
	v := &Volume{
		id:         id,
		dir:        dir,
		collection: collection,
	}

	datPath := v.DataFileName()
	idxPath := v.IndexFileName()

	isNew := false
	if _, err := os.Stat(datPath); os.IsNotExist(err) {
		isNew = true
	}

	dataFile, err := os.OpenFile(datPath, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return nil, fmt.Errorf("open data file %s: %w", datPath, err)
	}

	// Ensure dataFile is closed on any error path.
	ok := false
	defer func() {
		if !ok {
			_ = dataFile.Close()
		}
	}()

	if isNew {
		v.SuperBlock = SuperBlock{Version: version}
		if err := WriteSuperBlock(dataFile, v.SuperBlock); err != nil {
			return nil, fmt.Errorf("write superblock: %w", err)
		}
	} else {
		sb, err := ReadSuperBlock(dataFile)
		if err != nil {
			return nil, fmt.Errorf("read superblock: %w", err)
		}
		v.SuperBlock = sb
	}

	db := NewMemDb()
	if err := db.Load(idxPath); err != nil {
		return nil, fmt.Errorf("load index: %w", err)
	}
	if err := db.OpenIndexFile(idxPath); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("open index file: %w", err)
	}

	v.dataFile = dataFile
	v.nm = db
	ok = true
	return v, nil
}

// DataFileName returns the path to the .dat volume file.
func (v *Volume) DataFileName() string {
	return filepath.Join(v.dir, fmt.Sprintf("%d", v.id)+VolumeFileExtension)
}

// IndexFileName returns the path to the .idx index file.
func (v *Volume) IndexFileName() string {
	return filepath.Join(v.dir, fmt.Sprintf("%d", v.id)+IndexFileExtension)
}

// Close closes the data file and needle index.
func (v *Volume) Close() error {
	v.dataFileAccessLock.Lock()
	defer v.dataFileAccessLock.Unlock()

	var firstErr error
	if v.nm != nil {
		if err := v.nm.Close(); err != nil {
			firstErr = err
		}
		v.nm = nil
	}
	if v.dataFile != nil {
		if err := v.dataFile.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("close data file: %w", err)
		}
		v.dataFile = nil
	}
	return firstErr
}

// Destroy closes and deletes all volume files.
func (v *Volume) Destroy() error {
	v.dataFileAccessLock.Lock()
	defer v.dataFileAccessLock.Unlock()

	if v.nm != nil {
		_ = v.nm.Close()
		v.nm = nil
	}
	if v.dataFile != nil {
		_ = v.dataFile.Close()
		v.dataFile = nil
	}
	if err := os.Remove(v.DataFileName()); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove data file: %w", err)
	}
	if err := os.Remove(v.IndexFileName()); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove index file: %w", err)
	}
	return nil
}

// WriteNeedle appends a needle to the .dat file and records its location in the index.
// Returns the encoded offset (actualOffset / 8), written size, and error.
func (v *Volume) WriteNeedle(n *needle.Needle) (offset types.Offset, size uint32, err error) {
	v.dataFileAccessLock.Lock()
	defer v.dataFileAccessLock.Unlock()

	if v.ReadOnly {
		return 0, 0, fmt.Errorf("volume %d is read-only", v.id)
	}
	if v.noWriteOrDelete {
		return 0, 0, fmt.Errorf("volume %d is frozen for compaction", v.id)
	}

	// Get current end-of-file offset
	currentOffset, err := v.dataFile.Seek(0, 2)
	if err != nil {
		return 0, 0, fmt.Errorf("seek end: %w", err)
	}

	// Align to 8-byte boundary if needed
	if rem := currentOffset % int64(types.NeedleAlignmentSize); rem != 0 {
		pad := int64(types.NeedleAlignmentSize) - rem
		padding := make([]byte, pad)
		if _, err := v.dataFile.Write(padding); err != nil {
			return 0, 0, fmt.Errorf("write alignment padding: %w", err)
		}
		currentOffset += pad
	}

	encodedOffset := types.ToEncoded(currentOffset)

	written, err := n.WriteTo(v.dataFile, v.SuperBlock.Version)
	if err != nil {
		return 0, 0, fmt.Errorf("write needle: %w", err)
	}

	if err := v.nm.Put(n.Id, encodedOffset, types.Size(written)); err != nil {
		return 0, 0, fmt.Errorf("update index: %w", err)
	}

	return encodedOffset, uint32(written), nil
}

// GetNeedle reads a needle from the volume by NeedleId.
func (v *Volume) GetNeedle(id types.NeedleId) (*needle.Needle, error) {
	v.dataFileAccessLock.RLock()
	defer v.dataFileAccessLock.RUnlock()

	encodedOffset, size, err := v.nm.Get(id)
	if err != nil {
		return nil, err
	}

	actualOffset := encodedOffset.ToActualOffset()
	n, err := needle.ReadNeedleFromFile(v.dataFile, actualOffset, size, v.SuperBlock.Version)
	if err != nil {
		return nil, fmt.Errorf("read needle at offset %d: %w", actualOffset, err)
	}
	return n, nil
}

// ReadNeedleByFileId reads a needle and verifies its Cookie for security.
func (v *Volume) ReadNeedleByFileId(fid types.FileId) (*needle.Needle, error) {
	v.dataFileAccessLock.RLock()
	defer v.dataFileAccessLock.RUnlock()

	encodedOffset, size, err := v.nm.Get(fid.NeedleId)
	if err != nil {
		return nil, err
	}

	actualOffset := encodedOffset.ToActualOffset()
	n, err := needle.ReadNeedleFromFile(v.dataFile, actualOffset, size, v.SuperBlock.Version)
	if err != nil {
		return nil, fmt.Errorf("read needle at offset %d: %w", actualOffset, err)
	}

	// Verify cookie to prevent unauthorized access
	if n.Cookie != fid.Cookie {
		return nil, needle.ErrCookieMismatch
	}
	return n, nil
}

// DeleteNeedle marks a needle as deleted by writing a tombstone and updating the index.
func (v *Volume) DeleteNeedle(id types.NeedleId) error {
	v.dataFileAccessLock.Lock()
	defer v.dataFileAccessLock.Unlock()

	if v.ReadOnly {
		return fmt.Errorf("volume %d is read-only", v.id)
	}
	if v.noWriteOrDelete {
		return fmt.Errorf("volume %d is frozen for compaction", v.id)
	}

	// Check needle exists
	if _, _, err := v.nm.Get(id); err != nil {
		return err
	}

	// Write tombstone needle to .dat
	tombstone := &needle.Needle{Id: id, DataSize: 0}
	if _, _, err := v.writeNeedleUnlocked(tombstone); err != nil {
		return fmt.Errorf("write tombstone: %w", err)
	}

	// Mark as deleted in index
	return v.nm.Delete(id)
}

// writeNeedleUnlocked writes a needle without acquiring the lock (caller must hold it).
func (v *Volume) writeNeedleUnlocked(n *needle.Needle) (types.Offset, uint32, error) {
	currentOffset, err := v.dataFile.Seek(0, 2)
	if err != nil {
		return 0, 0, err
	}
	if rem := currentOffset % int64(types.NeedleAlignmentSize); rem != 0 {
		pad := int64(types.NeedleAlignmentSize) - rem
		if _, err := v.dataFile.Write(make([]byte, pad)); err != nil {
			return 0, 0, err
		}
		currentOffset += pad
	}
	encodedOffset := types.ToEncoded(currentOffset)
	written, err := n.WriteTo(v.dataFile, v.SuperBlock.Version)
	if err != nil {
		return 0, 0, err
	}
	return encodedOffset, uint32(written), nil
}

// GarbageLevel returns the ratio of deleted bytes to total bytes tracked by the index.
func (v *Volume) GarbageLevel() float64 {
	total := v.nm.ContentSize() + v.nm.DeletedSize()
	if total == 0 {
		return 0
	}
	return float64(v.nm.DeletedSize()) / float64(total)
}

// NeedCompact returns true if garbage exceeds the threshold.
func (v *Volume) NeedCompact(threshold float64) bool {
	return v.GarbageLevel() >= threshold
}

// UsedSize returns the file size in bytes.
func (v *Volume) UsedSize() int64 {
	v.dataFileAccessLock.RLock()
	defer v.dataFileAccessLock.RUnlock()
	stat, err := v.dataFile.Stat()
	if err != nil {
		return 0
	}
	return stat.Size()
}

// NeedleCount returns the number of live needles.
func (v *Volume) NeedleCount() int64 { return v.nm.FileCount() }

// DeletedCount returns the number of deleted needles.
func (v *Volume) DeletedCount() int64 { return v.nm.DeletedCount() }

// ContentSize returns total bytes of live needle data.
func (v *Volume) ContentSize() uint64 { return v.nm.ContentSize() }

// DeletedSize returns total bytes of deleted needle data.
func (v *Volume) DeletedSize() uint64 { return v.nm.DeletedSize() }

// Sync flushes the volume data to disk.
func (v *Volume) Sync() error {
	v.dataFileAccessLock.Lock()
	defer v.dataFileAccessLock.Unlock()
	if v.dataFile != nil {
		return v.dataFile.Sync()
	}
	return nil
}

// IsEmpty returns true if the volume has no needle data (only superblock).
func (v *Volume) IsEmpty() bool {
	return v.UsedSize() <= int64(SuperBlockSize)
}

// ID returns the volume ID.
func (v *Volume) ID() types.VolumeId { return v.id }

// Version returns the needle version from the superblock.
func (v *Volume) Version() types.NeedleVersion { return v.SuperBlock.Version }

// Collection returns the collection name.
func (v *Volume) Collection() string { return v.collection }

// NeedleIndexEntry is one live needle position in a volume index snapshot.
type NeedleIndexEntry struct {
	NeedleId types.NeedleId
	Offset   types.Offset
	Size     types.Size
}

// SnapshotLiveNeedleEntries returns live needle entries sorted by physical offset.
func (v *Volume) SnapshotLiveNeedleEntries() []NeedleIndexEntry {
	if v.nm == nil {
		return nil
	}

	entries := make([]NeedleIndexEntry, 0, v.nm.FileCount())
	_ = v.nm.Iterate(func(key types.NeedleId, offset types.Offset, size types.Size) {
		if size == types.TombstoneFileSize {
			return
		}
		entries = append(entries, NeedleIndexEntry{
			NeedleId: key,
			Offset:   offset,
			Size:     size,
		})
	})
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Offset.ToActualOffset() < entries[j].Offset.ToActualOffset()
	})
	return entries
}
