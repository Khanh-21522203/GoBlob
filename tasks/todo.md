# Phase 2 Fix Plan

## Group A – Independent fixes (needle package + Phase 1)
- [ ] A1. needle/needle.go: Remove duplicate `NeedleVersion int` type; replace with `types.NeedleVersion` everywhere
- [ ] A2. needle/needle_read.go: Fix Size calculation (bodySize already includes footer — remove double-count)
- [ ] A3. needle/needle_read.go: Update signature to use `types.NeedleVersion`; add bounds check before footer slice
- [ ] A4. needle/needle_write.go: Update signature to use `types.NeedleVersion`
- [ ] A5. grace/grace.go: Fix TOCTOU race in NotifyExit (hold lock before checking notified flag)
- [ ] A6. security/jwt.go: Restrict to HS256 only via WithValidMethods
- [ ] A7. security/middleware.go: Fix gRPC peer extraction (use grpc/peer package)
- [ ] A8. go.mod: Add hashicorp/raft, syndtr/goleveldb, gorilla/mux dependencies

## Group B – Foundational storage (depends on A)
- [ ] B1. volume/superblock.go: Complete rewrite — 8-byte binary format (Version, ReplicaPlacement, TTL, CompactionRevision, ExtraSize)
- [ ] B2. volume/superblock_test.go: Rewrite tests for new 8-byte format
- [ ] B3. volume/needle_map.go: Fix index entry to 16 bytes (use uint32 for Offset, not uint64)
- [ ] B4. volume/needle_map.go: Fix Delete — set Size=0 tombstone instead of removing entry
- [ ] B5. volume/needle_map.go: Add statistics fields (contentSize, deletedSize, fileCount, deletedCount)
- [ ] B6. volume/needle_map.go: Fix Flush — append single entry instead of rewriting whole file; handle nil file
- [ ] B7. volume/needle_map.go: Fix Load — read 16-byte entries
- [ ] B8. volume/needle_map.go: Add Put append-only writes to idx file

## Group C – Volume layer (depends on B)
- [ ] C1. volume/volume.go: Change version field type to types.NeedleVersion
- [ ] C2. volume/volume.go: Change filename to {id}.dat (remove collection prefix)
- [ ] C3. volume/volume.go: NewVolume writes 8-byte SuperBlock to new .dat file; byteCount starts at 8
- [ ] C4. volume/volume.go: WriteNeedle — encode offset (ToEncoded); return types.Offset
- [ ] C5. volume/volume.go: GetNeedle — call offset.ToActualOffset() before ReadAt
- [ ] C6. volume/volume.go: Add noWriteOrDelete bool field; guard in WriteNeedle/DeleteNeedle
- [ ] C7. volume/volume.go: Add ReadNeedleByFileId with Cookie verification
- [ ] C8. volume/compact.go: Fix temp file extensions (.cpd, .cpx)
- [ ] C9. volume/compact.go: Write SuperBlock with CompactionRevision+1 to .cpd file
- [ ] C10. volume/compact.go: Stream iteration instead of loading all needles into memory
- [ ] C11. volume/compact.go: Check TTL expiry (needle.IsExpired) during compaction
- [ ] C12. volume/compact.go: Fix CommitCompact — direct atomic rename (no backup step)
- [ ] C13. volume/compact.go: Fix lock strategy — don't hold write lock during compaction
- [ ] C14. volume/disk_location.go: Add volumes map[types.VolumeId]*Volume
- [ ] C15. volume/disk_location.go: Implement LoadExistingVolumes
- [ ] C16. volume/disk_location.go: Fix filename parsing for {id}.dat format
- [ ] C17. volume/disk_location.go: Add GetVolume, fix AddVolume
- [ ] C18. storage/store.go: Call LoadExistingVolumes on startup
- [ ] C19. storage/store.go: Use FileId in ReadVolumeNeedle (for Cookie check)
- [ ] C20. storage/store.go: Add NeedleMapKind to constructor
- [ ] C21. volume/volume_test.go: Update tests for new format (offset encoding, filename, superblock)

## Verification
- [ ] go build ./... — zero errors
- [ ] go test ./goblob/... -race — all pass
