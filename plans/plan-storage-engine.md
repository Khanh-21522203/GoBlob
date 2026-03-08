# Feature: Storage Engine

## 1. Purpose

The Storage Engine is the core data persistence layer of the Volume Server. It manages the three-level hierarchy: **Store** (one per volume server process) → **DiskLocation** (one per configured data directory) → **Volume** (one per `.dat`/`.idx` file pair).

It provides the complete lifecycle for volumes: creation, loading from disk at startup, needle read/write, compaction, and deletion. All blob I/O passes through this subsystem.

## 2. Responsibilities

- **Store**: Manage all `DiskLocation` instances; dispatch reads/writes to the correct volume
- **DiskLocation**: Scan a directory for existing volumes at startup; track volume maps; monitor disk free space
- **Volume**: Open/close `.dat` and `.idx` files; append needles; read needles by ID; mark needles deleted; manage read/write locking
- **NeedleMap (index)**: Maintain the NeedleId → (Offset, Size) mapping in memory or via LevelDB
- **Compaction**: Copy live needles from a volume to a new file, then swap atomically
- **State persistence**: Save/load a protobuf `VolumeServerState` file across restarts
- **Disk space monitoring**: Check free space every minute; mark locations read-only when low
- **Volume channels**: Notify the volume server of newly created/deleted volumes via Go channels

## 3. Non-Responsibilities

- Does not perform replication (replication wraps the write call externally)
- Does not assign NeedleIds (delegated to Sequencer via master)
- Does not handle HTTP or gRPC directly
- Does not manage heartbeats to master

## 4. Architecture Design

```
+----------------------------------------------------------+
|                   VolumeServer process                    |
+----------------------------------------------------------+
|                      Store                               |
|                        |                                 |
|       +----------------+----------------+                |
|       |                |                |                |
|  DiskLocation[0]  DiskLocation[1]  DiskLocation[N]       |
|  /data/ssd        /data/hdd        /mnt/external         |
|       |                |                |                |
|  +----+----+      +----+----+      +----+----+           |
|  |vol map  |      |vol map  |      |vol map  |           |
|  |[id]*Vol |      |[id]*Vol |      |[id]*Vol |           |
|  +---------+      +---------+      +---------+           |
|       |                                                  |
|  +----+----+                                             |
|  | Volume  |                                             |
|  |  .dat   | (append-only data file)                    |
|  |  .idx   | (needle index file or LevelDB)             |
|  |  NeedleMapper                                        |
|  |  sync.RWMutex (dataFileAccessLock)                   |
|  +---------+                                             |
+----------------------------------------------------------+
```

### Volume State Machine
```
         NewVolume()
              |
              v
         [Writable]
           /     \
     full/size  noWrite
      limit     flag
         |         |
    [ReadOnly] [ReadOnlyNoWrite]
         |
    Vacuum()
         |
    [Compacting] -- failure --> [ReadOnly]
         |
      success
         |
    [ReadOnly/Writable] (new compacted file)
```

## 5. Core Data Structures (Go)

```go
package storage

import (
    "sync"
    "os"
    "goblob/core/types"
    "goblob/storage/needle"
)

// NeedleMapKind selects the implementation used for the needle index.
type NeedleMapKind int

const (
    NeedleMapInMemory     NeedleMapKind = iota // fast, high RAM usage
    NeedleMapLevelDB                           // low RAM, LevelDB-backed
    NeedleMapLevelDBMedium                     // medium write batch size
    NeedleMapLevelDBLarge                      // large write batch size
)

// Store is the top-level storage manager for a single Volume Server.
// It owns all DiskLocations and dispatches I/O to the correct Volume.
type Store struct {
    Ip         string
    Port       int
    GrpcPort   int
    PublicUrl  string

    Locations  []*DiskLocation // one per configured data directory

    volumeSizeLimit uint64 // set by master; volumes over this become read-only
    NeedleMapKind   NeedleMapKind

    // State is a protobuf file persisted to disk to survive restarts.
    State *VolumeServerState

    // Channels for incremental heartbeat deltas sent to the volume server.
    NewVolumesChan      chan types.VolumeId
    DeletedVolumesChan  chan types.VolumeId

    logger *slog.Logger
}

// DiskLocation represents one mounted data directory on the volume server.
type DiskLocation struct {
    Directory     string          // absolute path of the data directory
    IdxDirectory  string          // separate index directory (may equal Directory)
    DiskType      types.DiskType
    Tags          []string
    MaxVolumeCount int32
    MinFreeSpace  MinFreeSpace    // policy for disk-full protection

    volumes    map[types.VolumeId]*Volume  // guarded by volumesLock
    volumesLock sync.RWMutex

    isDiskSpaceLow bool // set true by CheckDiskSpace goroutine

    // dirUuid is a persistent UUID for this directory, stored in .uuid file.
    dirUuid string

    stopCheckDiskSpace chan struct{}

    logger *slog.Logger
}

// MinFreeSpace encodes the threshold below which a disk is considered full.
// Can be expressed as a byte count or a percentage.
type MinFreeSpace struct {
    Percent  float32
    Bytes    uint64
}

// Volume represents a single logical volume: a .dat file + .idx index.
type Volume struct {
    Id         types.VolumeId
    location   *DiskLocation // back-reference, for path construction
    Collection string
    diskId     uint32        // disk position index within DiskLocation

    // SuperBlock is the 8-byte header read/written at the start of .dat.
    SuperBlock needle.SuperBlock

    // dataBackend is the file handle for the .dat file.
    dataBackend BackendFile

    // nm is the needle index (memory or LevelDB).
    nm NeedleMapper

    // dataFileAccessLock guards concurrent access to dataBackend.
    // Read lock for reads; write lock for appends and compaction.
    dataFileAccessLock sync.RWMutex

    // noWriteOrDelete: set when volume is read-only (full, manual freeze, etc.)
    noWriteOrDelete bool
    // noWriteCanDelete: set to allow deletes but not writes.
    noWriteCanDelete bool

    // asyncRequestsChan buffers async write requests to this volume.
    asyncRequestsChan chan *AsyncRequest

    // lastModifiedTsSeconds: last write timestamp for idle detection.
    lastModifiedTsSeconds uint64

    // volumeInfo: protobuf metadata for this volume (.vif file).
    volumeInfo *VolumeInfo

    logger *slog.Logger
}

// NeedleMapper is the interface for the needle index (offset lookup by ID).
type NeedleMapper interface {
    // Put stores or updates the offset/size for needleId.
    Put(needleId types.NeedleId, offset types.Offset, size types.Size) error
    // Get returns the NeedleValue for needleId. Returns ErrNotFound if absent.
    Get(needleId types.NeedleId) (types.NeedleValue, error)
    // Delete marks needleId as deleted (tombstone) in the index.
    Delete(needleId types.NeedleId, offset types.Offset) error
    // Close releases any resources (e.g., LevelDB handles).
    Close()
    // IndexFileSize returns the current size of the .idx file in bytes.
    IndexFileSize() uint64
    // ContentSize returns the total non-deleted data size tracked by this index.
    ContentSize() uint64
    // DeletedSize returns the total deleted data size still on disk.
    DeletedSize() uint64
    // FileCount returns the number of live needles.
    FileCount() int
    // DeletedCount returns the number of deleted (tombstoned) needles.
    DeletedCount() int
    // MaxFileKey returns the highest NeedleId seen (for sequencer recovery).
    MaxFileKey() types.NeedleId
    // IndexFileContent returns the raw bytes of the .idx file (for replication sync).
    IndexFileContent() ([]byte, error)
}

// AsyncRequest is a pending write or delete queued on a volume's async channel.
type AsyncRequest struct {
    OpType    AsyncOpType
    Needle    *needle.Needle
    WriteSize uint32
    ActualSize int64
    // result channel for the caller to await the outcome
    Result chan AsyncResult
}

type AsyncOpType int
const (
    AsyncOpWrite  AsyncOpType = iota
    AsyncOpDelete
)

type AsyncResult struct {
    Offset types.Offset
    Size   types.Size
    Err    error
}

// VolumeServerState is persisted to disk and loaded at startup.
// It allows the volume server to remember which volumes it knows about
// across restarts without rescanning all .dat files.
type VolumeServerState struct {
    // IdxToVolumeId maps an index position to a volume ID, used for stable disk IDs.
    IdxToVolumeId map[uint32]types.VolumeId
}

// VolumeInfo is protobuf metadata stored in a .vif sidecar file per volume.
type VolumeInfo struct {
    Files         []*RemoteFile // for tiered storage
    Version       uint32
    Replication   string
    DiskType      string
    Collection    string
}

// WriteRequest is the input to Store.WriteVolumeNeedle.
type WriteRequest struct {
    VolumeId types.VolumeId
    Needle   *needle.Needle
    // IsReplication signals this write is a replica (skip re-replication).
    IsReplication bool
}

// WriteResult is returned by Store.WriteVolumeNeedle.
type WriteResult struct {
    Offset      types.Offset
    Size        types.Size
    IsUnchanged bool // true if identical needle already existed at this offset
}
```

## 6. Public Interfaces

```go
package storage

// Store API
func NewStore(cfg StoreConfig) (*Store, error)
func (s *Store) WriteVolumeNeedle(req WriteRequest) (WriteResult, error)
func (s *Store) ReadVolumeNeedle(vid types.VolumeId, n *needle.Needle, readOption ReadOption) (count int, err error)
func (s *Store) DeleteVolumeNeedle(vid types.VolumeId, n *needle.Needle) (types.Size, error)
func (s *Store) FindVolume(vid types.VolumeId) *Volume
func (s *Store) HasVolume(vid types.VolumeId) bool
func (s *Store) AllocateVolume(vid types.VolumeId, collection string, rp types.ReplicaPlacement, ttl types.TTL, diskType types.DiskType, preallocate int64) error
func (s *Store) DeleteVolume(vid types.VolumeId) error
func (s *Store) MarkVolumeReadonly(vid types.VolumeId) error
func (s *Store) SetVolumeSizeLimit(limit uint64)
func (s *Store) CollectHeartbeat() *VolumeHeartbeat
func (s *Store) Close()

// Volume API (used internally and by compaction/EC)
func (v *Volume) WriteNeedle(n *needle.Needle) (offset types.Offset, size types.Size, isUnchanged bool, err error)
func (v *Volume) ReadNeedle(n *needle.Needle, readOption ReadOption) (count int, err error)
func (v *Volume) DeleteNeedle(n *needle.Needle) (types.Size, error)
func (v *Volume) Compact(gcThreshold float64) error
func (v *Volume) CommitCompact() error
func (v *Volume) Close()
func (v *Volume) IsReadOnly() bool
func (v *Volume) ContentSize() uint64
func (v *Volume) DeletedSize() uint64
func (v *Volume) NeedleCount() int
func (v *Volume) DataFileSize() (int64, error)

// NeedleMapper API (implemented by memory and LevelDB backends)
// (see NeedleMapper interface above)

// ReadOption controls read behavior
type ReadOption struct {
    // AttemptMetaOnly: return metadata without data (for HEAD requests)
    AttemptMetaOnly bool
    // ReadDeleted: return data for deleted needles (for sync/replication)
    ReadDeleted bool
}
```

## 7. Internal Algorithms

### Store Initialization
```
NewStore(cfg):
  for each dir in cfg.Directories:
    loc = NewDiskLocation(dir, ...)
    go loc.loadExistingVolumes(needleMapKind, ldbTimeout)
    go loc.checkDiskSpaceLoop()
    s.Locations = append(s.Locations, loc)
  wg.Wait()   // parallel load
  s.State = loadOrCreateState(cfg.IdxDir)
  return s
```

### Volume Loading (per DiskLocation, parallel)
```
loadExistingVolumes(dir):
  for each *.dat file in dir:
    vid = parseVolumeIdFromFilename(filename)
    v = loadExistingVolume(dir, vid, needleMapKind)
    loc.volumes[vid] = v
```

### Volume Write Algorithm
```
WriteNeedle(n):
  if v.noWriteOrDelete: return ErrReadOnly
  if v.ContentSize() + n.Size > volumeSizeLimit:
    v.noWriteOrDelete = true
    return ErrVolumeFull (triggers volume growth on master)

  v.dataFileAccessLock.Lock()
  defer v.dataFileAccessLock.Unlock()

  offset = v.dataBackend.currentSize / NeedleAlignmentSize
  bytesWritten = n.WriteTo(v.dataBackend, v.SuperBlock.Version)
  v.nm.Put(n.Id, offset, n.Size)
  v.lastModifiedTsSeconds = now()
  return offset, n.Size, false, nil
```

### Volume Read Algorithm
```
ReadNeedle(n, opt):
  v.dataFileAccessLock.RLock()
  defer v.dataFileAccessLock.RUnlock()

  nv, err = v.nm.Get(n.Id)
  if err == ErrNotFound: return 0, ErrNotFound
  if nv.IsDeleted() && !opt.ReadDeleted: return 0, ErrDeleted

  data = ReadNeedleBlob(v.dataBackend, nv.Offset.ToActualOffset(), nv.Size)
  n.ReadFrom(data, nv.Offset.ToActualOffset(), nv.Size, v.SuperBlock.Version)
  n.VerifyChecksum()
  if n.Cookie != requestedCookie: return 0, ErrCookieMismatch
  if n.IsExpired(): return 0, ErrExpired
  return 1, nil
```

### Volume Delete Algorithm
```
DeleteNeedle(n):
  if v.noWriteOrDelete: return 0, ErrReadOnly

  v.dataFileAccessLock.Lock()
  defer v.dataFileAccessLock.Unlock()

  // Write a tombstone needle to .dat (for compaction awareness)
  n.Data = nil
  n.Size = 0
  offset, _, _, err = v.writeNeedle(n)

  // Update index: mark size = TombstoneFileSize
  v.nm.Delete(n.Id, offset)
  return prevSize, nil
```

### Compaction Algorithm
```
Compact(gcThreshold):
  if deletedRatio < gcThreshold: return nil (not needed)

  tmpDatPath = v.DataFilePath() + ".cpd"
  tmpIdxPath = v.DataFilePath() + ".cpx"

  v.dataFileAccessLock.RLock() // allow reads during compaction
  copy all live needles (where nm.Get(id).Size != TombstoneFileSize && !expired) to tmpDat
  rebuild tmpIdx from live needles
  v.dataFileAccessLock.RUnlock()

CommitCompact():
  v.dataFileAccessLock.Lock() // exclusive for swap
  atomic rename: tmpDat -> v.DatPath, tmpIdx -> v.IdxPath
  reload v.nm from new .idx
  increment v.SuperBlock.CompactionRevision
  rewrite SuperBlock to new .dat
  v.dataFileAccessLock.Unlock()
```

### Disk Space Check (per DiskLocation, goroutine)
```
checkDiskSpaceLoop():
  ticker = time.NewTicker(1 * time.Minute)
  for:
    select:
    case <-ticker.C:
      free, total = diskUsage(loc.Directory)
      if free < loc.MinFreeSpace.threshold(total):
        loc.isDiskSpaceLow = true
        markAllVolumesReadOnly(loc)
      else:
        loc.isDiskSpaceLow = false
    case <-loc.stopCheckDiskSpace:
      return
```

### Async Write Channel
Each Volume has `asyncRequestsChan chan *AsyncRequest` with buffer size 128. A background goroutine `volume.startWorker()` drains the channel:
```
startWorker():
  for req := range asyncRequestsChan:
    switch req.OpType:
    case AsyncOpWrite:
      offset, size, _, err = v.WriteNeedle(req.Needle)
      req.Result <- AsyncResult{offset, size, err}
    case AsyncOpDelete:
      size, err = v.DeleteNeedle(req.Needle)
      req.Result <- AsyncResult{Size: size, Err: err}
```
Callers that need synchronous behavior send to the channel and wait on `req.Result`.

## 8. Persistence Model

### File naming conventions
```
/data/dir/
  vol_<id>.dat        - needle data file
  vol_<id>.idx        - needle index (NeedleMapInMemory only; LevelDB uses a subdirectory)
  vol_<id>.vif        - volume info protobuf sidecar
  vol_<id>.ldb/       - LevelDB directory for needle index (NeedleMapLevelDB)
  vol_<id>.cpd        - temp compaction data (renamed to .dat on commit)
  vol_<id>.cpx        - temp compaction index (renamed to .idx on commit)
  state.pb            - VolumeServerState protobuf
  .uuid               - directory UUID file
```

### .dat Header
First `8 + superBlockExtraSize` bytes are the SuperBlock. All subsequent bytes are needles.

### .idx Format
Fixed 16-byte records, one per needle written (including tombstones). Needles are appended in write order, not needle ID order. The in-memory NeedleMap is built by iterating this file in sequence.

### LevelDB Index
Each LevelDB key is the 8-byte big-endian NeedleId. Value is the 8-byte `Offset(4B) + Size(4B)`. LevelDB provides O(log n) Get/Put. The `.idx` file is also maintained for recovery and replication.

## 9. Concurrency Model

| Object | Lock | Usage |
|--------|------|-------|
| `Volume.dataFileAccessLock` | `sync.RWMutex` | RLock for reads and needle lookups; Lock for writes, deletes, compaction |
| `DiskLocation.volumesLock` | `sync.RWMutex` | RLock when dispatching I/O; Lock when adding/removing volumes |
| `Volume.asyncRequestsChan` | channel (buffered 128) | Serializes async writes; callers block if full |

**Deadlock avoidance**: No nested locks. `dataFileAccessLock` and `volumesLock` are never held simultaneously. The disk space goroutine never acquires `dataFileAccessLock`.

## 10. Configuration

```go
type StoreConfig struct {
    Ip         string
    Port       int
    GrpcPort   int
    PublicUrl  string
    Directories     []DiskDirectoryConfig
    NeedleMapKind   NeedleMapKind
    VolumeSizeLimit uint64
    LevelDBTimeout  time.Duration // offload idle LevelDB after this duration
    IdxStateDir     string        // directory for state.pb
}
```

## 11. Observability

- `obs.VolumeServerNeedleWriteBytes.WithLabelValues(vid).Add(bytes)` on each write
- `obs.VolumeServerNeedleReadBytes.WithLabelValues(vid).Add(bytes)` on each read
- `obs.VolumeServerDiskFreeBytes.WithLabelValues(dir).Set(free)` updated every minute
- Compaction start/end logged at INFO with volume ID, before/after sizes
- Disk-full events logged at WARN with directory path and free bytes remaining
- Volume load errors logged at ERROR with filename and error

## 12. Testing Strategy

- **Unit tests**:
  - `TestVolumeWriteRead`: write a needle, read it back, assert data equality
  - `TestVolumeDelete`: write then delete a needle, assert read returns ErrDeleted
  - `TestVolumeReadOnlyAfterSizeLimit`: write until limit exceeded, assert next write fails
  - `TestCompaction`: write 10 needles, delete 5, compact, assert compacted volume has 5 live needles
  - `TestNeedleMapMemory`: put/get/delete on in-memory map
  - `TestNeedleMapLevelDB`: same as above with LevelDB backend
  - `TestStoreParallelLoad`: create multiple DiskLocations with temp dirs, load in parallel
  - `TestDiskSpaceCheck`: mock disk usage, assert volume marked read-only when below threshold
- **Integration tests**:
  - `TestStoreWriteReadCycle`: full write → read → delete → compact cycle with real disk
- All tests use `t.TempDir()` for isolation

## 13. Open Questions

None.
