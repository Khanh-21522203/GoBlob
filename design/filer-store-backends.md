# Filer Store Backends

### Purpose

Provide pluggable persistence for filer metadata and KV state so deployments can choose embedded or external databases without changing filer API behavior.

### Scope

**In scope:**
- Store selection and backend loading in `goblob/filer/storeloader/store_loader.go`.
- Shared `filer.FilerStore` contract in `goblob/filer/filer_store.go`.
- Backend implementations:
  - LevelDB: `goblob/filer/leveldb2/leveldb2_store.go`
  - Redis: `goblob/filer/redis3/redis_store.go`
  - Postgres: `goblob/filer/postgres2/postgres_store.go`
  - MySQL: `goblob/filer/mysql2/mysql_store.go`
  - Cassandra: `goblob/filer/cassandra/cassandra_store.go`

**Out of scope:**
- Filer service API handlers consuming the store.
- Migration tooling between backends.

### Primary User Flow

1. Filer runtime builds config map (backend name + backend-specific keys).
2. `LoadFilerStoreFromConfig` instantiates selected backend.
3. Backend initializes connection/files and ensures required schema.
4. Filer operations call generic store methods (`InsertEntry`, `FindEntry`, `ListDirectoryEntries`, `KvGet`, etc.).
5. Backend serializes/deserializes `filer.Entry` payloads and persists state.

### System Flow

1. Backend selection:
   - `backend` config key chooses implementation (`leveldb2|redis3|postgres2|mysql2|cassandra`).
2. Common write path:
   - `InsertEntry`/`UpdateEntry` marshal `filer.Entry` as JSON bytes and upsert by `(dir,name)` identity.
3. Common read path:
   - `FindEntry` loads serialized JSON by `(dir,name)` and unmarshals to `filer.Entry`.
4. Directory listing path:
   - backends implement ordered pagination using `startFileName`, `includeStart`, `limit`, and optional name prefix.
5. KV path:
   - `KvPut/KvGet/KvDelete` support IAM, quotas, locks, and miscellaneous metadata.

### Data Model

- Shared logical entity: `filer.Entry` JSON blob, keyed by directory + base name.
- LevelDB backend:
  - metadata key: `"<dir>\x00<name>"` -> JSON entry bytes.
  - KV key namespace: prefix byte `0xff` + raw key bytes.
- Redis backend:
  - hash key per directory: `<namespace>:meta:<dir>` mapping `name -> JSON entry`.
  - directory index set: `<namespace>:dirs`.
  - KV key: `<namespace>:kv:<hex(key)>`.
- Postgres backend schema:
  - `filer_meta(dir_hash BIGINT, dir TEXT, name TEXT, meta BYTEA, PRIMARY KEY(dir_hash,dir,name))`.
  - `filer_kv(k TEXT PRIMARY KEY, v BYTEA)`.
- MySQL backend schema:
  - `filer_meta(dir_hash BIGINT, dir VARCHAR(1024), name VARCHAR(255), meta LONGBLOB, PRIMARY KEY(...))`.
  - `filer_kv(k VARCHAR(1024) PRIMARY KEY, v LONGBLOB)`.
- Cassandra backend schema:
  - `filer_meta(dir text, name text, meta blob, PRIMARY KEY (dir,name))`.
  - `filer_kv(k text PRIMARY KEY, v blob)`.
  - `filer_dirs(dir text PRIMARY KEY)` for folder-prefix deletes.

### Interfaces and Contracts

- `FilerStore` methods must support:
  - CRUD: `InsertEntry`, `UpdateEntry`, `FindEntry`, `DeleteEntry`, `DeleteFolderChildren`.
  - listing with pagination + prefix filter.
  - transaction hooks (`BeginTransaction`, `CommitTransaction`, `RollbackTransaction`) where supported.
  - raw KV APIs.
- Store loader contract:
  - unknown backend value returns `unsupported filer backend` error.
  - missing backend defaults to `leveldb2`.

### Dependencies

**Internal modules:**
- `goblob/filer` for entry model and interface.
- `goblob/server/runtime` calls store loader and injects resulting store into filer server.

**External services/libraries:**
- LevelDB (`goleveldb`) for embedded mode.
- Redis (`go-redis/v9`), PostgreSQL (`lib/pq`), MySQL (`go-sql-driver/mysql`), Cassandra (`gocql`) for remote modes.

### Failure Modes and Edge Cases

- Backend connection/ping/init failures fail filer startup (`init filer store` error path).
- JSON unmarshal errors during reads/listing propagate as backend errors.
- Non-transactional backends (LevelDB/Redis/Cassandra) treat transaction methods as no-op; callers must not assume atomic multi-step semantics.
- Key length constraints:
  - MySQL KV key hex capped by `maxKVKeyHexLen=1024`.
- Cassandra list ordering requires in-memory sort after query results.

### Observability and Debugging

- No backend-specific metrics emitted directly.
- Debug entry points:
  - `store_loader.go:LoadFilerStoreFromConfig` for backend selection issues.
  - backend `Initialize` functions for DSN/address/auth/schema errors.
  - backend `listEntries` for pagination and prefix filtering behavior.

### Risks and Notes

- All backends store full `filer.Entry` as JSON blob; schema-level querying on entry fields is limited.
- Directory delete semantics vary in implementation detail (prefix scans, set/index maintenance) and can have different performance profiles.
- Configuration key names are backend-specific and mostly stringly typed; typoed keys can silently fall back to defaults.

Changes:

