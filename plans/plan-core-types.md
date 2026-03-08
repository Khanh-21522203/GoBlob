# Feature: Core Types

## 1. Purpose

Core Types is the foundational package that defines every primitive type used across the entire GoBlob system. All other packages import from here. It contains no business logic—only type definitions, encoding helpers, and constants.

Without a shared types package, every subsystem would define its own `VolumeId`, `NeedleId`, or `Cookie`, leading to incompatible types, implicit conversions, and coupling between packages. By defining them once in a single package, the compiler enforces type safety across the entire codebase.

## 2. Responsibilities

- Define `VolumeId` (uint32): identifies a logical volume within the cluster
- Define `NeedleId` (uint64): identifies a needle (blob) within a volume
- Define `Cookie` (uint32): random anti-brute-force value paired with a NeedleId
- Define `FileId`: the compound public identifier `VolumeId,NeedleIdCookie` exposed to clients
- Define `Offset` (uint32): encoded offset within a `.dat` file (actual byte offset divided by 8)
- Define `Size` (uint32): byte size of a needle on disk; value `TombstoneFileSize` means deleted
- Define `DiskType`: enumeration of storage media types (`hdd`, `ssd`, custom tags)
- Define `ReplicaPlacement`: 3-digit encoding of replication topology policy
- Define `TTL`: per-needle expiry with granularity units
- Define `NeedleValue`: index entry combining `(NeedleId, Offset, Size)`
- Define `ServerAddress`: typed string `"host:port"` with helpers to derive gRPC address
- Define `TombstoneFileSize` constant
- Define `NeedleVersion` constants (v1, v2, v3)
- Provide parse/format helpers for `FileId` and `TTL`

## 3. Non-Responsibilities

- No I/O or disk access
- No network calls
- No business logic (allocation, routing, replication decisions)
- No configuration loading
- No logging

## 4. Architecture Design

Core Types sits at the bottom of the dependency graph. Every other package depends on it; it depends on nothing beyond the Go standard library.

```
+----------------------------------------------------------+
|                    All GoBlob Packages                    |
|  master | volume | filer | topology | storage | security  |
+---------------------------+------------------------------+
                            |
                    import core/types
                            |
              +-------------+-------------+
              |       goblob/core/types    |
              |  VolumeId  NeedleId Cookie |
              |  FileId    Offset  Size    |
              |  DiskType  TTL     RP      |
              |  ServerAddress NeedleValue |
              +----------------------------+
```

**FileId encoding**: The public file identifier follows the format `<VolumeId>,<NeedleId><Cookie>` where `NeedleId` and `Cookie` are concatenated as a single hex string. Example: `3,01637037d6` means VolumeId=3, NeedleId=0x01637037, Cookie=0xd6. Parsing splits on `,`, then splits the hex string at position `len-8` (last 8 hex chars = 4 bytes = Cookie).

**Offset encoding**: Stored as `actual_offset / 8` in a uint32. This allows addressing up to `2^32 * 8 = 32 GB` per volume file with 4-byte storage.

**ReplicaPlacement encoding**: A single byte `DDDRRRCC` where:
- `D` (bits 7-5): number of copies required in different data centers
- `R` (bits 4-2): number of copies required in same DC, different rack
- `C` (bits 1-0): number of copies on same server

## 5. Core Data Structures (Go)

```go
package types

import (
    "fmt"
    "strconv"
    "strings"
)

// VolumeId identifies a logical volume in the cluster.
// Assigned by the Master sequencer; max ~4 billion volumes per cluster.
type VolumeId uint32

// NeedleId uniquely identifies a needle within a volume.
// Assigned by the Master sequencer; globally monotonically increasing.
type NeedleId uint64

// Cookie is a random 4-byte value generated at write time.
// It is part of the public FileId, preventing brute-force enumeration.
type Cookie uint32

// Offset is the encoded byte offset of a needle inside a .dat file.
// Actual byte offset = Offset * NeedleAlignmentSize (8 bytes).
// Max addressable offset: 2^32 * 8 = 32 GB.
type Offset uint32

// Size is the on-disk byte size of a needle.
// A value of TombstoneFileSize means the needle has been deleted.
type Size uint32

const (
    // NeedleAlignmentSize is the alignment boundary for needles in .dat files.
    NeedleAlignmentSize uint64 = 8

    // TombstoneFileSize is the sentinel Size value written to the index
    // when a needle is logically deleted. The actual data is not removed
    // from .dat until compaction (vacuum).
    TombstoneFileSize Size = 0

    // NeedleIndexSize is the fixed size of one index entry in bytes.
    // Layout: NeedleId(8) + Offset(4) + Size(4) = 16 bytes.
    NeedleIndexSize = 16
)

// ToActualOffset converts the encoded Offset to the real byte position.
func (o Offset) ToActualOffset() int64 {
    return int64(o) * int64(NeedleAlignmentSize)
}

// ToEncoded converts an actual byte offset to the encoded Offset value.
// Panics if offset is not aligned to NeedleAlignmentSize.
func ToEncoded(actualOffset int64) Offset {
    return Offset(actualOffset / int64(NeedleAlignmentSize))
}

// NeedleValue is a single entry in the needle index.
// It maps a NeedleId to its location (Offset, Size) in the .dat file.
type NeedleValue struct {
    Key    NeedleId
    Offset Offset
    Size   Size
}

// IsDeleted reports whether this needle has been logically deleted.
func (nv NeedleValue) IsDeleted() bool {
    return nv.Size == TombstoneFileSize
}

// FileId is the compound public blob identifier exposed to clients.
// Format: "<VolumeId>,<NeedleId><Cookie>" where NeedleId+Cookie are hex-encoded.
// Example: "3,01637037d6"
type FileId struct {
    VolumeId VolumeId
    NeedleId NeedleId
    Cookie   Cookie
}

// String encodes FileId to its canonical wire format.
func (f FileId) String() string {
    // NeedleId: 8 bytes = 16 hex chars; Cookie: 4 bytes = 8 hex chars
    return fmt.Sprintf("%d,%016x%08x", f.VolumeId, f.NeedleId, f.Cookie)
}

// ParseFileId parses a string of the form "<vid>,<needleid><cookie>".
// Returns an error if the format is invalid.
func ParseFileId(s string) (FileId, error) {
    commaIdx := strings.Index(s, ",")
    if commaIdx < 0 {
        return FileId{}, fmt.Errorf("invalid file id %q: missing comma", s)
    }
    vidStr := s[:commaIdx]
    rest := s[commaIdx+1:]

    // rest must be >= 8 hex chars (cookie) + at least 1 char (needleId)
    if len(rest) < 9 {
        return FileId{}, fmt.Errorf("invalid file id %q: needle+cookie too short", s)
    }

    vid64, err := strconv.ParseUint(vidStr, 10, 32)
    if err != nil {
        return FileId{}, fmt.Errorf("invalid volume id in %q: %w", s, err)
    }

    // Last 8 hex chars = Cookie (4 bytes)
    cookieHex := rest[len(rest)-8:]
    needleHex := rest[:len(rest)-8]

    cookie64, err := strconv.ParseUint(cookieHex, 16, 32)
    if err != nil {
        return FileId{}, fmt.Errorf("invalid cookie in %q: %w", s, err)
    }
    needle64, err := strconv.ParseUint(needleHex, 16, 64)
    if err != nil {
        return FileId{}, fmt.Errorf("invalid needle id in %q: %w", s, err)
    }

    return FileId{
        VolumeId: VolumeId(vid64),
        NeedleId: NeedleId(needle64),
        Cookie:   Cookie(cookie64),
    }, nil
}

// DiskType represents the storage medium type of a disk directory.
// Used for tiered placement: hdd, ssd, or arbitrary custom tags.
type DiskType string

const (
    HardDriveType DiskType = "hdd"
    SolidStateType DiskType = "ssd"
    DefaultDiskType DiskType = ""
)

// ReplicaPlacement encodes the topology-aware replication policy.
// Stored as a single byte: bits 7-5 = DC count, bits 4-2 = rack count, bits 1-0 = server count.
type ReplicaPlacement struct {
    DifferentDataCenterCount uint8 // replicas on servers in different data centers
    DifferentRackCount       uint8 // replicas on servers in a different rack, same DC
    SameRackCount            uint8 // replicas on different servers in the same rack
}

// Byte encodes the placement policy to its 1-byte wire form.
func (rp ReplicaPlacement) Byte() byte {
    return (rp.DifferentDataCenterCount&0x07)<<5 |
        (rp.DifferentRackCount&0x07)<<2 |
        (rp.SameRackCount & 0x03)
}

// ParseReplicaPlacement decodes a single byte into a ReplicaPlacement.
func ParseReplicaPlacement(b byte) ReplicaPlacement {
    return ReplicaPlacement{
        DifferentDataCenterCount: (b >> 5) & 0x07,
        DifferentRackCount:       (b >> 2) & 0x07,
        SameRackCount:            b & 0x03,
    }
}

// String returns the human-readable 3-digit notation, e.g. "001", "010", "100".
func (rp ReplicaPlacement) String() string {
    return fmt.Sprintf("%d%d%d",
        rp.DifferentDataCenterCount,
        rp.DifferentRackCount,
        rp.SameRackCount,
    )
}

// TotalCopies returns the total number of copies including the primary.
func (rp ReplicaPlacement) TotalCopies() int {
    return 1 +
        int(rp.DifferentDataCenterCount) +
        int(rp.DifferentRackCount) +
        int(rp.SameRackCount)
}

// TTLUnit represents the granularity of a TTL value.
type TTLUnit byte

const (
    TTLUnitEmpty  TTLUnit = 0
    TTLUnitMinute TTLUnit = 'm'
    TTLUnitHour   TTLUnit = 'h'
    TTLUnitDay    TTLUnit = 'd'
    TTLUnitWeek   TTLUnit = 'w'
    TTLUnitMonth  TTLUnit = 'M'
    TTLUnitYear   TTLUnit = 'y'
)

// TTL encodes a per-needle time-to-live duration.
// Stored in 2 bytes: Count (1 byte, 1-255) and Unit (1 byte).
// A zero TTL means "no expiry".
type TTL struct {
    Count uint8
    Unit  TTLUnit
}

// IsNeverExpire reports whether the TTL means "live forever".
func (t TTL) IsNeverExpire() bool {
    return t.Count == 0
}

// Bytes encodes the TTL to its 2-byte wire representation.
func (t TTL) Bytes() [2]byte {
    return [2]byte{t.Count, byte(t.Unit)}
}

// ParseTTL parses a string like "3d", "12h", "1y" into a TTL.
func ParseTTL(s string) (TTL, error) {
    if s == "" {
        return TTL{}, nil
    }
    unit := TTLUnit(s[len(s)-1])
    count64, err := strconv.ParseUint(s[:len(s)-1], 10, 8)
    if err != nil {
        return TTL{}, fmt.Errorf("invalid ttl %q: %w", s, err)
    }
    return TTL{Count: uint8(count64), Unit: unit}, nil
}

// NeedleVersion is the binary format version of a needle.
type NeedleVersion uint8

const (
    NeedleVersionV1 NeedleVersion = 1 // basic needle
    NeedleVersionV2 NeedleVersion = 2 // with metadata fields
    NeedleVersionV3 NeedleVersion = 3 // with AppendAtNs timestamp
    CurrentNeedleVersion = NeedleVersionV3
)

// ServerAddress is a typed "host:port" string for a GoBlob service instance.
// gRPC port is always HTTP port + 10000 unless overridden.
type ServerAddress string

// ToGrpcAddress derives the gRPC address from an HTTP ServerAddress.
// If the address already encodes a gRPC port (host:port.grpcPort), extract it.
// Otherwise, add 10000 to the HTTP port.
func (a ServerAddress) ToGrpcAddress() string {
    // Implementation: parse "host:port" or "host:httpPort.grpcPort"
    // and return "host:grpcPort".
    s := string(a)
    dotIdx := strings.LastIndex(s, ".")
    colonIdx := strings.LastIndex(s, ":")
    if dotIdx > colonIdx {
        // format: "host:httpPort.grpcPort"
        return s[:colonIdx+1] + s[dotIdx+1:]
    }
    // format: "host:httpPort" -> derive grpcPort = httpPort + 10000
    parts := strings.SplitN(s, ":", 2)
    if len(parts) != 2 {
        return s
    }
    port, err := strconv.Atoi(parts[1])
    if err != nil {
        return s
    }
    return fmt.Sprintf("%s:%d", parts[0], port+10000)
}

// ToHttpAddress returns the HTTP address string.
func (a ServerAddress) ToHttpAddress() string {
    s := string(a)
    dotIdx := strings.LastIndex(s, ".")
    colonIdx := strings.LastIndex(s, ":")
    if dotIdx > colonIdx {
        return s[:dotIdx]
    }
    return s
}

// Host returns just the hostname portion.
func (a ServerAddress) Host() string {
    s := string(a)
    colonIdx := strings.Index(s, ":")
    if colonIdx < 0 {
        return s
    }
    return s[:colonIdx]
}
```

## 6. Public Interfaces

All types in this package are exported. There are no interface types—only concrete types and helper functions. The package surface is:

```go
// Type assertions and conversions
func ParseFileId(s string) (FileId, error)
func ParseReplicaPlacement(b byte) ReplicaPlacement
func ParseTTL(s string) (TTL, error)
func ToEncoded(actualOffset int64) Offset

// Type methods
func (f FileId) String() string
func (o Offset) ToActualOffset() int64
func (nv NeedleValue) IsDeleted() bool
func (rp ReplicaPlacement) Byte() byte
func (rp ReplicaPlacement) String() string
func (rp ReplicaPlacement) TotalCopies() int
func (t TTL) IsNeverExpire() bool
func (t TTL) Bytes() [2]byte
func (a ServerAddress) ToGrpcAddress() string
func (a ServerAddress) ToHttpAddress() string
func (a ServerAddress) Host() string
```

## 7. Internal Algorithms

### FileId Parsing
1. Find comma index to split `VolumeId` from `NeedleId+Cookie`
2. Parse `VolumeId` as decimal uint32
3. The hex suffix `rest` = NeedleId hex + Cookie hex; last 8 chars = Cookie
4. Parse Cookie from last 8 hex chars (uint32)
5. Parse NeedleId from remaining hex chars (uint64)
6. Return `FileId{VolumeId, NeedleId, Cookie}`

Time complexity: O(n) where n = string length.

### Offset Encoding
- Encode: `actualOffset / 8` — right-shift by 3 bits
- Decode: `encodedOffset * 8` — left-shift by 3 bits
- Constraint: actual offsets must be multiples of 8 (enforced by padding in needle format)

### ReplicaPlacement Byte Encoding
Bit layout `DDDRRRCC`:
- Bits 7-5 (3 bits): `DifferentDataCenterCount` (0-7)
- Bits 4-2 (3 bits): `DifferentRackCount` (0-7)
- Bits 1-0 (2 bits): `SameRackCount` (0-3)

## 8. Persistence Model

Not applicable. Core types are not persisted directly—they appear embedded in other persisted structures (needle binary format, index files, protobuf messages).

## 9. Concurrency Model

All types in this package are value types (structs or named scalars). They are inherently safe for concurrent use when passed by value. No locks, channels, or goroutines.

## 10. Configuration

No configuration. All constants are hardcoded:

```go
const (
    NeedleAlignmentSize uint64 = 8
    TombstoneFileSize   Size   = 0
    NeedleIndexSize            = 16
    CurrentNeedleVersion       = NeedleVersionV3
)
```

Default ports (informational constants, used by other packages):
```go
const (
    DefaultMasterHTTPPort = 9333
    DefaultVolumeHTTPPort = 8080
    DefaultFilerHTTPPort  = 8888
    DefaultS3HTTPPort     = 8333
    GRPCPortOffset        = 10000
)
```

## 11. Observability

No metrics or logging in this package. Errors are returned as typed `error` values with descriptive messages for callers to log.

## 12. Testing Strategy

**Unit tests** (table-driven):
- `TestParseFileId`: valid and invalid inputs, round-trip `String()`→`ParseFileId()`
- `TestOffsetEncoding`: round-trip `ToEncoded`→`ToActualOffset` for alignment boundary values
- `TestReplicaPlacementEncoding`: all combinations of D/R/C counts, round-trip byte→struct→byte
- `TestParseTTL`: each unit type, invalid inputs, zero count
- `TestServerAddressGrpcDerivation`: HTTP port + 10000, explicit gRPC port override, malformed inputs

All tests use only the standard library `testing` package. No external dependencies.

## 13. Open Questions

None. All types are fully specified by the documentation.
