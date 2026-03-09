package types

import (
	"fmt"
	"strconv"
	"strings"
)

// Core types used across all GoBlob components.

type VolumeId uint32    // identifies a logical volume; max ~4 billion
type NeedleId uint64    // blob identity within a volume; globally monotonic
type Cookie   uint32    // random 4-byte anti-brute-force value
type Offset   uint32    // encoded byte offset = actualOffset / 8
type Size     uint32    // on-disk byte size; 0 = tombstone (deleted)
type DiskType string    // "hdd", "ssd", or custom tag; "" = default
type NeedleVersion uint8
type ServerAddress string  // typed "host:port" string

const (
	NeedleAlignmentSize uint64 = 8
	TombstoneFileSize   Size   = 0
	NeedleIndexSize            = 16 // NeedleId(8)+Offset(4)+Size(4)
)

const (
	NeedleVersionV1 NeedleVersion = 1
	NeedleVersionV2 NeedleVersion = 2
	NeedleVersionV3 NeedleVersion = 3
	CurrentNeedleVersion = NeedleVersionV3
)

const (
	DefaultMasterHTTPPort = 9333
	DefaultVolumeHTTPPort = 8080
	DefaultFilerHTTPPort  = 8888
	DefaultS3HTTPPort     = 8333
	GRPCPortOffset        = 10000
)

const (
	HardDriveType   DiskType = "hdd"
	SolidStateType  DiskType = "ssd"
	DefaultDiskType DiskType = ""
)

// Offset helpers

func (o Offset) ToActualOffset() int64 {
	return int64(o) * int64(NeedleAlignmentSize)
}

func ToEncoded(actualOffset int64) Offset {
	return Offset(actualOffset / int64(NeedleAlignmentSize))
}

// NeedleValue represents one entry in the .idx file

type NeedleValue struct {
	Key    NeedleId
	Offset Offset
	Size   Size
}

func (nv NeedleValue) IsDeleted() bool {
	return nv.Size == TombstoneFileSize
}

// FileId with exact string encoding: "<VolumeId>,<NeedleId><Cookie>"
// NeedleId is 16 hex chars, Cookie is 8 hex chars
// Example: VolumeId=3, NeedleId=0x01637037, Cookie=0xd6 → "3,0000000001637037000000d6"

type FileId struct {
	VolumeId VolumeId
	NeedleId NeedleId
	Cookie   Cookie
}

func (f FileId) String() string {
	return fmt.Sprintf("%d,%016x%08x", f.VolumeId, f.NeedleId, f.Cookie)
}

func ParseFileId(s string) (FileId, error) {
	commaIdx := strings.Index(s, ",")
	if commaIdx == -1 {
		return FileId{}, fmt.Errorf("invalid FileId: no comma found")
	}

	volumeIdStr := s[:commaIdx]
	rest := s[commaIdx+1:]

	if len(rest) < 9 {
		return FileId{}, fmt.Errorf("invalid FileId: hex string too short")
	}

	volumeId, err := strconv.ParseUint(volumeIdStr, 10, 32)
	if err != nil {
		return FileId{}, fmt.Errorf("invalid VolumeId: %w", err)
	}

	// Last 8 hex chars = Cookie
	cookieStr := rest[len(rest)-8:]
	cookie, err := strconv.ParseUint(cookieStr, 16, 32)
	if err != nil {
		return FileId{}, fmt.Errorf("invalid Cookie: %w", err)
	}

	// Remaining hex chars = NeedleId
	needleIdStr := rest[:len(rest)-8]
	needleId, err := strconv.ParseUint(needleIdStr, 16, 64)
	if err != nil {
		return FileId{}, fmt.Errorf("invalid NeedleId: %w", err)
	}

	return FileId{
		VolumeId: VolumeId(volumeId),
		NeedleId: NeedleId(needleId),
		Cookie:   Cookie(cookie),
	}, nil
}

// ReplicaPlacement with exact 1-byte encoding (bit layout DDRRRRCC)

type ReplicaPlacement struct {
	DifferentDataCenterCount uint8 // bits 7-5 (3 bits)
	DifferentRackCount       uint8 // bits 4-2 (3 bits)
	SameRackCount            uint8 // bits 1-0 (2 bits)
}

func (rp ReplicaPlacement) Byte() byte {
	return (rp.DifferentDataCenterCount&0x07)<<5 |
		(rp.DifferentRackCount&0x07)<<2 |
		(rp.SameRackCount & 0x03)
}

func ParseReplicaPlacement(b byte) ReplicaPlacement {
	return ReplicaPlacement{
		DifferentDataCenterCount: (b >> 5) & 0x07,
		DifferentRackCount:       (b >> 2) & 0x07,
		SameRackCount:            b & 0x03,
	}
}

func (rp ReplicaPlacement) TotalCopies() int {
	return 1 + int(rp.DifferentDataCenterCount) + int(rp.DifferentRackCount) + int(rp.SameRackCount)
}

func (rp ReplicaPlacement) String() string {
	return fmt.Sprintf("%d%d%d", rp.DifferentDataCenterCount, rp.DifferentRackCount, rp.SameRackCount)
}

// TTL with exact 2-byte wire format

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

type TTL struct {
	Count uint8
	Unit  TTLUnit
}

func (t TTL) IsNeverExpire() bool {
	return t.Count == 0
}

func (t TTL) Bytes() [2]byte {
	return [2]byte{t.Count, byte(t.Unit)}
}

func ParseTTL(s string) (TTL, error) {
	if s == "" {
		return TTL{}, nil
	}

	if len(s) < 2 {
		return TTL{}, fmt.Errorf("invalid TTL: too short")
	}

	countStr := s[:len(s)-1]
	unitChar := s[len(s)-1]

	count, err := strconv.ParseUint(countStr, 10, 8)
	if err != nil {
		return TTL{}, fmt.Errorf("invalid TTL count: %w", err)
	}

	unit := TTLUnit(unitChar)
	switch unit {
	case TTLUnitMinute, TTLUnitHour, TTLUnitDay, TTLUnitWeek, TTLUnitMonth, TTLUnitYear:
		return TTL{Count: uint8(count), Unit: unit}, nil
	default:
		return TTL{}, fmt.Errorf("invalid TTL unit: %c", unitChar)
	}
}

func (t TTL) String() string {
	if t.Count == 0 {
		return ""
	}
	return fmt.Sprintf("%d%c", t.Count, t.Unit)
}

// ServerAddress helpers

func (sa ServerAddress) ToGrpcAddress() ServerAddress {
	// If address is "host:httpPort", return "host:httpPort+10000"
	// If address is "host:httpPort.grpcPort", return "host:grpcPort"
	parts := strings.Split(string(sa), ".")
	if len(parts) == 1 {
		// Simple host:port format
		host, portStr, err := splitHostPort(string(sa))
		if err != nil {
			return sa
		}
		port, err := strconv.ParseUint(portStr, 10, 32)
		if err != nil {
			return sa
		}
		return ServerAddress(fmt.Sprintf("%s:%d", host, port+GRPCPortOffset))
	}
	// Has explicit gRPC port
	return sa
}

func (sa ServerAddress) ToHttpAddress() ServerAddress {
	parts := strings.Split(string(sa), ".")
	if len(parts) > 1 {
		// Strip gRPC port suffix
		return ServerAddress(parts[0])
	}
	return sa
}

func (sa ServerAddress) Host() string {
	host, _, err := splitHostPort(string(sa))
	if err != nil {
		return string(sa)
	}
	return host
}

func splitHostPort(s string) (string, string, error) {
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return s, "", fmt.Errorf("invalid address format")
	}
	return parts[0], parts[1], nil
}
