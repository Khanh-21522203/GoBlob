# Caching

Phase 8 adds `goblob/cache` with local and distributed cache backends.

## What is implemented

- `Cache` interface (`Get`, `Put`, `Invalidate`).
- `LRUCache` in-memory implementation with strict byte-capacity eviction.
- `RedisCache` implementation with key prefix and TTL.
- Unit tests for LRU eviction/invalidation and Redis behavior (miniredis).

## Notes

This phase provides cache primitives and backends. Read-path integration is deployment-specific and can be enabled where cache semantics fit workload requirements.
