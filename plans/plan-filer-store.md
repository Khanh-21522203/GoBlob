# Feature: Filer Metadata Store

## 1. Purpose

The Filer Metadata Store is the pluggable persistence layer for file and directory metadata in GoBlob. It maps full file paths to `Entry` objects containing attributes (ownership, permissions, timestamps) and the list of data chunks stored on volume servers.

The store is an interface with 20+ backend implementations (LevelDB, Redis, PostgreSQL, MySQL, Cassandra, SQLite, etc.). The default embedded backend is LevelDB2, which requires no external infrastructure.

## 2. Responsibilities

- Define the `Entry` struct: a file/directory with path, attributes, and chunk list
- Define the `FilerStore` interface: CRUD operations for entries
- Provide a default `LevelDB2` implementation for zero-configuration deployments
- Provide a `VirtualFilerStore` wrapper that handles path normalization and prefix translation
- Support `BeginTransaction`, `CommitTransaction`, `RollbackTransaction` for atomic operations
- Support `KvPut`, `KvGet`, `KvDelete` for arbitrary key-value metadata (used by distributed locks, etc.)
- Provide directory listing with pagination (prefix scan)
- Define the `FileChunk` type: a single chunk of a file stored on one volume

## 3. Non-Responsibilities

- Does not store blob data (that is the Volume Server's job)
- Does not handle HTTP requests (that is the Filer Server's job)
- Does not perform access control (that is handled by the JWT/guard layer)
- Does not manage chunk lifecycle (deletion is handled by the Filer Server's background queue)

## 4. Architecture Design

```
Filer Server
    |
    | filer.Store (VirtualFilerStore)
    |     |
    |     | path normalization, prefix mapping
    |     |
    | FilerStore interface
    |     |
    +-----+------+--------+--------+------+
    |     |      |        |        |      |
  LevelDB2 Redis3 Postgres MySQL Cassandra ...
  (default)(opt) (opt)   (opt)  (opt)
```

### Entry Path Layout
```
/                             root directory
/photos/                      directory
/photos/vacation.jpg          regular file
/buckets/                     S3 buckets directory
/buckets/my-bucket/           bucket (= directory)
/buckets/my-bucket/img/a.jpg  object (= file)

Key in LevelDB/Redis: "<dirPath>\x00<filename>"
  e.g. "/photos\x00vacation.jpg"
```

### Entry-to-Chunk Mapping
```
Entry.Chunks = []*FileChunk{
  {FileId: "5,abc123", Offset: 0, Size: 1048576, ModifiedTsNs: 1700000000000000000},
  {FileId: "5,def456", Offset: 1048576, Size: 524288, ModifiedTsNs: 1700000001000000000},
}
```

Files smaller than `saveToFilerLimit` bytes are stored inline in `Entry.Content` (no chunks).

## 5. Core Data Structures (Go)

```go
package filer

import (
    "time"
    "goblob/core/types"
)

// FullPath is the absolute path to a file or directory in the filer namespace.
type FullPath string

func (fp FullPath) DirAndName() (dir FullPath, name string) {
    i := strings.LastIndex(string(fp), "/")
    return FullPath(string(fp)[:i]), string(fp)[i+1:]
}

// Entry represents a single file or directory in the filer namespace.
type Entry struct {
    // FullPath is the absolute path. Used as the primary key.
    FullPath FullPath

    // Attr holds POSIX-style file attributes.
    Attr Attr

    // Chunks is the ordered list of data chunks for a regular file.
    // Empty for directories or inline-stored files.
    Chunks []*FileChunk

    // Content holds inlined file data (for small files below saveToFilerLimit).
    Content []byte

    // Extended is a map of arbitrary extended attributes (xattr).
    Extended map[string][]byte

    // HardLinkId and HardLinkCounter support hard links.
    HardLinkId      []byte
    HardLinkCounter int32

    // Remote is set when the file's chunks live in remote (tiered) storage.
    Remote *RemoteEntry
}

// Attr holds POSIX-style metadata for an entry.
type Attr struct {
    Mtime    time.Time  // last modification time
    Crtime   time.Time  // creation time
    Mode     os.FileMode
    Uid      uint32
    Gid      uint32
    Mime     string     // MIME type for files
    Replication string  // replication policy for this entry's chunks
    Collection  string  // collection for this entry's chunks
    TtlSec   int32      // per-file TTL in seconds (0 = no expiry)
    DiskType string
    FileSize uint64     // logical file size (sum of chunk sizes)
    INode    uint64     // virtual inode number (hash of FullPath)
    // SymlinkTarget is non-empty for symbolic links.
    SymlinkTarget string
}

// IsDirectory reports whether this entry represents a directory.
func (e *Entry) IsDirectory() bool {
    return e.Attr.Mode&os.ModeDir != 0
}

// FileChunk represents one contiguous range of a file stored on a volume server.
type FileChunk struct {
    // FileId is the volume-server blob identifier (e.g., "5,abc123ef").
    FileId       string
    // Offset is the byte position of this chunk within the file.
    Offset       int64
    // Size is the number of bytes in this chunk.
    Size         int64
    // ModifiedTsNs is the Unix nanosecond timestamp when this chunk was written.
    ModifiedTsNs int64
    // ETag is the content hash.
    ETag         string
    // CipherKey is the per-chunk encryption key (if encryption is enabled).
    CipherKey    []byte
    // IsCompressed indicates the chunk data is gzip-compressed.
    IsCompressed bool
    // Fid parsed: set on first access, cached.
    fid          *types.FileId
}

// Fid returns the parsed FileId for this chunk.
func (c *FileChunk) Fid() (types.FileId, error) {
    if c.fid != nil: return *c.fid, nil
    f, err = types.ParseFileId(c.FileId)
    if err == nil: c.fid = &f
    return f, err
}

// RemoteEntry holds tiered storage metadata when chunks live in cloud storage.
type RemoteEntry struct {
    StorageName string
    RemotePath  string
    RemoteSize  int64
    RemoteETag  string
}

// FilerStore is the interface all metadata backend implementations must satisfy.
type FilerStore interface {
    // GetName returns a human-readable name for this backend (e.g., "leveldb2").
    GetName() string
    // Initialize configures and connects to the backend.
    Initialize(configuration util.Configuration, prefix string) error
    // Shutdown cleanly closes the backend.
    Shutdown()

    // Entry CRUD
    InsertEntry(ctx context.Context, entry *Entry) error
    UpdateEntry(ctx context.Context, entry *Entry) error
    FindEntry(ctx context.Context, fp FullPath) (*Entry, error)
    DeleteEntry(ctx context.Context, fp FullPath) error
    DeleteFolderChildren(ctx context.Context, fp FullPath) error

    // Directory listing
    // Returns (lastFileName, error); lastFileName is used for pagination.
    ListDirectoryEntries(
        ctx context.Context,
        dirPath FullPath,
        startFileName string,
        includeStartFile bool,
        limit int64,
        eachEntryFn func(*Entry) bool,
    ) (string, error)

    // Batch operations
    ListDirectoryPrefixedEntries(
        ctx context.Context,
        dirPath FullPath,
        startFileName string,
        includeStartFile bool,
        limit int64,
        prefix string,
        eachEntryFn func(*Entry) bool,
    ) (string, error)

    // Transactions (no-op for backends that don't support them)
    BeginTransaction(ctx context.Context) (context.Context, error)
    CommitTransaction(ctx context.Context) error
    RollbackTransaction(ctx context.Context) error

    // Key-value store (for distributed locks, etc.)
    KvPut(ctx context.Context, key []byte, value []byte) error
    KvGet(ctx context.Context, key []byte) ([]byte, error)
    KvDelete(ctx context.Context, key []byte) error
}

// VirtualFilerStore wraps a FilerStore with path normalization and optional split-store logic.
type VirtualFilerStore struct {
    store    FilerStore
    // pathTranslations maps source path prefixes to target prefixes.
    // Used when different paths are served by different backend stores.
    pathTranslations []PathTranslation
}

type PathTranslation struct {
    FromDir string
    ToDir   string
}

// LevelDB2Store is the default embedded metadata backend.
// It requires no external service and is suitable for development and small deployments.
type LevelDB2Store struct {
    dir string
    db  *leveldb.DB
    // Separate databases per storage prefix to allow sharding.
    dbs map[string]*leveldb.DB
    mu  sync.Mutex
}
```

## 6. Public Interfaces

```go
package filer

// FilerStore interface (see §5 above for full definition)

// NewLevelDB2Store creates a new LevelDB2 store in the given directory.
func NewLevelDB2Store(dir string) (*LevelDB2Store, error)

// LoadFilerStoreFromConfig reads filer.toml and returns the configured store.
// Falls back to LevelDB2 if no config file is found.
func LoadFilerStoreFromConfig(config util.Configuration) (FilerStore, error)

// Entry helpers

// TotalSize returns the sum of all chunk sizes.
func (e *Entry) TotalSize() uint64

// ChunksSize returns the total size of all FileChunks.
func (e *Entry) ChunksSize() uint64

// SetChunks replaces the chunks and updates the entry's FileSize attribute.
func (e *Entry) SetChunks(chunks []*FileChunk)
```

## 7. Internal Algorithms

### LevelDB2 Key Encoding
```
Key format: dir + "\x00" + name

Example:
  /photos/vacation.jpg
  dir  = "/photos"
  name = "vacation.jpg"
  key  = "/photos\x00vacation.jpg"

Directory listing uses LevelDB's prefix scan:
  prefix = dir + "\x00"
  iterator.Seek(prefix + startFileName)
  while iterator.ValidForPrefix(prefix):
    yield iterator.Value()
    if --limit == 0: break
```

### Entry Serialization
Entries are serialized to protobuf for storage. The protobuf schema mirrors the `Entry` struct:
- `FullPath` is encoded as the key
- `Attr`, `Chunks`, `Extended`, `HardLinkId`, `Remote` are encoded in the protobuf value
- `Content` (inline data) is stored directly in the protobuf

```
func entryToBytes(entry *Entry) ([]byte, error):
  pb = &filer_pb.Entry{
    Name: fileName(entry.FullPath),
    Attributes: attrToProto(entry.Attr),
    Chunks: chunksToProto(entry.Chunks),
    Extended: entry.Extended,
    Content: entry.Content,
    ...
  }
  return proto.Marshal(pb)

func bytesToEntry(dir FullPath, key []byte, value []byte) (*Entry, error):
  pb = &filer_pb.Entry{}
  proto.Unmarshal(value, pb)
  return protoToEntry(dir, pb), nil
```

### Directory Listing Pagination
LevelDB's `Iterator.Seek` enables efficient prefix scans without loading all entries:
```
ListDirectoryEntries(ctx, dirPath, startFileName, includeStartFile, limit, fn):
  prefix = string(dirPath) + "/"
  startKey = prefix + startFileName

  iter = db.NewIterator(leveldb.Range{Start: startKey})
  defer iter.Release()

  for iter.Next():
    if !strings.HasPrefix(string(iter.Key()), prefix): break
    entry = decodeEntry(dirPath, iter.Key(), iter.Value())
    if !includeStartFile && entry.Name == startFileName: continue
    if !fn(entry): break
    lastFileName = entry.Name
    if --limit == 0: break

  return lastFileName, nil
```

### Transaction Support
For backends that support transactions (PostgreSQL, MySQL), the context carries a transaction handle:
```
BeginTransaction(ctx):
  tx = db.BeginTx(ctx, nil)
  return context.WithValue(ctx, txKey, tx), nil

CommitTransaction(ctx):
  tx = ctx.Value(txKey).(*sql.Tx)
  return tx.Commit()

// Operations check ctx for a transaction:
InsertEntry(ctx, entry):
  if tx = ctx.Value(txKey); tx != nil:
    tx.Exec("INSERT ...", ...)
  else:
    db.Exec("INSERT ...", ...)
```

For backends that don't support transactions (LevelDB, Redis), `BeginTransaction` returns a no-op context and `CommitTransaction` returns nil.

## 8. Persistence Model

### LevelDB2 Storage Layout
```
$default_store_dir/
  filer.db/                 # LevelDB database directory
    CURRENT                 # pointer to current MANIFEST
    MANIFEST-XXXXXX         # file that enumerates all live SSTables
    XXXXXX.ldb              # SSTable data files
    LOG                     # diagnostic log

Key-Value pairs:
  "/photos\x00vacation.jpg"   -> Entry protobuf bytes
  "/\x00photos"               -> Entry protobuf bytes (directory entry)
  "\x00kv:lock:<name>"        -> distributed lock state bytes
```

### Other Backend Layouts
Each backend implements the same logical key-value mapping using its native storage primitives:
- **Redis**: `HSET <dir> <name> <value>` per entry; prefix scan via `HSCAN`
- **PostgreSQL**: Table `filer_meta(dir_hash BIGINT, dir TEXT, name TEXT, meta BYTEA, PRIMARY KEY(dir_hash, name))`
- **MySQL**: Same as PostgreSQL but MySQL-specific syntax

## 9. Concurrency Model

`FilerStore` implementations must be safe for concurrent use from multiple goroutines:
- **LevelDB2**: LevelDB's own concurrency model (single writer at a time via internal mutex; multiple concurrent readers)
- **Redis**: Stateless; each operation uses a connection from a pool
- **SQL backends**: Connection pool; transactions are per-context

The `VirtualFilerStore` wrapper adds no locking—it delegates entirely to the underlying store.

## 10. Configuration

Configured via `filer.toml`:
```toml
# Default: LevelDB2 (no config file needed)
[leveldb2]
  dir = "./filemetadir"

# Alternative: PostgreSQL
[postgres2]
  hostname = "localhost"
  port = 5432
  database = "goblob_filer"
  username = "goblob"
  password = "secret"
  connection_max_idle = 100
  connection_max_open = 100
  connection_max_lifetime_seconds = 0

# Alternative: Redis
[redis3]
  address = "localhost:6379"
  password = ""
  database = 0
```

## 11. Observability

- `obs.FilerStoreLatency.WithLabelValues(operation, backend).Observe(duration)` for every store operation
- Store initialization logged at INFO: `"filer store initialized: backend=%s"`
- ListDirectoryEntries calls > 1000 entries logged at DEBUG (potential performance issue)
- Transaction errors logged at ERROR

## 12. Testing Strategy

- **Interface compliance tests**: A shared test suite in `filerstore_test.go` runs against any `FilerStore` implementation:
  - `TestInsertFindDelete`
  - `TestListDirectoryEntries` (pagination: list, resume from cursor)
  - `TestListDirectoryPrefixedEntries`
  - `TestDeleteFolderChildren`
  - `TestKvPutGetDelete`
  - `TestTransactionCommit`
  - `TestTransactionRollback`

- Run this suite for each backend:
  - LevelDB2: always (in-process, no deps)
  - PostgreSQL: with `testcontainers-go` (skip if Docker unavailable)
  - Redis: with `testcontainers-go`

- **Unit tests for LevelDB2 key encoding**:
  - `TestKeyEncoding`: round-trip dir+name → key → dir+name
  - `TestListPaginationCursor`: list with cursor, assert correct continuation

## 13. Open Questions

None.
