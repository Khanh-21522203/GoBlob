package types

import (
	"testing"
)

func TestParseFileId(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    FileId
		wantErr bool
	}{
		{
			name:  "valid FileId - basic",
			input: "3,0000000001637037000000d6",
			want: FileId{
				VolumeId: 3,
				NeedleId: 0x01637037,
				Cookie:   0xd6,
			},
			wantErr: false,
		},
		{
			name:  "valid FileId - all zeros",
			input: "0,000000000000000000000000",
			want: FileId{
				VolumeId: 0,
				NeedleId: 0,
				Cookie:   0,
			},
			wantErr: false,
		},
		{
			name:  "valid FileId - max values",
			input: "4294967295,ffffffffffffffffffffffff",
			want: FileId{
				VolumeId: 0xFFFFFFFF,
				NeedleId: 0xFFFFFFFFFFFFFFFF,
				Cookie:   0xFFFFFFFF,
			},
			wantErr: false,
		},
		{
			name:    "invalid - no comma",
			input:   "0000000001637037000000d6",
			wantErr: true,
		},
		{
			name:    "invalid - too short",
			input:   "3,abc",
			wantErr: true,
		},
		{
			name:    "invalid - bad volume id",
			input:   "abc,000000000000000000000000",
			wantErr: true,
		},
		{
			name:    "invalid - bad needle id hex",
			input:   "3,xxxxxxxxxxxxxxxx00000000",
			wantErr: true,
		},
		{
			name:    "invalid - bad cookie hex",
			input:   "3,0000000000000000xxxxxxxx",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseFileId(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseFileId() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("ParseFileId() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFileIdRoundTrip(t *testing.T) {
	tests := []struct {
		name  string
		fid   FileId
	}{
		{"basic", FileId{VolumeId: 3, NeedleId: 0x01637037, Cookie: 0xd6}},
		{"zeros", FileId{VolumeId: 0, NeedleId: 0, Cookie: 0}},
		{"max", FileId{VolumeId: 0xFFFFFFFF, NeedleId: 0xFFFFFFFFFFFFFFFF, Cookie: 0xFFFFFFFF}},
		{"random1", FileId{VolumeId: 123, NeedleId: 0xdeadbeef, Cookie: 0xcafe}},
		{"random2", FileId{VolumeId: 9999, NeedleId: 0x1234567890abcdef, Cookie: 0xbabe}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := tt.fid.String()
			got, err := ParseFileId(s)
			if err != nil {
				t.Errorf("ParseFileId(%q) error = %v", s, err)
				return
			}
			if got != tt.fid {
				t.Errorf("round trip: got %v, want %v", got, tt.fid)
			}
		})
	}
}

func TestOffsetEncoding(t *testing.T) {
	tests := []struct {
		name         string
		actualOffset int64
	}{
		{"zero", 0},
		{"aligned", 8},
		{"aligned 2", 16},
		{"unaligned", 9},  // gets truncated
		{"large", 1024 * 1024 * 8},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded := ToEncoded(tt.actualOffset)
			got := encoded.ToActualOffset()
			// We expect to lose precision if not aligned
			expected := (tt.actualOffset / 8) * 8
			if got != expected {
				t.Errorf("ToEncoded(%d).ToActualOffset() = %d, want %d", tt.actualOffset, got, expected)
			}
		})
	}
}

func TestReplicaPlacement(t *testing.T) {
	tests := []struct {
		name string
		rp   ReplicaPlacement
		want byte
	}{
		{"000", ReplicaPlacement{0, 0, 0}, 0x00},
		{"001", ReplicaPlacement{0, 0, 1}, 0x01},
		{"010", ReplicaPlacement{0, 1, 0}, 0x04},
		{"011", ReplicaPlacement{0, 1, 1}, 0x05},
		{"100", ReplicaPlacement{1, 0, 0}, 0x20},
		{"101", ReplicaPlacement{1, 0, 1}, 0x21},
		{"111", ReplicaPlacement{1, 1, 1}, 0x25},
		{"max", ReplicaPlacement{7, 7, 3}, 0xFF},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.rp.Byte(); got != tt.want {
				t.Errorf("ReplicaPlacement.Byte() = 0x%02x, want 0x%02x", got, tt.want)
			}
			// Round-trip
			parsed := ParseReplicaPlacement(tt.want)
			if parsed != tt.rp {
				t.Errorf("ParseReplicaPlacement(0x%02x) = %v, want %v", tt.want, parsed, tt.rp)
			}
			// Check TotalCopies
			expectedTotal := 1 + int(tt.rp.DifferentDataCenterCount) + int(tt.rp.DifferentRackCount) + int(tt.rp.SameRackCount)
			if got := tt.rp.TotalCopies(); got != expectedTotal {
				t.Errorf("TotalCopies() = %d, want %d", got, expectedTotal)
			}
		})
	}
}

func TestParseTTL(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    TTL
		wantErr bool
	}{
		{"empty", "", TTL{}, false},
		{"minutes", "5m", TTL{Count: 5, Unit: TTLUnitMinute}, false},
		{"hours", "2h", TTL{Count: 2, Unit: TTLUnitHour}, false},
		{"days", "7d", TTL{Count: 7, Unit: TTLUnitDay}, false},
		{"weeks", "1w", TTL{Count: 1, Unit: TTLUnitWeek}, false},
		{"months", "6M", TTL{Count: 6, Unit: TTLUnitMonth}, false},
		{"years", "1y", TTL{Count: 1, Unit: TTLUnitYear}, false},
		{"max count", "255m", TTL{Count: 255, Unit: TTLUnitMinute}, false},
		{"too short", "m", TTL{}, true},
		{"invalid unit", "5x", TTL{}, true},
		{"invalid count", "abc", TTL{}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseTTL(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseTTL() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("ParseTTL() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTTLString(t *testing.T) {
	tests := []struct {
		name string
		ttl  TTL
		want string
	}{
		{"zero", TTL{}, ""},
		{"minutes", TTL{Count: 5, Unit: TTLUnitMinute}, "5m"},
		{"hours", TTL{Count: 2, Unit: TTLUnitHour}, "2h"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.ttl.String(); got != tt.want {
				t.Errorf("TTL.String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTTLBytes(t *testing.T) {
	ttl := TTL{Count: 5, Unit: TTLUnitMinute}
	got := ttl.Bytes()
	want := [2]byte{5, 'm'}
	if got != want {
		t.Errorf("TTL.Bytes() = %v, want %v", got, want)
	}
}

func TestServerAddressGrpcDerivation(t *testing.T) {
	tests := []struct {
		name     string
		input    ServerAddress
		expected ServerAddress
	}{
		{"simple port 8080", "localhost:8080", "localhost:18080"},
		{"simple port 9333", "master:9333", "master:19333"},
		{"with grpc port", "volume:8080.18080", "volume:8080.18080"},
		{"just host", "localhost", "localhost"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.input.ToGrpcAddress()
			if got != tt.expected {
				t.Errorf("ToGrpcAddress() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestServerAddressHttpDerivation(t *testing.T) {
	tests := []struct {
		name     string
		input    ServerAddress
		expected ServerAddress
	}{
		{"simple", "localhost:8080", "localhost:8080"},
		{"with grpc port", "localhost:8080.18080", "localhost:8080"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.input.ToHttpAddress()
			if got != tt.expected {
				t.Errorf("ToHttpAddress() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestServerAddressHost(t *testing.T) {
	tests := []struct {
		name     string
		input    ServerAddress
		expected string
	}{
		{"simple", "localhost:8080", "localhost"},
		{"with grpc port", "localhost:8080.18080", "localhost"},
		{"just host", "localhost", "localhost"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.input.Host()
			if got != tt.expected {
				t.Errorf("Host() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestNeedleValueIsDeleted(t *testing.T) {
	tests := []struct {
		name string
		nv   NeedleValue
		want bool
	}{
		{"deleted", NeedleValue{Size: TombstoneFileSize}, true},
		{"not deleted", NeedleValue{Size: 100}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.nv.IsDeleted(); got != tt.want {
				t.Errorf("NeedleValue.IsDeleted() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTTLIsNeverExpire(t *testing.T) {
	tests := []struct {
		name string
		ttl  TTL
		want bool
	}{
		{"zero count", TTL{}, true},
		{"non-zero", TTL{Count: 1, Unit: TTLUnitMinute}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.ttl.IsNeverExpire(); got != tt.want {
				t.Errorf("TTL.IsNeverExpire() = %v, want %v", got, tt.want)
			}
		})
	}
}
