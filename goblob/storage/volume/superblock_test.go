package volume

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"

	"GoBlob/goblob/core/types"
)

func TestSuperBlockBytes(t *testing.T) {
	sb := SuperBlock{
		Version:            types.NeedleVersionV3,
		ReplicaPlacement:   types.ReplicaPlacement{DifferentDataCenterCount: 1, DifferentRackCount: 0, SameRackCount: 0},
		Ttl:                types.TTL{Count: 3, Unit: types.TTLUnitDay},
		CompactionRevision: 5,
	}

	b := sb.Bytes()
	if len(b) != SuperBlockSize {
		t.Fatalf("expected %d bytes, got %d", SuperBlockSize, len(b))
	}
	if b[0] != byte(types.NeedleVersionV3) {
		t.Errorf("Version byte: got %d, want %d", b[0], types.NeedleVersionV3)
	}
	if b[1] != sb.ReplicaPlacement.Byte() {
		t.Errorf("ReplicaPlacement byte: got %d, want %d", b[1], sb.ReplicaPlacement.Byte())
	}
	if b[2] != 3 || b[3] != byte(types.TTLUnitDay) {
		t.Errorf("TTL bytes: got [%d,%d], want [3,%d]", b[2], b[3], types.TTLUnitDay)
	}
	rev := uint16(b[4])<<8 | uint16(b[5])
	if rev != 5 {
		t.Errorf("CompactionRevision: got %d, want 5", rev)
	}
}

func TestParseSuperBlock(t *testing.T) {
	original := SuperBlock{
		Version:            types.NeedleVersionV3,
		ReplicaPlacement:   types.ParseReplicaPlacement(0b00100100),
		Ttl:                types.TTL{Count: 7, Unit: types.TTLUnitHour},
		CompactionRevision: 42,
	}

	parsed, err := ParseSuperBlock(original.Bytes())
	if err != nil {
		t.Fatalf("ParseSuperBlock: %v", err)
	}

	if parsed.Version != original.Version {
		t.Errorf("Version: got %d, want %d", parsed.Version, original.Version)
	}
	if parsed.ReplicaPlacement != original.ReplicaPlacement {
		t.Errorf("ReplicaPlacement: got %v, want %v", parsed.ReplicaPlacement, original.ReplicaPlacement)
	}
	if parsed.Ttl != original.Ttl {
		t.Errorf("TTL: got %v, want %v", parsed.Ttl, original.Ttl)
	}
	if parsed.CompactionRevision != original.CompactionRevision {
		t.Errorf("CompactionRevision: got %d, want %d", parsed.CompactionRevision, original.CompactionRevision)
	}
}

func TestParseSuperBlockTooShort(t *testing.T) {
	_, err := ParseSuperBlock([]byte{1, 2, 3})
	if err == nil {
		t.Error("expected error for short data")
	}
}

func TestReadWriteSuperBlock(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.dat")

	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}

	sb := SuperBlock{
		Version:            types.NeedleVersionV3,
		CompactionRevision: 2,
	}

	if err := WriteSuperBlock(f, sb); err != nil {
		t.Fatalf("WriteSuperBlock: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	f2, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f2.Close()

	parsed, err := ReadSuperBlock(f2)
	if err != nil {
		t.Fatalf("ReadSuperBlock: %v", err)
	}
	if parsed.Version != sb.Version {
		t.Errorf("Version: got %d, want %d", parsed.Version, sb.Version)
	}
	if parsed.CompactionRevision != sb.CompactionRevision {
		t.Errorf("CompactionRevision: got %d, want %d", parsed.CompactionRevision, sb.CompactionRevision)
	}
}

func TestReadSuperBlockEOF(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "empty.dat")
	f, _ := os.Create(path)
	f.Close()

	f2, _ := os.Open(path)
	defer f2.Close()
	_, err := ReadSuperBlock(f2)
	if err != io.EOF {
		t.Errorf("expected io.EOF for empty file, got %v", err)
	}
}

func TestSuperBlockIsCompatible(t *testing.T) {
	for _, v := range []types.NeedleVersion{types.NeedleVersionV1, types.NeedleVersionV2, types.NeedleVersionV3} {
		sb := SuperBlock{Version: v}
		if !sb.IsCompatible() {
			t.Errorf("Version %d should be compatible", v)
		}
	}
	sb := SuperBlock{Version: 99}
	if sb.IsCompatible() {
		t.Error("Version 99 should not be compatible")
	}
}

// Ensure Bytes() produces exactly SuperBlockSize bytes
func TestSuperBlockBytesLength(t *testing.T) {
	sb := SuperBlock{}
	b := sb.Bytes()
	if len(b) != SuperBlockSize {
		t.Errorf("expected %d bytes, got %d", SuperBlockSize, len(b))
	}
	_ = bytes.NewReader(b) // silence unused import
}
