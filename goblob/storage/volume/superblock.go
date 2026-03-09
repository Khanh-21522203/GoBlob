package volume

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"

	"GoBlob/goblob/core/types"
)

const SuperBlockSize = 8

// SuperBlock is the 8-byte header at the start of every .dat volume file.
//
// Binary layout:
//   Byte 0:   Version            uint8  — NeedleVersion (1, 2, or 3)
//   Byte 1:   ReplicaPlacement   uint8  — encoded as DDDRRRCC
//   Bytes 2-3: TTL               [2]byte — [Count, Unit]
//   Bytes 4-5: CompactionRevision uint16 — big-endian
//   Bytes 6-7: ExtraSize         uint16 — always 0 for now
type SuperBlock struct {
	Version            types.NeedleVersion
	ReplicaPlacement   types.ReplicaPlacement
	Ttl                types.TTL
	CompactionRevision uint16
}

// Bytes serializes the SuperBlock into exactly 8 bytes.
func (sb SuperBlock) Bytes() []byte {
	buf := make([]byte, SuperBlockSize)
	buf[0] = byte(sb.Version)
	buf[1] = sb.ReplicaPlacement.Byte()
	ttl := sb.Ttl.Bytes()
	buf[2] = ttl[0]
	buf[3] = ttl[1]
	binary.BigEndian.PutUint16(buf[4:6], sb.CompactionRevision)
	binary.BigEndian.PutUint16(buf[6:8], 0) // ExtraSize = 0
	return buf
}

// ParseSuperBlock parses a SuperBlock from at least 8 bytes of data.
func ParseSuperBlock(data []byte) (SuperBlock, error) {
	if len(data) < SuperBlockSize {
		return SuperBlock{}, fmt.Errorf("superblock data too short: %d bytes", len(data))
	}
	return SuperBlock{
		Version:            types.NeedleVersion(data[0]),
		ReplicaPlacement:   types.ParseReplicaPlacement(data[1]),
		Ttl:                types.TTL{Count: data[2], Unit: types.TTLUnit(data[3])},
		CompactionRevision: binary.BigEndian.Uint16(data[4:6]),
	}, nil
}

// ReadSuperBlock reads and parses the SuperBlock from the start of the .dat file.
func ReadSuperBlock(f *os.File) (SuperBlock, error) {
	buf := make([]byte, SuperBlockSize)
	_, err := io.ReadFull(f, buf)
	if err == io.EOF {
		return SuperBlock{}, io.EOF
	}
	if err != nil {
		return SuperBlock{}, fmt.Errorf("reading superblock: %w", err)
	}
	return ParseSuperBlock(buf)
}

// WriteSuperBlock writes the SuperBlock to byte offset 0 of the .dat file.
func WriteSuperBlock(f *os.File, sb SuperBlock) error {
	_, err := f.WriteAt(sb.Bytes(), 0)
	return err
}

// IsCompatible returns true if the SuperBlock version is understood.
func (sb SuperBlock) IsCompatible() bool {
	return sb.Version >= types.NeedleVersionV1 && sb.Version <= types.CurrentNeedleVersion
}
