# Storage Engine

## Assumptions

- Each volume is a pair of `.dat` (data) and `.idx` (index) files, plus an 8-byte super block header.
- Volume size is bounded by `volumeSizeLimitMB` (default 30GB) set by the master.
- Disk space is checked every minute per `DiskLocation`; low-space volumes become read-only.

## Code Files / Modules Referenced

- `goblob/storage/store.go` - `Store` struct, `NewStore()`, read/write dispatch
- `goblob/storage/disk_location.go` - `DiskLocation` struct, volume loading, EC shard loading
- `goblob/storage/volume.go` - `Volume` struct, `NewVolume()`
- `goblob/storage/volume_loading.go` - Volume load from disk
- `goblob/storage/volume_read.go` - `readNeedle()`, blob retrieval
- `goblob/storage/volume_write.go` - `writeNeedle()`, `deleteNeedle()`, blob storage
- `goblob/storage/volume_vacuum.go` - Compaction (garbage collection)
- `goblob/storage/volume_checking.go` - Integrity verification
- `goblob/storage/volume_info.go` - Volume metadata (`.vif` file)
- `goblob/storage/volume_tier.go` - Tiered storage support
- `goblob/storage/needle_map.go` - `NeedleMapper` interface
- `goblob/storage/needle_map_memory.go` - In-memory needle map
- `goblob/storage/needle_map_leveldb.go` - LevelDB-backed needle map
- `goblob/storage/store_vacuum.go` - Store-level vacuum coordination
- `goblob/storage/store_ec.go` - EC shard operations on store
- `goblob/storage/store_state.go` - Persistent volume server state

## Overview

The storage engine is the core data persistence layer of GoBlob. It organizes data in a three-level hierarchy: **Store** (per volume server) -> **DiskLocation** (per disk directory) -> **Volume** (per logical volume). Each volume is an append-only log of needles (blobs), indexed by an in-memory or LevelDB-backed needle map.

## Responsibilities

- **Volume lifecycle**: Create, load, close, compact, and destroy volumes
- **Needle I/O**: Append needles to volumes, read needles by ID, delete by marking
- **Index management**: Maintain needle-to-offset mapping (memory or LevelDB)
- **Disk space monitoring**: Periodic free-space checks, auto-mark ReadOnly
- **Compaction**: Vacuum deleted needles to reclaim space
- **State persistence**: Track volume server state across restarts

## Architecture Role

```
+--------------------------------------------------------------+
|                    Volume Server Process                       |
+--------------------------------------------------------------+
|                                                               |
|                      +--------+                               |
|                      | Store  |                               |
|                      +---+----+                               |
|                          |                                    |
|          +---------------+---------------+                    |
|          |               |               |                    |
|   +------+------+ +------+------+ +------+------+            |
|   |DiskLocation | |DiskLocation | |DiskLocation |            |
|   | dir=/ssd    | | dir=/hdd    | | dir=/mnt    |            |
|   | DiskType=ssd| | DiskType=hdd| | DiskType=hdd|            |
|   +------+------+ +------+------+ +------+------+            |
|          |               |               |                    |
|    +-----+-----+  +-----+-----+   +-----+-----+             |
|    |  volumes   |  |  volumes   |  |  volumes   |            |
|    | map[vid]   |  | map[vid]   |  | map[vid]   |            |
|    |  *Volume   |  |  *Volume   |  |  *Volume   |            |
|    +-----+------+  +-----+-----+  +-----+------+            |
|          |               |               |                    |
|    +-----+------+  +-----+------+  +-----+------+           |
|    | vol_1.dat  |  | vol_5.dat  |  | vol_9.dat  |           |
|    | vol_1.idx  |  | vol_5.idx  |  | vol_9.idx  |           |
|    | vol_2.dat  |  | vol_6.dat  |  | vol_10.dat |           |
|    | vol_2.idx  |  | vol_6.idx  |  | vol_10.idx |           |
|    +------------+  +------------+  +------------+            |
+--------------------------------------------------------------+
```

## Component Structure Diagram

```
Store
  |
  +-- Locations []*DiskLocation
  |     |
  |     +-- volumes map[VolumeId]*Volume
  |     |     |
  |     |     +-- Volume
  |     |           +-- Id              needle.VolumeId
  |     |           +-- SuperBlock      (8-byte header)
  |     |           +-- DataFile        *os.File
  |     |           +-- nm              NeedleMapper (index)
  |     |           +-- Collection      string
  |     |           +-- noWriteOrDelete bool (read-only)
  |     |           +-- noWriteCanDelete bool
  |     |           +-- asyncRequestsChan chan *AsyncRequest
  |     |           +-- dataFileAccessLock sync.RWMutex
  |     |           +-- volumeInfo      *VolumeInfo (protobuf)
  |     |           +-- location        *DiskLocation (back-ref)
  |     |           +-- diskId          uint32
  |     |
  |     +-- DiskType, Tags, MaxVolumeCount
  |     +-- MinFreeSpace, isDiskSpaceLow
  |
  +-- State *State (persisted protobuf)
  +-- MasterAddress pb.ServerAddress
  +-- volumeSizeLimit uint64
  +-- NeedleMapKind (memory|leveldb|...)
  +-- Channel set for heartbeat deltas:
        NewVolumesChan, DeletedVolumesChan
        StateUpdateChan
```

## Control Flow

### Store Initialization

```
NewStore(grpcDialOption, ip, port, ..., dirnames, maxCounts, ...)
    |
    +--> for each directory (i):
    |       |
    |       +--> NewDiskLocation(dir, maxCount, minFreeSpace, idxDir, diskType, tags)
    |       |       +--> ResolvePath(dir)
    |       |       +--> GenerateDirUuid(dir)  (create/read .uuid file)
    |       |       +--> go CheckDiskSpace()   (every minute)
    |       |
    |       +--> s.Locations = append(s.Locations, location)
    |       |
    |       +--> go loadExistingVolumesWithId(needleMapKind, ldbTimeout, diskId)
    |               |
    |               +--> scan directory for .dat files
    |               +--> for each .dat file:
    |                       NewVolume(dir, ...) or loadExistingVolume()
    |                       add to volumes map
    |
    +--> wg.Wait()  (all locations loaded in parallel)
    |
    +--> NewState(idxFolder)
    |       +--> Load state.pb from disk
    |
    +--> return Store
```

### Volume Write Flow

```
Store.WriteVolumeNeedle(volumeId, needle)
    |
    +--> findVolume(volumeId)
    |       +--> iterate Locations[].volumes[volumeId]
    |
    +--> volume.writeNeedle(needle)
    |       |
    |       +--> check noWriteOrDelete / noWriteCanDelete
    |       +--> check volume size limit
    |       |
    |       +--> dataFileAccessLock.Lock()
    |       +--> append needle bytes to .dat file
    |       |    (header + data + checksum + padding)
    |       +--> nm.Put(needleId, offset, size)
    |       |    (update needle map / index)
    |       +--> update lastModifiedTsSeconds
    |       +--> dataFileAccessLock.Unlock()
    |
    +--> return (offset, size, isUnchanged, err)
```

### Volume Read Flow

```
Store.ReadVolumeNeedle(volumeId, needle, readOption)
    |
    +--> findVolume(volumeId)
    |
    +--> volume.readNeedle(needle, readOption)
    |       |
    |       +--> nm.Get(needle.Id)
    |       |    --> returns (NeedleValue: offset, size)
    |       |
    |       +--> if size == TombstoneFileSize: deleted
    |       |
    |       +--> ReadNeedleBlob(offset, size)
    |       |    (pread from .dat file at offset)
    |       |
    |       +--> needle.ReadData(blob, offset, size, version)
    |       |    (parse header, extract data, verify CRC32)
    |       |
    |       +--> check cookie match (anti-brute-force)
    |       +--> check TTL expiry
    |
    +--> return (count, err)
```

## Data Flow Diagram

```
Write Path:
                                                 
  Needle bytes                                   
  +--------+---------+------+-----+------+-----+ 
  | Cookie | NeedleId| Size | Data| CRC  | Pad | 
  | 4B     | 8B      | 4B   | var | 4B   | var | 
  +--------+---------+------+-----+------+-----+ 
       |                                         
       v                                         
  +----+----+                                    
  | .dat    |  (append-only)                     
  | file    |                                    
  +---------+                                    
                                                 
  Index entry                                    
  +---------+--------+------+                    
  | NeedleId| Offset | Size |                    
  | 8B      | 4B     | 4B   |  (16 bytes/entry) 
  +---------+--------+------+                    
       |                                         
       v                                         
  +----+----+                                    
  | .idx    |  (or NeedleMap in memory/LevelDB)  
  | file    |                                    
  +---------+                                    

Read Path:
                                                 
  NeedleId --> NeedleMap.Get(id)                 
       |                                         
       v                                         
  (offset, size) --> .dat pread(offset, size)    
       |                                         
       v                                         
  raw bytes --> Needle.ReadData()                
       |       (parse, decompress, verify CRC)   
       v                                         
  file data                                      
```

## Dependencies

| Dependency | Purpose |
|---|---|
| `goblob/storage/needle` | Needle struct, TTL, version, parsing |
| `goblob/storage/super_block` | SuperBlock (8-byte volume header) |
| `goblob/storage/types` | DiskType, NeedleId, Cookie, CRC |
| `goblob/storage/idx` | Binary `.idx` file reader/writer |
| `goblob/storage/needle_map` | Sorted-file and btree map implementations |
| `goblob/storage/backend` | Local disk file abstraction |
| `github.com/syndtr/goleveldb` | LevelDB for needle index |
| `goblob/util` | Disk space checks, path resolution |
| `goblob/obs` | Prometheus metrics |

## Error Handling

- **CRC mismatch**: Detected on read; returns error (data corruption)
- **Disk full**: `isDiskSpaceLow` flag set; volumes marked ReadOnly
- **Volume not found**: Returns `ErrorNotFound`
- **Write to read-only**: Returns error immediately
- **Compaction failure**: Logged; original volume preserved
- **LevelDB errors**: Needle map operations return errors, propagated to callers

## Async / Background Behavior

| Goroutine | Purpose |
|---|---|
| `CheckDiskSpace()` | Per-minute per-DiskLocation free space check |
| `Volume.startWorker()` | Async needle request processing via `asyncRequestsChan` (128 buffer) |
| `loadExistingVolumesWithId()` | Parallel volume loading at startup |
| Compaction workers | Triggered externally; throttled by `compactionBytePerSecond` |

## Configuration

- **NeedleMapKind**: `memory` (in-memory `.idx`), `leveldb`, `leveldbMedium`, `leveldbLarge`
- **LevelDB timeout**: Hours of idle before offloading LevelDB to save memory
- **Volume size limit**: Set by master (default 30GB)
- **Preallocate**: Optional disk preallocation for volume files

## Edge Cases

- **Tombstone deletion**: Deletes write a tombstone (size = `TombstoneFileSize`) to the index; actual data reclaimed during compaction
- **Volume compaction**: Creates a temporary `.cpd`/`.cpx` file, copies live needles, then atomically swaps
- **Concurrent reads during compaction**: `dataFileAccessLock` (RWMutex) allows concurrent reads but exclusive writes
- **Volume ID overflow**: VolumeId is `uint32`; practical limit of ~4 billion volumes per cluster
- **Super block version**: v2 and v3 support extra metadata in the super block (beyond 8 bytes)
- **Async write channel**: `asyncRequestsChan` has buffer of 128; excess writes block until processed
