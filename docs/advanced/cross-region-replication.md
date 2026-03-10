# Cross-Region Replication

Phase 8 adds async metadata replication in `goblob/replication/async`.

## What is implemented

- Configurable source/target replicator.
- Source metadata subscription via filer `SubscribeMetadata` stream.
- Chunk data copy before metadata apply:
  - source chunk read via source volume lookup + HTTP GET
  - target chunk write via assign + HTTP PUT
  - chunk `FileId` rewrite to target-assigned FID
- Target upsert/delete application with timestamp-based stale-write protection.
- Replication metrics:
  - `goblob_replication_lag_seconds`
  - `goblob_replication_replicated_entries_total`
- Runtime status snapshots and CLI reporting.

## Command

```bash
blob replication.status
blob replication.status -target region-b
```

`replication.status` prints in-process status snapshots (lag, replicated count, last error).
