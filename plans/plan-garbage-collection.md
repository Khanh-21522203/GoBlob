# Feature: Garbage Collection & Compaction

## 1. Purpose

When blobs are deleted in GoBlob, the needle is not immediately removed from the `.dat` file. Instead, a tombstone entry is written to the index (size = `TombstoneFileSize`). Over time, the volume accumulates dead space. Garbage Collection (also called Vacuum or Compaction) reclaims this space by rewriting the volume with only live needles.

GC must not disrupt ongoing reads. It runs in the background, throttled to limit I/O impact, and performs an atomic swap at the end so readers see a consistent view throughout.

## 2. Responsibilities

- Determine when a volume's garbage ratio crosses the `garbageThreshold` (default 30%)
- Create a new compacted `.dat` and `.idx` pair (`*.cpd` and `*.cpx` temp files)
- Copy all live (non-deleted, non-expired) needles to the compacted file
- Atomically swap the compacted files for the originals
- Reload the needle index from the new compacted file
- Throttle I/O during compaction to avoid starving normal traffic
- Coordinate with the master to pause writes to the volume being compacted
- Increment `CompactionRevision` in the volume's SuperBlock after each compaction

## 3. Non-Responsibilities

- Does not compact filer metadata stores (each backend manages its own compaction)
- Does not delete volume files entirely (that is a separate `VolumeDelete` operation)
- Does not run automatically based on a time interval; only triggered by the garbage ratio threshold

## 4. Architecture Design

```
Master (vacuum coordinator)
  |
  | 1. GET /vol/vacuum/check?garbageThreshold=0.3
  |    Checks all volumes; returns list of volume IDs exceeding threshold
  |
  | 2. For each candidate volume:
  |    GET /vol/vacuum/needle?vid=N
  |    (Freezes writes: volume.noWriteOrDelete = true)
  v
Volume Server
  |
  | 3. Compact: read live needles from .dat -> write .cpd/.cpx
  |    (reads use RLock; compaction runs under separate goroutine)
  |
  | 4. GET /vol/vacuum/commit?vid=N
  |    Swaps .cpd -> .dat, .cpx -> .idx, reloads index
  |
  | 5. Writes resume (noWriteOrDelete = false)

Throttling:
  compactionBytePerSecond limits data copy rate
  (0 = unlimited, default = 0 for admin-triggered; throttled for automated)
```

### Compaction State Machine
```
[Normal]
    |
    | garbage ratio > threshold AND master requests vacuum
    v
[VacuumPending]
    | volume.noWriteOrDelete = true (stop writes during compact)
    v
[Compacting]
    | copy live needles to .cpd/.cpx
    | RLock held only during reads; allows concurrent reads
    v
[CommitPending]
    | write lock acquired for swap
    v
[Committed]
    | .dat/.idx swapped, index reloaded
    | noWriteOrDelete = false (writes resume)
    v
[Normal]
```

## 5. Core Data Structures (Go)

```go
package storage

// VacuumCheckResult holds the result of checking one volume for GC eligibility.
type VacuumCheckResult struct {
    VolumeId     types.VolumeId
    GarbageRatio float64 // deletedBytes / totalBytes
    NeedsVacuum  bool
}

// CompactionStats records the outcome of a compaction run.
type CompactionStats struct {
    VolumeId           types.VolumeId
    OriginalSize       int64
    CompactedSize      int64
    LiveNeedleCount    int
    DeletedNeedleCount int
    Duration           time.Duration
    BytesCopied        int64
}

// VacuumOption controls the vacuum process.
type VacuumOption struct {
    // GarbageThreshold: compact when ratio >= this. Default: 0.3.
    GarbageThreshold float64
    // BytesPerSecond throttles I/O. Default: 0 (unlimited).
    BytesPerSecond int64
    // DryRun: compute stats but do not modify files.
    DryRun bool
}
```

## 6. Public Interfaces

```go
package storage

// Volume-level compaction

// CheckGarbageRatio returns the fraction of deleted/expired data in this volume.
func (v *Volume) CheckGarbageRatio() float64

// Compact creates compacted temp files (.cpd, .cpx).
// This is the read phase: runs concurrently with reads.
// Returns stats about what was compacted.
func (v *Volume) Compact(gcThreshold float64, bytesPerSecond int64) (CompactionStats, error)

// CommitCompact atomically swaps the compacted files for the originals.
// Acquires write lock; must be called after Compact() succeeds.
// Returns error if temp files do not exist.
func (v *Volume) CommitCompact() error

// RollbackCompact removes temp files without swapping.
func (v *Volume) RollbackCompact() error

// Store-level vacuum coordination

// VacuumVolume orchestrates the full vacuum cycle for a single volume:
// Check -> Compact -> Commit (or Rollback on failure).
func (s *Store) VacuumVolume(vid types.VolumeId, opt VacuumOption) (CompactionStats, error)

// VacuumCheck returns which volumes on this store exceed the garbage threshold.
func (s *Store) VacuumCheck(threshold float64) []VacuumCheckResult
```

## 7. Internal Algorithms

### CheckGarbageRatio
```
CheckGarbageRatio():
  totalSize = v.nm.ContentSize() + v.nm.DeletedSize()
  if totalSize == 0: return 0
  return float64(v.nm.DeletedSize()) / float64(totalSize)
```

### Compact (read phase)
```
Compact(gcThreshold, bytesPerSecond):
  if CheckGarbageRatio() < gcThreshold: return stats, nil // not needed

  tmpDatPath = v.dataFilePath() + ".cpd"
  tmpIdxPath = v.idxFilePath() + ".cpx"

  tmpDat = createFile(tmpDatPath)
  tmpIdx = createFile(tmpIdxPath)

  // Write new SuperBlock to tmpDat (with incremented CompactionRevision)
  newSuperBlock = v.SuperBlock
  newSuperBlock.CompactionRevision++
  tmpDat.Write(newSuperBlock.Bytes())

  // Throttler controls copy rate
  throttler = newThrottler(bytesPerSecond)

  // Iterate needle index in order of NeedleId
  v.dataFileAccessLock.RLock()
  v.nm.ForEach(func(nv types.NeedleValue) error:
    if nv.IsDeleted(): return nil  // skip tombstones

    // Read needle from original .dat
    blob, err = ReadNeedleBlob(v.dataBackend, nv.Offset.ToActualOffset(), nv.Size)
    if err: return err

    // Parse to check TTL
    n = needle.Needle{}
    n.ReadFrom(blob, nv.Offset.ToActualOffset(), nv.Size, v.SuperBlock.Version)
    if n.IsExpired(v.lastModifiedTsSeconds): return nil  // skip expired

    // Append to compacted file
    newOffset = tmpDat.currentSize / NeedleAlignmentSize
    tmpDat.Write(blob)
    tmpIdx.WriteEntry(IndexEntry{n.Id, types.ToEncoded(newOffset), nv.Size})

    stats.BytesCopied += int64(nv.Size)
    stats.LiveNeedleCount++

    throttler.Wait(int64(nv.Size))
    return nil
  )
  v.dataFileAccessLock.RUnlock()

  stats.CompactedSize = tmpDat.currentSize
  return stats, nil
```

### CommitCompact (write phase)
```
CommitCompact():
  v.dataFileAccessLock.Lock()
  defer v.dataFileAccessLock.Unlock()

  // Close current files
  v.dataBackend.Close()
  v.nm.Close()

  // Atomic rename
  os.Rename(tmpDatPath, v.dataFilePath())
  os.Rename(tmpIdxPath, v.idxFilePath())

  // Reload from new files
  v.dataBackend = openFile(v.dataFilePath())
  v.nm = newNeedleMapper(v.idxFilePath(), v.NeedleMapKind)
  v.nm.LoadFromIndex(v.idxFilePath())

  return nil
```

### I/O Throttler
```
type Throttler struct {
    bytesPerSecond int64
    lastTime       time.Time
    bytesThisSec   int64
}

Wait(bytes int64):
  if throttler.bytesPerSecond <= 0: return  // unlimited

  throttler.bytesThisSec += bytes
  if throttler.bytesThisSec >= throttler.bytesPerSecond:
    elapsed = time.Since(throttler.lastTime)
    if elapsed < time.Second:
      time.Sleep(time.Second - elapsed)
    throttler.bytesThisSec = 0
    throttler.lastTime = time.Now()
```

### Master-Coordinated Vacuum
The master's admin scripts trigger vacuum via HTTP calls to volume servers:
```
Vacuum flow from master:
  1. GET http://vs:8080/vol/vacuum/check?garbageThreshold=0.3
     <- {volumes: [{vid:5, ratio:0.42}, ...]}

  2. For each vid with ratio > threshold:
     GET http://vs:8080/vol/vacuum/needle?vid=5
     <- starts async compaction on volume server

  3. After compaction completes (poll or callback):
     GET http://vs:8080/vol/vacuum/commit?vid=5
     <- commits and resumes writes
```

Alternatively, the admin shell `volume.vacuum` command calls the master's vacuum orchestration endpoint.

## 8. Persistence Model

### Temp Files
```
/data/dir/
  vol_<id>.dat     # original data file
  vol_<id>.idx     # original index
  vol_<id>.cpd     # compaction temp: new data file (present during compaction)
  vol_<id>.cpx     # compaction temp: new index    (present during compaction)
```

On crash during compaction:
- If `.cpd`/`.cpx` exist but `.dat` still exists: crash happened before commit → discard `.cpd`/`.cpx` on next startup
- If only `.dat` exists: no crash recovery needed

### CompactionRevision
The `SuperBlock.CompactionRevision` is incremented with each successful compaction. Replicas must have the same revision; a mismatch indicates a replica missed a compaction and needs resynchronization.

## 9. Concurrency Model

- **Compact (read phase)**: Holds `dataFileAccessLock.RLock` only during needle reads. Multiple reads and the compaction can proceed concurrently. Writes are NOT blocked during the read phase.
- **CommitCompact (write phase)**: Holds `dataFileAccessLock.Lock` exclusively for the file rename and index reload. Typically takes <100ms.
- **No concurrent compactions**: The volume server's vacuum handler ensures at most one compaction per volume at a time (protected by a per-volume bool flag `isCompacting`).

## 10. Configuration

```go
type VacuumConfig struct {
    // GarbageThreshold: compact when ratio >= this. Default: 0.3.
    GarbageThreshold float64 `mapstructure:"garbage_threshold" default:"0.3"`
    // CompactionBytesPerSecond throttles copy I/O. Default: 0 (unlimited).
    CompactionBytesPerSecond int64 `mapstructure:"compaction_bytes_per_second" default:"0"`
}
```

## 11. Observability

- Compaction start logged at INFO: `"starting compaction vid=%d ratio=%.2f"`
- Compaction completion logged at INFO: `"completed compaction vid=%d original=%d compacted=%d needles=%d"`
- Compaction rollback logged at WARN with error
- Metrics:
  - `obs.VolumeCompactionBytesTotal.WithLabelValues(vid).Add(bytesCopied)`
  - `obs.VolumeCompactionDuration.WithLabelValues(vid).Observe(duration.Seconds())`
  - `obs.VolumeGarbageRatio.WithLabelValues(vid).Set(ratio)` updated on each vacuum check

## 12. Testing Strategy

- **Unit tests**:
  - `TestCompactionPreservesLiveNeedlesOnly`: write 10 needles, delete 4, compact, assert only 6 remain in new file
  - `TestCompactionSkipsExpiredNeedles`: write needle with 1-minute TTL, wait, compact, assert expired needle absent
  - `TestCompactionIncrementsRevision`: compact twice, assert CompactionRevision = 2
  - `TestConcurrentReadsDuringCompaction`: compact while issuing reads, assert no read errors or data corruption
  - `TestRollbackOnFailure`: inject disk error during commit, assert original files untouched
  - `TestGarbageRatioThreshold`: set gcThreshold=0.5, assert compaction only triggers when >50% garbage
- **Integration tests**:
  - `TestEndToEndVacuum`: write, delete, check ratio, compact, commit, read survivors from new file

## 13. Open Questions

None.
