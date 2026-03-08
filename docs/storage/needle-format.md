# Needle Format

## Assumptions

- Needle is the fundamental unit of stored data (a single uploaded file/blob).
- Needle file size is limited to 4GB (Size field is uint32).
- Three needle versions exist (v1, v2, v3) with progressive feature additions.

## Code Files / Modules Referenced

- `goblob/storage/needle/needle.go` - `Needle` struct, `CreateNeedleFromRequest()`
- `goblob/storage/needle/needle_read.go` - `ReadData()`, `ReadNeedleBody()`
- `goblob/storage/needle/needle_parse_upload.go` - HTTP upload parsing
- `goblob/storage/needle/volume_id.go` - `VolumeId` type
- `goblob/storage/needle/needle_id_type.go` - `NeedleId` type
- `goblob/storage/needle/file_id.go` - `FileId` (volume + needle + cookie)
- `goblob/storage/needle/crc.go` - CRC32 checksum
- `goblob/storage/needle/needle_ttl.go` - TTL (time to live) encoding
- `goblob/storage/needle/needle_version.go` - Version constants
- `goblob/storage/super_block/super_block.go` - `SuperBlock` (8-byte volume header)
- `goblob/storage/super_block/replica_placement.go` - `ReplicaPlacement` encoding
- `goblob/storage/types/` - `Cookie`, `NeedleId`, `Offset`, `Size`
- `goblob/storage/idx/` - Binary `.idx` file format

## Overview

A Needle represents a single stored file inside a GoBlob volume. The volume file (`.dat`) is an append-only sequence of needles. Each needle contains the file data, metadata (name, MIME type, TTL, timestamps), and a CRC32 checksum for integrity verification. An index file (`.idx`) maps needle IDs to their offsets in the data file.

## Responsibilities

- **Binary serialization**: Encode/decode needle to/from byte stream
- **Integrity**: CRC32 checksum for corruption detection
- **Anti-brute-force**: Cookie (random 4-byte value) prevents guessing file IDs
- **Metadata storage**: Filename, MIME type, key-value pairs, timestamps
- **TTL support**: Per-needle expiration with minute/hour/day/week/month/year granularity
- **Compression**: Optional gzip/zstd compression of data

## Architecture Role

```
+------------------------------------------------------------------+
|                     Volume (.dat file)                             |
+------------------------------------------------------------------+
| SuperBlock |  Needle 1  |  Needle 2  |  Needle 3  | ... | Needle N|
| (8+ bytes) | (variable) | (variable) | (variable) |     |(variable)|
+------------------------------------------------------------------+

+------------------------------------------------------------------+
|                     Index (.idx file)                              |
+------------------------------------------------------------------+
| Entry 1    | Entry 2    | Entry 3    | ...        | Entry N       |
| (16 bytes) | (16 bytes) | (16 bytes) |            | (16 bytes)    |
+------------------------------------------------------------------+
```

## Component Structure Diagram

### File ID Structure

```
File ID format: <VolumeId>,<NeedleId><Cookie>

Example: 3,01637037d6

  +----------+-----------------------------+
  | VolumeId | NeedleId + Cookie (hex)     |
  |    3     | 01637037d6                  |
  +----------+-----------------------------+
              |            |
              NeedleId     Cookie
              (8 bytes)    (4 bytes)
              
  VolumeId: identifies which volume on which server
  NeedleId: unique within the volume, from master Sequencer
  Cookie:   random, prevents brute-force lookups
```

### Needle Binary Layout (Version 2/3)

```
+----------+-----------+----------+
| Header   | Body      | Footer   |
+----------+-----------+----------+

Header (fixed 16 bytes):
+--------+----------+------+
| Cookie | NeedleId | Size |
| 4 B    | 8 B      | 4 B  |
+--------+----------+------+

Body (variable, present if Size > 0):
+----------+------+----------+------+----------+------+
| DataSize | Data | NameSize | Name | MimeSize | Mime |
| 4 B      | var  | 1 B      | var  | 1 B      | var  |
+----------+------+----------+------+----------+------+
+----------+-------+--------------+
| PairsSize| Pairs | LastModified |
| 2 B      | var   | 5 B          |
+----------+-------+--------------+
+-----+
| Ttl |
| 2 B |
+-----+

Footer:
+----------+--------------+---------+
| Checksum | AppendAtNs   | Padding |
| 4 B      | 8 B (v3 only)| 0-7 B   |
+----------+--------------+---------+

Padding: aligns total needle size to 8-byte boundary
```

### SuperBlock Layout

```
+-------+------------------+-----+------------------+--------+
| Byte 0| Byte 1           |Byte | Byte 4-5         |Byte 6-7|
|Version|ReplicaPlacement   |2-3  |CompactionRevision|ExtraSize|
|       |                   |TTL  |                  |        |
+-------+------------------+-----+------------------+--------+
| 1 B   | 1 B              | 2 B | 2 B              | 2 B    |
+-------+------------------+-----+------------------+--------+

Total: 8 bytes (base) + ExtraSize bytes (protobuf SuperBlockExtra)

Version: 1 (basic), 2 (with metadata), 3 (with AppendAtNs)

ReplicaPlacement byte encoding:
  Bits: DDDRRRCC
  D = different data center count
  R = same rack, different server count
  C = same server (copy) count
  Example: 001 = one copy in a different data center

TTL encoding (2 bytes):
  Byte 0: count (1-255)
  Byte 1: unit (m=minute, h=hour, d=day, w=week, M=month, y=year)
  Example: 0x03 0x64 = "3d" (3 days)
```

### Index Entry Layout

```
+----------+--------+------+
| NeedleId | Offset | Size |
| 8 B      | 4 B    | 4 B  |
+----------+--------+------+
Total: 16 bytes per entry

NeedleId: uint64, the needle identifier
Offset:   uint32, byte offset in .dat file (divided by 8, so max ~32GB)
Size:     uint32, total needle size in bytes
          Size == 0: tombstone (deleted needle)
```

## Control Flow

### Needle Creation from HTTP Upload

```
CreateNeedleFromRequest(r *http.Request, fixJpgOrientation, sizeLimit, buffer)
    |
    +--> ParseUpload(r, sizeLimit, buffer)
    |       +--> Parse multipart form
    |       +--> Read file data (with size limit check)
    |       +--> Extract filename, mime type, modification time
    |       +--> Extract TTL, content-md5
    |       +--> Extract GoBlob-* header pairs
    |       +--> Optional: fix JPEG orientation
    |       +--> Optional: gzip/zstd compress
    |       +--> Return ParsedUpload{Data, FileName, MimeType, ...}
    |
    +--> Set needle fields from ParsedUpload:
    |    n.Data, n.LastModified, n.Ttl
    |    n.Name (if < 256 bytes), n.Mime (if < 256 bytes)
    |    n.Pairs (if key-value pairs, JSON, < 64KB)
    |
    +--> return (needle, originalSize, contentMd5, error)
```

### Needle Read from Volume

```
ReadData(bytes, offset, size, version)
    |
    +--> Parse header: Cookie (4B), Id (8B), Size (4B)
    |
    +--> If version >= 2:
    |       Parse DataSize (4B)
    |       Read Data[0:DataSize]
    |       Parse flags byte
    |       If HasName: parse NameSize (1B), Name
    |       If HasMime: parse MimeSize (1B), Mime
    |       If HasPairs: parse PairsSize (2B), Pairs
    |       If HasLastModified: parse LastModified (5B)
    |       If HasTtl: parse Ttl (2B)
    |
    +--> Parse Checksum (CRC32, 4B)
    |
    +--> If version >= 3:
    |       Parse AppendAtNs (8B)
    |
    +--> Verify CRC32 checksum
    |
    +--> return error if checksum mismatch
```

## Data Flow Diagram

```
HTTP Upload                   Needle Binary              Volume File
+-----------+                 +-------------+            +----------+
| multipart |  --parse-->     | Cookie      |  --append->| .dat     |
| form data |                 | NeedleId    |            | (append  |
| + headers |                 | Size        |            |  only)   |
+-----------+                 | DataSize    |            +----------+
                              | Data[]      |
                              | NameSize    |            +----------+
                              | Name[]      |  --put-->  | .idx     |
                              | MimeSize    |            | (NeedleId|
                              | Mime[]      |            |  Offset  |
                              | PairsSize   |            |  Size)   |
                              | Pairs[]     |            +----------+
                              | LastModified|
                              | Ttl         |
                              | Checksum    |
                              | AppendAtNs  |
                              | Padding     |
                              +-------------+
```

## Dependencies

| Dependency | Purpose |
|---|---|
| `goblob/images` | JPEG orientation fix |
| `goblob/storage/types` | Type definitions (Cookie, NeedleId, Size, Offset, CRC) |
| `goblob/util` | Byte conversion utilities |
| `hash/crc32` | CRC32 checksum (IEEE polynomial) |
| `compress/gzip` | Optional gzip compression |

## Error Handling

- **CRC mismatch**: Critical error on read; indicates data corruption
- **Size overflow**: Needle size limited to uint32 (4GB); uploads exceeding `fileSizeLimitMB` rejected
- **TTL expiry**: Expired needles return "not found" on read but remain in .dat until compaction
- **Cookie mismatch**: Read returns error (prevents brute-force access)
- **Truncated read**: If pread returns fewer bytes than expected, returns I/O error

## Edge Cases

- **Empty needle**: Size == 0 in index means tombstone (deleted)
- **Padding alignment**: All needles padded to 8-byte boundary for efficient disk I/O
- **Offset encoding**: Stored as uint32 but represents actual_offset/8, allowing max ~32GB per volume
- **Version upgrade**: SuperBlock version determines which needle fields are present
- **Compression flag**: Stored in Flags byte; reader auto-detects and decompresses
- **Large pairs**: Key-value pairs limited to 64KB total; excess silently truncated
- **Append timestamp**: Version 3 adds AppendAtNs for precise ordering and conflict resolution
