# Deduplication

Phase 8 adds content-hash dedup primitives in `goblob/storage/dedup`.

## What is implemented

- SHA-256 content hashing.
- Filer-KV mappings:
  - `dedup:<sha256>` -> `fileId`
  - `dedup_ref:<fileId>` -> reference count
  - `dedup_rev:<fileId>` -> reverse hash mapping
- Lookup-hit flow with refcount increment.
- Miss-record flow with initial metadata writes.
- Refcount decrement with cleanup of hash mappings at zero.

## Notes

The package is ready for write/delete-path integration where deduplicated file IDs are assigned and reference counts are updated.
