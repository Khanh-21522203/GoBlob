# Feature: Needle Format

## 1. Purpose

The Needle Format subsystem defines the binary layout used to store a single blob (file) inside a GoBlob volume `.dat` file. A needle contains the raw file data plus associated metadata (filename, MIME type, key-value pairs, timestamps) and a CRC32 checksum for integrity verification.

The needle format is the most foundational persistence concern: every write to disk and every read from disk passes through this subsystem's encoding/decoding logic.

## 2. Responsibilities

- Define the `Needle` struct with all metadata fields
- Serialize a `Needle` to bytes for appending to a `.dat` file
- Deserialize bytes back into a `Needle` for reads
- Compute and verify CRC32 checksums
- Parse needle data from an HTTP multipart upload request
- Encode and decode the volume `SuperBlock` (8-byte header)
- Encode and decode index entries (`.idx` file format, 16 bytes each)
- Enforce needle size limit (uint32 max = 4 GB)
- Support optional gzip/zstd compression of data
- Support TTL per needle
- Detect and handle tombstone (deleted) needles

## 3. Non-Responsibilities

- Does not manage files on disk (that is the Storage Engine's job)
- Does not assign NeedleIds (that is the Sequencer's job)
- Does not replicate writes (that is the Replication Engine's job)
- Does not encrypt data (encryption wraps at a higher layer)

## 4. Architecture Design

```
HTTP Upload                  Needle                    Volume .dat file
+-----------+                +---------------+          +---------------+
|multipart  |  --parse-->    | SuperBlock    |          | SuperBlock    |
|form +     |                | (8+ bytes)    |          | (8+ bytes)    |
|headers    |  CreateNeedle  +---------------+          +---------------+
+-----------+  FromRequest   | Needle 1      |          | Needle 1      |
                             |  Header(16B)  | --write->|  Header(16B)  |
                             |  Body(var)    |          |  Body(var)    |
                             |  Footer(12B+) |          |  Footer(12B+) |
                             +---------------+          +---------------+
                                                        | Needle 2 ...  |
                                                        +---------------+

.idx file (index):
+----------+--------+------+   per entry: 16 bytes
| NeedleId | Offset | Size |   NeedleId: 8B, Offset: 4B, Size: 4B
| uint64   | uint32 | uint32|
+----------+--------+------+
```

### Needle Binary Layout (Version 3)

```
HEADER (16 bytes, fixed):
  Cookie     : uint32  (4B) - random anti-brute-force value
  NeedleId   : uint64  (8B) - identity from sequencer
  BodySize   : uint32  (4B) - total bytes in body + footer

BODY (variable, present if BodySize > 0):
  DataSize   : uint32  (4B) - length of Data
  Data       : []byte  (DataSize bytes) - the actual file content
  Flags      : uint8   (1B) - bitmask of optional field presence
  [NameSize] : uint8   (1B) - present if HasName flag set
  [Name]     : []byte  - filename (up to 255 bytes)
  [MimeSize] : uint8   (1B) - present if HasMime flag set
  [Mime]     : []byte  - MIME type (up to 255 bytes)
  [PairsSize]: uint16  (2B) - present if HasPairs flag set
  [Pairs]    : []byte  - key=value pairs, JSON, up to 64KB
  [LastModified]: uint40 (5B) - Unix seconds, present if HasLastModified set
  [Ttl]      : uint16  (2B) - TTL encoding, present if HasTtl set

FOOTER (12 bytes minimum):
  Checksum   : uint32  (4B) - CRC32 IEEE over Cookie+NeedleId+Body
  AppendAtNs : uint64  (8B) - v3 only: Unix nanoseconds of append time
  Padding    : 0-7 bytes  - pads total needle to 8-byte boundary
```

### Flags Byte
```
Bit 0 (0x01): HasName
Bit 1 (0x02): HasMime
Bit 2 (0x04): HasLastModified
Bit 3 (0x08): HasTtl
Bit 4 (0x10): HasPairs
Bit 5 (0x20): IsCompressed (data is gzip or zstd)
```

### SuperBlock Layout (8 bytes)
```
Byte 0: Version (uint8) - 1, 2, or 3
Byte 1: ReplicaPlacement (uint8) - encoded as DDDRRRCC
Byte 2-3: TTL (uint16) - [Count, Unit]
Byte 4-5: CompactionRevision (uint16) - incremented per compaction
Byte 6-7: ExtraSize (uint16) - bytes of protobuf SuperBlockExtra following
[ExtraSize bytes: protobuf SuperBlockExtra - collection, version, etc.]
```

## 5. Core Data Structures (Go)

```go
package needle

import (
    "hash/crc32"
    "goblob/core/types"
)

// Needle represents a single stored blob, including its metadata.
// It is the unit of storage within a volume .dat file.
type Needle struct {
    // --- Identity ---
    Cookie   types.Cookie   // random, anti-brute-force
    Id       types.NeedleId // assigned by sequencer
    Size     types.Size     // on-disk size of this needle (header+body+footer+padding)

    // --- Data ---
    DataSize uint32 // actual data length (may differ from len(Data) if compressed)
    Data     []byte // file content (after decompression if IsCompressed)

    // --- Optional metadata ---
    Flags        uint8
    Name         []byte // original filename (max 255 bytes)
    Mime         []byte // MIME type (max 255 bytes)
    Pairs        []byte // key=value metadata (max 64KB, JSON-encoded)
    LastModified uint64 // Unix seconds (5-byte on wire)
    Ttl          types.TTL

    // --- Footer ---
    Checksum   CRC32    // computed over Cookie+Id+Body
    AppendAtNs uint64   // v3: nanoseconds since epoch when written
}

// Flag constants for the Flags byte
const (
    FlagHasName         uint8 = 0x01
    FlagHasMime         uint8 = 0x02
    FlagHasLastModified uint8 = 0x04
    FlagHasTtl          uint8 = 0x08
    FlagHasPairs        uint8 = 0x10
    FlagIsCompressed    uint8 = 0x20
)

// CRC32 is a uint32 checksum using the IEEE polynomial.
type CRC32 uint32

// NewCRC32 computes a CRC32 from a Cookie, NeedleId, and body bytes.
func NewCRC32(cookie types.Cookie, id types.NeedleId, body []byte) CRC32 {
    h := crc32.NewIEEE()
    var buf [12]byte
    binary.BigEndian.PutUint32(buf[0:4], uint32(cookie))
    binary.BigEndian.PutUint64(buf[4:12], uint64(id))
    h.Write(buf[:])
    h.Write(body)
    return CRC32(h.Sum32())
}

// ParsedUpload is the result of parsing an HTTP multipart upload.
type ParsedUpload struct {
    Data         []byte
    FileName     string
    MimeType     string
    ModifiedTime uint64     // Unix seconds
    TTL          types.TTL
    Pairs        []byte     // raw key=value metadata
    IsGzipped    bool       // data is already gzip-compressed
    IsZstd       bool
    OriginalSize int        // before compression
    ContentMD5   string
}

// SuperBlock is the 8-byte header at the beginning of every .dat file.
// It encodes volume-level metadata that applies to all needles in the volume.
type SuperBlock struct {
    Version            types.NeedleVersion
    ReplicaPlacement   types.ReplicaPlacement
    Ttl                types.TTL
    CompactionRevision uint16
    Extra              *SuperBlockExtra // protobuf, nil if ExtraSize==0
}

// SuperBlockExtra holds protobuf-encoded additional volume metadata.
type SuperBlockExtra struct {
    VolumeId   uint32
    Collection string
    // ... other fields in protobuf definition
}

// IndexEntry is a single 16-byte record in the .idx file.
type IndexEntry struct {
    NeedleId types.NeedleId // 8 bytes
    Offset   types.Offset   // 4 bytes (encoded: actual/8)
    Size     types.Size     // 4 bytes (0 = tombstone)
}

// NeedleAlignPadding computes how many bytes to add to reach the next 8-byte boundary.
func NeedleAlignPadding(n int) int {
    remainder := n % int(types.NeedleAlignmentSize)
    if remainder == 0 {
        return 0
    }
    return int(types.NeedleAlignmentSize) - remainder
}
```

## 6. Public Interfaces

```go
package needle

// CreateNeedleFromRequest parses an HTTP multipart request into a Needle.
// fileSizeLimitBytes: reject if data exceeds this; 0 = no limit.
// fixJpgOrientation: rotate JPEG if EXIF says rotated.
func CreateNeedleFromRequest(
    r *http.Request,
    fixJpgOrientation bool,
    fileSizeLimitBytes int64,
) (*Needle, int, string, error)
// Returns: (needle, originalSize, contentMD5, error)

// WriteTo serializes the Needle to w in version v binary format.
// Returns the total bytes written (header + body + footer + padding).
func (n *Needle) WriteTo(w io.Writer, v types.NeedleVersion) (int64, error)

// ReadFrom deserializes a Needle from a byte slice at the given version.
// The bytes must have been read from the .dat file at the needle's offset.
func (n *Needle) ReadFrom(data []byte, offset int64, size types.Size, v types.NeedleVersion) error

// VerifyChecksum returns an error if the stored checksum does not match the computed one.
func (n *Needle) VerifyChecksum() error

// IsExpired reports whether the needle's TTL has elapsed since writeTimeUnixSec.
func (n *Needle) IsExpired(writeTimeUnixSec uint64) bool

// SuperBlock encoding
func (sb *SuperBlock) Bytes() []byte
func ParseSuperBlock(data []byte) (SuperBlock, error)

// Index entry encoding
func (e IndexEntry) Bytes() []byte
func ParseIndexEntry(b []byte) (IndexEntry, error)

// Raw blob read: reads exactly (offset, size) from r without parsing fields.
// Used for fast data extraction during EC reconstruction.
func ReadNeedleBlob(r io.ReaderAt, offset int64, size types.Size) ([]byte, error)
```

## 7. Internal Algorithms

### Needle Serialization (WriteTo)
1. Compute body: encode DataSize(4B) + Data + Flags(1B) + optional fields in flag order
2. Compute CRC32 over `Cookie+Id+body_bytes`
3. Encode footer: Checksum(4B) + AppendAtNs(8B, v3 only)
4. Compute BodySize = len(body) + len(footer)
5. Write header: Cookie(4B) + NeedleId(8B) + BodySize(4B)
6. Write body bytes
7. Write footer bytes
8. Write padding: `NeedleAlignPadding(16 + len(body) + len(footer))` zero bytes
9. Total needle size on disk = 16 + len(body) + len(footer) + padding

### Needle Deserialization (ReadFrom)
1. Parse header: Cookie(4B), NeedleId(8B), BodySize(4B)
2. If BodySize == 0: empty needle, skip
3. Parse body:
   - DataSize(4B), Data[0:DataSize]
   - Flags(1B)
   - If HasName: NameSize(1B), Name[0:NameSize]
   - If HasMime: MimeSize(1B), Mime[0:MimeSize]
   - If HasPairs: PairsSize(2B), Pairs[0:PairsSize]
   - If HasLastModified: LastModified (5B, big-endian uint40)
   - If HasTtl: TTL[2B]
4. Parse footer: Checksum(4B); if v3: AppendAtNs(8B)
5. Verify CRC32: recompute over Cookie+NeedleId+body, compare with stored
6. If compressed flag set: decompress Data into n.Data

### CRC32 Computation
Uses `hash/crc32` with the IEEE (Castagnoli variant not used) polynomial:
```
h = crc32.NewIEEE()
h.Write(bigEndian(cookie))    // 4 bytes
h.Write(bigEndian(needleId))  // 8 bytes
h.Write(bodyBytes)            // variable
checksum = h.Sum32()
```

### HTTP Upload Parsing
1. Detect Content-Type: `multipart/form-data` or `application/octet-stream`
2. For multipart: read file field from form, extract filename and MIME
3. If `Content-Encoding: gzip`: mark as pre-compressed
4. Check total data size against fileSizeLimitBytes
5. Optionally compress with gzip if data is not already compressed and compressible MIME type
6. Extract `GoBlob-*` headers as key-value pairs
7. Extract `Ttl` header
8. Return `ParsedUpload`

### TTL Expiry Check
```
if needle.Ttl.IsNeverExpire(): return false
secondsPerUnit = map[unit]: m=60, h=3600, d=86400, w=604800, M=2592000, y=31536000
expireAtSec = writeTimeUnixSec + uint64(needle.Ttl.Count) * secondsPerUnit[unit]
return currentUnixSec > expireAtSec
```

## 8. Persistence Model

### .dat File Layout
```
[SuperBlock: 8+ bytes]
[Needle 1: variable, 8-byte aligned]
[Needle 2: variable, 8-byte aligned]
...
[Needle N: variable, 8-byte aligned]
```

The file is append-only during normal operation. Compaction creates a new `.cpd` file with only live needles, then atomically renames it.

### .idx File Layout
```
[IndexEntry 1: 16 bytes]
[IndexEntry 2: 16 bytes]
...
[IndexEntry N: 16 bytes]
```

Entry N corresponds to the Nth needle written. The index is rebuilt from `.dat` on startup if the `.idx` file is missing or corrupt.

### Offset Encoding
Stored as `uint32 = actualByteOffset / 8`. Maximum volume size = `2^32 * 8 = 32 GB`. The 8-byte alignment ensures every needle is addressable within uint32.

## 9. Concurrency Model

The Needle struct and its methods are not thread-safe by themselves. Callers (the storage engine) are responsible for synchronization:
- Multiple readers may call `ReadFrom` concurrently with different byte slices (safe, no shared state)
- `WriteTo` must not be called concurrently on the same `*Needle`

`NewCRC32` is stateless and safe for concurrent use.

## 10. Configuration

No configuration struct. The needle format is entirely governed by the `NeedleVersion` constant and the per-needle `Flags` byte. Version is set by the `SuperBlock` per volume.

```go
const (
    MaxNeedleDataSize   = 1 << 32 - 1 // 4 GB, uint32 max
    MaxNeedleNameSize   = 255
    MaxNeedleMimeSize   = 255
    MaxNeedlePairsSize  = 65535 // 64KB
)
```

## 11. Observability

- `obs.VolumeServerNeedleWriteBytes.WithLabelValues(vid).Add(float64(n.Size))` — called by storage engine after each write
- `obs.VolumeServerNeedleReadBytes.WithLabelValues(vid).Add(float64(n.DataSize))` — called by storage engine after each read
- CRC mismatch errors are logged at `ERROR` level by the caller with the `volume_id` and `needle_id` fields

## 12. Testing Strategy

- **Unit tests** (table-driven):
  - `TestNeedleRoundTrip`: serialize then deserialize, assert all fields equal, for v1/v2/v3
  - `TestNeedleChecksum`: corrupt one byte of data, assert `VerifyChecksum` returns error
  - `TestNeedlePadding`: assert total size is always a multiple of 8
  - `TestSuperBlockEncoding`: encode and parse, assert equality
  - `TestIndexEntryEncoding`: encode and parse 16-byte entry
  - `TestTTLExpiry`: test with past/future timestamps
  - `TestCreateNeedleFromRequest`: valid upload, oversized upload, gzip-encoded body
- **Fuzz tests** (Go 1.18+):
  - `FuzzReadFrom`: feed arbitrary bytes, assert no panic (only parse errors)

## 13. Open Questions

None. The format is fully specified by the documentation.
