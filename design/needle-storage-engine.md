# Needle Storage Engine

### Purpose

Persist blob data as append-only needles inside per-volume data/index files, and provide read/delete/compaction primitives used by volume servers.

### Scope

**In scope:**
- Top-level storage manager in `goblob/storage/store.go`.
- Volume file lifecycle and needle index integration in `goblob/storage/volume/volume.go`.
- Needle binary model and parsing/writing logic in `goblob/storage/needle/*.go`.
- Compaction workflow in `goblob/storage/volume/compact.go`.

**Out of scope:**
- Network-layer HTTP/gRPC handlers that call storage.
- Cluster placement/replication strategy.

### Primary User Flow

1. Volume server calls `WriteVolumeNeedle` with parsed `Needle`.
2. Storage appends needle bytes to `<vid>.dat` (8-byte aligned) and records `(needleId -> offset,size)` in `<vid>.idx`.
3. Reads resolve index entry, load needle bytes, and verify cookie/checksum.
4. Deletes write tombstone and mark index entry deleted.
5. Compaction rewrites live needles to temp files, then atomically swaps compacted files.

### System Flow

1. Store startup (`NewStore`): creates `DiskLocation` objects and loads existing volumes from configured directories.
2. Write path (`Volume.WriteNeedle`):
   - seek EOF, align to 8-byte boundary,
   - serialize needle with header/body/footer,
   - update needle map index via `nm.Put`.
3. Read path (`Volume.ReadNeedleByFileId`):
   - lookup encoded offset + size in index,
   - read needle bytes from `.dat`,
   - verify `Cookie` against file ID.
4. Delete path (`Volume.DeleteNeedle`):
   - ensure entry exists,
   - append tombstone needle (`DataSize=0`),
   - mark index key as deleted.
5. Compaction path (`Volume.Compact` + `CommitCompact`):
   - scan live index, skip deleted/expired,
   - write `.cpd`/`.cpx` temp files,
   - freeze writes briefly, rename temp files to `.dat`/`.idx`, reload superblock + index.

```
Needle write
  -> Volume.WriteNeedle
     -> append to .dat
     -> nm.Put(needleId, offset,size)

Needle read
  -> nm.Get(needleId)
  -> ReadNeedleFromFile(offset,size)
  -> cookie/checksum validation
```

### Data Model

- Disk files per volume ID:
  - `<vid>.dat`: superblock + aligned needle records.
  - `<vid>.idx`: needle map index.
- `needle.Needle` fields:
  - identity: `Cookie`, `Id`, `Size`.
  - payload: `DataSize`, `Data`.
  - flags + optionals: `Name`, `Mime`, `Pairs`, `LastModified`, `Ttl`.
  - footer: `Checksum`, `AppendAtNs` (v3).
- Store-level config (`DiskDirectoryConfig`): `Directory`, `MaxVolumeCount`, `DiskType`.

### Interfaces and Contracts

- `storage.VolumeStore` interface methods:
  - `WriteVolumeNeedle`, `ReadVolumeNeedle`, `DeleteVolumeNeedle`, `AllocateVolume`, `GetVolume`, `GetLocations`, `ReloadExistingVolumes`, `Close`.
- Volume contracts:
  - read by `FileId` validates cookie and returns `ErrCookieMismatch` when mismatch.
  - `NeedCompact(threshold)` uses deleted-bytes ratio.
  - `SnapshotLiveNeedleEntries` returns sorted live entries by physical offset.

### Dependencies

**Internal modules:**
- `goblob/core/types` for typed IDs, offsets, and needle versions.
- `goblob/storage/volume` for per-volume file operations.
- `goblob/storage/needle` for binary encoding/decoding and checksum.

**External services/libraries:**
- Local filesystem durability; no remote service dependency in this layer.

### Failure Modes and Edge Cases

- Missing volume ID returns `volume <id> not found` at store layer.
- Corrupt needle bytes or checksum mismatch causes read errors (`ErrChecksumMismatch`).
- Cookie mismatch returns `ErrCookieMismatch` to prevent unauthorized reads by guessed IDs.
- Compaction may fail mid-way; temp files are cleaned on failure paths, and swap is guarded by write freeze lock.
- Write persistence failures in index update after data append can create inconsistency risk requiring repair tooling.

### Observability and Debugging

- No native logging in storage package; callers surface errors.
- Debug entry points:
  - `volume.go:WriteNeedle` for alignment/index updates.
  - `needle_read.go:ReadFrom` for parse/checksum errors.
  - `compact.go:Compact` and `CommitCompact` for reclaim behavior.
- Runtime volume metrics are emitted by volume server layer, not storage package directly.

### Risks and Notes

- Single-process file locking assumptions: volume methods rely on in-process mutexes, not cross-process file locks.
- Current default needle map backend is in-memory map (`NeedleMapInMemory`) in volume server option path.
- Compaction skips newly written data after scan start by design and resolves via freeze-and-swap phase.

Changes:

