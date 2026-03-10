package volume

import (
	"fmt"
	"os"

	"GoBlob/goblob/core/types"
	"GoBlob/goblob/storage/needle"
)

// CompactionResult holds statistics from a Compact() run.
type CompactionResult struct {
	OriginalSize   int64
	CompactedSize  int64
	NeedlesRead    uint64
	NeedlesWritten uint64
	NeedlesDeleted uint64
	NeedlesExpired uint64
	TempDataFile   string
	TempIndexFile  string
}

// Compact rewrites this volume into temp files (.cpd / .cpx), skipping
// deleted and expired needles. Reads are allowed during compaction.
// Writes are not blocked — new writes after compaction starts may be missed;
// CommitCompact handles the freeze-and-swap.
func (v *Volume) Compact(compactThreshold float64) (*CompactionResult, error) {
	if v.GarbageLevel() < compactThreshold {
		return nil, fmt.Errorf("garbage ratio %.2f below threshold %.2f", v.GarbageLevel(), compactThreshold)
	}

	baseName := fmt.Sprintf("%d", v.id)
	cpdPath := fmt.Sprintf("%s/%s.cpd", v.dir, baseName)
	cpxPath := fmt.Sprintf("%s/%s.cpx", v.dir, baseName)

	// Remove stale temp files from a previous crashed run
	os.Remove(cpdPath)
	os.Remove(cpxPath)

	result := &CompactionResult{
		OriginalSize:  v.UsedSize(),
		TempDataFile:  cpdPath,
		TempIndexFile: cpxPath,
	}

	// Create temp .cpd data file
	cpdFile, err := os.Create(cpdPath)
	if err != nil {
		return nil, fmt.Errorf("create temp data file: %w", err)
	}
	defer func() {
		cpdFile.Close()
		if result.NeedlesWritten == 0 && err != nil {
			os.Remove(cpdPath)
			os.Remove(cpxPath)
		}
	}()

	// Write new SuperBlock with incremented CompactionRevision
	newSB := v.SuperBlock
	newSB.CompactionRevision++
	if err = WriteSuperBlock(cpdFile, newSB); err != nil {
		return nil, fmt.Errorf("write superblock to cpd: %w", err)
	}
	// WriteSuperBlock uses WriteAt(0) which does not advance the file cursor.
	// Seek to end of superblock so subsequent Write calls start after it.
	if _, err = cpdFile.Seek(int64(SuperBlockSize), 0); err != nil {
		return nil, fmt.Errorf("seek past superblock in cpd: %w", err)
	}

	// Create temp .cpx index
	cpxDB := NewMemDb()
	cpxFile, err := os.Create(cpxPath)
	if err != nil {
		return nil, fmt.Errorf("create temp index file: %w", err)
	}
	cpxDB.file = cpxFile
	defer cpxDB.Close()

	// Stream through the index, copy live needles
	var compactErr error
	currentOffset := int64(SuperBlockSize)

	// Take a read snapshot of the index
	v.dataFileAccessLock.RLock()
	sourceVersion := v.SuperBlock.Version
	v.dataFileAccessLock.RUnlock()

	// Iterate the needle map
	nm := v.nm.(*MemDb)
	nm.iterateIndex(func(id types.NeedleId, encOffset types.Offset, size types.Size) {
		if compactErr != nil {
			return
		}
		result.NeedlesRead++

		// Skip tombstones
		if size == types.TombstoneFileSize {
			result.NeedlesDeleted++
			return
		}

		// Read needle using read lock
		v.dataFileAccessLock.RLock()
		n, readErr := needle.ReadNeedleFromFile(v.dataFile, encOffset.ToActualOffset(), size, sourceVersion)
		v.dataFileAccessLock.RUnlock()
		if readErr != nil {
			compactErr = fmt.Errorf("read needle %d: %w", id, readErr)
			return
		}

		// Skip expired needles
		if n.IsExpired(n.AppendAtNs / 1e9) {
			result.NeedlesExpired++
			return
		}

		// Write to .cpd
		written, writeErr := n.WriteTo(cpdFile, sourceVersion)
		if writeErr != nil {
			compactErr = fmt.Errorf("write needle %d to cpd: %w", id, writeErr)
			return
		}

		// Update temp index
		encNew := types.ToEncoded(currentOffset)
		if putErr := cpxDB.Put(id, encNew, types.Size(written)); putErr != nil {
			compactErr = fmt.Errorf("update temp index for %d: %w", id, putErr)
			return
		}

		currentOffset += written
		result.NeedlesWritten++
	})

	if compactErr != nil {
		return nil, compactErr
	}

	if err = cpdFile.Sync(); err != nil {
		return nil, fmt.Errorf("sync cpd: %w", err)
	}

	result.CompactedSize = currentOffset
	return result, nil
}

// CommitCompact freezes new writes, atomically swaps the compacted files,
// then reloads the index. Reads are blocked only during the brief swap.
func (v *Volume) CommitCompact(result *CompactionResult) error {
	v.dataFileAccessLock.Lock()
	defer v.dataFileAccessLock.Unlock()

	v.noWriteOrDelete = true
	defer func() { v.noWriteOrDelete = false }()

	// Close current files
	if v.nm != nil {
		v.nm.Close()
		v.nm = nil
	}
	if v.dataFile != nil {
		v.dataFile.Close()
		v.dataFile = nil
	}

	// Atomic rename: .cpd → .dat, .cpx → .idx
	datPath := v.DataFileName()
	idxPath := v.IndexFileName()

	if err := os.Rename(result.TempDataFile, datPath); err != nil {
		return fmt.Errorf("rename cpd→dat: %w", err)
	}
	if err := os.Rename(result.TempIndexFile, idxPath); err != nil {
		// Try to restore: rename .dat back to .cpd so state is consistent
		_ = os.Rename(datPath, result.TempDataFile)
		return fmt.Errorf("rename cpx→idx: %w", err)
	}

	// Re-open .dat file
	dataFile, err := os.OpenFile(datPath, os.O_RDWR, 0644)
	if err != nil {
		return fmt.Errorf("reopen dat: %w", err)
	}
	v.dataFile = dataFile

	// Reload superblock
	sb, err := ReadSuperBlock(dataFile)
	if err != nil {
		return fmt.Errorf("read superblock after compact: %w", err)
	}
	v.SuperBlock = sb

	// Reload index
	db := NewMemDb()
	if err := db.Load(idxPath); err != nil {
		return fmt.Errorf("reload index: %w", err)
	}
	if err := db.OpenIndexFile(idxPath); err != nil {
		return fmt.Errorf("open index after compact: %w", err)
	}
	v.nm = db

	return nil
}

// CompactVolume is a convenience wrapper.
func CompactVolume(v *Volume, threshold float64) (*CompactionResult, error) {
	result, err := v.Compact(threshold)
	if err != nil {
		return nil, err
	}
	if err := v.CommitCompact(result); err != nil {
		return nil, err
	}
	return result, nil
}
