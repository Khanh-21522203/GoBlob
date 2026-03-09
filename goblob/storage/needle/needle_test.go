package needle

import (
	"bytes"
	"testing"

	"GoBlob/goblob/core/types"
)

func TestNeedleRoundTrip(t *testing.T) {
	tests := []struct {
		name    string
		version types.NeedleVersion
		needle  func() *Needle
	}{
		{
			name:    "v1 minimal",
			version: types.NeedleVersionV1,
			needle: func() *Needle {
				n := &Needle{
					Cookie:   0x12345678,
					Id:       0x123456789ABCDEF0,
					DataSize: 4,
					Data:     []byte("test"),
					Flags:    0,
				}
				n.SetAppendAtNs()
				return n
			},
		},
		{
			name:    "v2 with name and mime",
			version: types.NeedleVersionV2,
			needle: func() *Needle {
				n := &Needle{
					Cookie:   0xABCDEF01,
					Id:       0xFEDCBA9876543210,
					DataSize: 12,
					Data:     []byte("hello world!"),
				}
				n.SetName("test.txt")
				n.SetMime("text/plain")
				n.SetAppendAtNs()
				return n
			},
		},
		{
			name:    "v3 with all fields",
			version: types.NeedleVersionV3,
			needle: func() *Needle {
				n := &Needle{
					Cookie:   0xDEADBEEF,
					Id:       0x0123456789ABCDEF,
					DataSize: 3,
					Data:     []byte("xyz"),
				}
				n.SetName("example.dat")
				n.SetMime("application/octet-stream")
				n.SetPairs(`{"key":"value"}`)
				n.SetLastModified(1234567890)
				n.SetTtl(types.TTL{Count: 7, Unit: types.TTLUnitDay})
				n.SetAppendAtNs()
				return n
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original := tt.needle()

			// Write to buffer
			var buf bytes.Buffer
			written, err := original.WriteTo(&buf, tt.version)
			if err != nil {
				t.Fatalf("WriteTo failed: %v", err)
			}

			// Read from buffer
			data := buf.Bytes()
			parsed := &Needle{}
			if err := parsed.ReadFrom(data, 0, types.Size(written), tt.version); err != nil {
				t.Fatalf("ReadFrom failed: %v", err)
			}

			// Verify all fields match
			if parsed.Cookie != original.Cookie {
				t.Errorf("Cookie mismatch: got %d, want %d", parsed.Cookie, original.Cookie)
			}
			if parsed.Id != original.Id {
				t.Errorf("Id mismatch: got %d, want %d", parsed.Id, original.Id)
			}
			if parsed.DataSize != original.DataSize {
				t.Errorf("DataSize mismatch: got %d, want %d", parsed.DataSize, original.DataSize)
			}
			if !bytes.Equal(parsed.Data, original.Data) {
				t.Errorf("Data mismatch: got %v, want %v", parsed.Data, original.Data)
			}
			if parsed.Flags != original.Flags {
				t.Errorf("Flags mismatch: got %d, want %d", parsed.Flags, original.Flags)
			}
			if parsed.HasName() && string(parsed.Name) != string(original.Name) {
				t.Errorf("Name mismatch: got %s, want %s", parsed.Name, original.Name)
			}
			if parsed.HasMime() && string(parsed.Mime) != string(original.Mime) {
				t.Errorf("Mime mismatch: got %s, want %s", parsed.Mime, original.Mime)
			}
			if parsed.HasPairs() && string(parsed.Pairs) != string(original.Pairs) {
				t.Errorf("Pairs mismatch: got %s, want %s", parsed.Pairs, original.Pairs)
			}
			if parsed.HasLastModified() && parsed.LastModified != original.LastModified {
				t.Errorf("LastModified mismatch: got %d, want %d", parsed.LastModified, original.LastModified)
			}
			if parsed.HasTtl() && parsed.Ttl != original.Ttl {
				t.Errorf("Ttl mismatch: got %v, want %v", parsed.Ttl, original.Ttl)
			}
			if parsed.Checksum != original.Checksum {
				t.Errorf("Checksum mismatch: got %d, want %d", parsed.Checksum, original.Checksum)
			}

			// Verify 8-byte alignment
			if written%8 != 0 {
				t.Errorf("Written size not 8-byte aligned: %d", written)
			}
		})
	}
}

func TestNeedleChecksum(t *testing.T) {
	n := &Needle{
		Cookie:   0x12345678,
		Id:       0x123456789ABCDEF0,
		DataSize: 4,
		Data:     []byte("test"),
	}
	n.SetAppendAtNs()

	var buf bytes.Buffer
	written, err := n.WriteTo(&buf, types.NeedleVersionV3)
	if err != nil {
		t.Fatalf("WriteTo failed: %v", err)
	}

	data := buf.Bytes()

	// Corrupt one byte of data
	data[20] ^= 0xFF

	parsed := &Needle{}
	err = parsed.ReadFrom(data, 0, types.Size(written), types.NeedleVersionV3)
	if err == nil {
		t.Error("Expected checksum error for corrupted data")
	}
	if err != ErrChecksumMismatch {
		t.Errorf("Expected ErrChecksumMismatch, got: %v", err)
	}
}

func TestNeedlePadding(t *testing.T) {
	tests := []struct {
		name         string
		dataSize     uint32
		hasName      bool
		hasMime      bool
		hasPairs     bool
		hasLastMod   bool
		hasTtl       bool
		alignedTotal int
	}{
		// v3 format: 16-byte header + body + 12-byte footer + padding
		// minimal body: 4-byte DataSize field + Data + 1-byte Flags
		{"minimal", 1, false, false, false, false, false, 40},   // 16+6+12+6=40
		{"minimal + 1", 2, false, false, false, false, false, 40}, // 16+7+12+5=40
		{"minimal + 2", 3, false, false, false, false, false, 40}, // 16+8+12+4=40
		{"minimal + 3", 4, false, false, false, false, false, 40}, // 16+9+12+3=40
		{"minimal + 4", 5, false, false, false, false, false, 40}, // 16+10+12+2=40
		{"minimal + 5", 6, false, false, false, false, false, 40}, // 16+11+12+1=40
		{"minimal + 6", 7, false, false, false, false, false, 40}, // 16+12+12=40
		{"minimal + 7", 8, false, false, false, false, false, 48}, // 16+13+12+7=48
		{"with name", 4, true, false, false, false, false, 48}, // 16+9+1+8+12+2=48
		{"with name + mime", 4, true, true, false, false, false, 64}, // 16+9+1+8+1+10+12=56, +8=64
		{"large data", 100, false, false, false, false, false, 136}, // 16+104+12+4=136
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := &Needle{
				Cookie:   0x12345678,
				Id:       0x123456789ABCDEF0,
				DataSize: tt.dataSize,
				Data:     make([]byte, tt.dataSize),
			}
			if tt.hasName {
				n.SetName("test.txt")
			}
			if tt.hasMime {
				n.SetMime("text/plain")
			}
			n.SetAppendAtNs()

			var buf bytes.Buffer
			written, err := n.WriteTo(&buf, types.NeedleVersionV3)
			if err != nil {
				t.Fatalf("WriteTo failed: %v", err)
			}

			if int(written) != tt.alignedTotal {
				t.Errorf("Size mismatch: got %d, want %d", written, tt.alignedTotal)
			}

			if written%8 != 0 {
				t.Errorf("Not 8-byte aligned: %d", written)
			}
		})
	}
}

func TestNeedleMinimal(t *testing.T) {
	// Needle with DataSize=0 represents a tombstone
	n := &Needle{
		Cookie:   0x12345678,
		Id:       0x123456789ABCDEF0,
		DataSize: 0,
		Data:     nil,
	}
	n.SetAppendAtNs()

	var buf bytes.Buffer
	written, err := n.WriteTo(&buf, types.NeedleVersionV3)
	if err != nil {
		t.Fatalf("WriteTo failed: %v", err)
	}

	data := buf.Bytes()
	parsed := &Needle{}
	if err := parsed.ReadFrom(data, 0, types.Size(written), types.NeedleVersionV3); err != nil {
		t.Fatalf("ReadFrom failed: %v", err)
	}

	if parsed.DataSize != 0 {
		t.Errorf("Expected DataSize 0, got %d", parsed.DataSize)
	}

	if parsed.IsDeleted() != true {
		t.Error("Expected needle to be deleted (tombstone)")
	}
}

func TestNeedleTTLExpiry(t *testing.T) {
	n := &Needle{
		Cookie:   0x12345678,
		Id:       0x123456789ABCDEF0,
		DataSize: 4,
		Data:     []byte("test"),
	}
	n.SetTtl(types.TTL{Count: 1, Unit: types.TTLUnitMinute})
	n.SetAppendAtNs()

	// Test with future TTL (write time is in the future, so not expired)
	futureTime := uint64(9999999999) // Year 2286
	if n.IsExpired(futureTime) {
		t.Error("Expected needle to not be expired")
	}

	// Test with expired TTL (write time was long ago)
	if !n.IsExpired(1) {
		t.Error("Expected needle to be expired")
	}

	// Test with no TTL
	n2 := &Needle{
		Cookie:   0x12345678,
		Id:       0x123456789ABCDEF0,
		DataSize: 4,
		Data:     []byte("test"),
	}
	n2.SetAppendAtNs()

	if n2.IsExpired(futureTime) {
		t.Error("Expected needle with no TTL to not be expired")
	}
}

func TestNeedleAlignPadding(t *testing.T) {
	tests := []struct {
		n        int
		expected int
	}{
		{0, 0},
		{1, 7},
		{2, 6},
		{3, 5},
		{4, 4},
		{5, 3},
		{6, 2},
		{7, 1},
		{8, 0},
		{9, 7},
		{16, 0},
		{17, 7},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			if got := NeedleAlignPadding(tt.n); got != tt.expected {
				t.Errorf("NeedleAlignPadding(%d) = %d, want %d", tt.n, got, tt.expected)
			}
		})
	}
}

func TestNeedleBodySize(t *testing.T) {
	n := &Needle{
		DataSize: 100,
		Data:     make([]byte, 100),
		Flags:    0,
	}

	// No optional fields: 4 (DataSize field) + 100 (Data) + 1 (Flags)
	if n.BodySize() != 105 {
		t.Errorf("Expected BodySize 105, got %d", n.BodySize())
	}

	// Add Name: 4 + 100 + 1 + 1 + 8 = 114
	n.SetName("test.txt")
	expectedSize := uint32(4 + 100 + 1 + 1 + 8) // DataSize field + Data + Flags + NameSize + Name
	if n.BodySize() != expectedSize {
		t.Errorf("Expected BodySize %d, got %d", expectedSize, n.BodySize())
	}

	// Add Mime: 4 + 100 + 1 + 1 + 8 + 1 + 10 = 125
	n.SetMime("text/plain")
	expectedSize = uint32(4 + 100 + 1 + 1 + 8 + 1 + 10) // + MimeSize + Mime
	if n.BodySize() != expectedSize {
		t.Errorf("Expected BodySize %d, got %d", expectedSize, n.BodySize())
	}
}

func TestNeedleFlagMethods(t *testing.T) {
	n := &Needle{
		Cookie:   0x12345678,
		Id:       0x123456789ABCDEF0,
		DataSize: 4,
		Data:     []byte("test"),
		Flags:    0,
	}

	if n.HasName() || n.HasMime() || n.HasLastModified() || n.HasTtl() || n.HasPairs() {
		t.Error("Expected no flags set")
	}

	n.SetName("test.txt")
	if !n.HasName() {
		t.Error("Expected HasName to return true")
	}

	n.SetMime("text/plain")
	if !n.HasMime() {
		t.Error("Expected HasMime to return true")
	}

	n.SetLastModified(1234567890)
	if !n.HasLastModified() {
		t.Error("Expected HasLastModified to return true")
	}

	n.SetTtl(types.TTL{Count: 1, Unit: types.TTLUnitDay})
	if !n.HasTtl() {
		t.Error("Expected HasTtl to return true")
	}

	n.SetPairs(`{"key":"value"}`)
	if !n.HasPairs() {
		t.Error("Expected HasPairs to return true")
	}

	// Check all flags are set
	expectedFlags := FlagHasName | FlagHasMime | FlagHasLastModified | FlagHasTtl | FlagHasPairs
	if n.Flags != expectedFlags {
		t.Errorf("Expected flags %d, got %d", expectedFlags, n.Flags)
	}
}

func TestNeedleGetters(t *testing.T) {
	n := &Needle{
		Cookie:   0x12345678,
		Id:       0x123456789ABCDEF0,
		DataSize: 4,
		Data:     []byte("test"),
	}
	n.SetName("test.txt")
	n.SetMime("text/plain")
	n.SetPairs(`{"key":"value"}`)

	if n.GetName() != "test.txt" {
		t.Errorf("GetName() = %s, want test.txt", n.GetName())
	}

	if n.GetMime() != "text/plain" {
		t.Errorf("GetMime() = %s, want text/plain", n.GetMime())
	}

	if n.GetPairs() != `{"key":"value"}` {
		t.Errorf("GetPairs() = %s, want {\"key\":\"value\"}", n.GetPairs())
	}

	// Test getter when flag not set
	n2 := &Needle{
		Cookie:   0x12345678,
		Id:       0x123456789ABCDEF0,
		DataSize: 4,
		Data:     []byte("test"),
		Flags:    0,
	}

	if n2.GetName() != "" {
		t.Errorf("GetName() without flag = %s, want empty", n2.GetName())
	}
}

func TestNeedleClone(t *testing.T) {
	original := &Needle{
		Cookie:      0x12345678,
		Id:          0x123456789ABCDEF0,
		DataSize:    4,
		Data:        []byte("test"),
		Flags:       0,
		LastModified: 1234567890,
	}
	original.SetName("test.txt")
	original.SetMime("text/plain")
	original.SetTtl(types.TTL{Count: 7, Unit: types.TTLUnitDay})
	original.SetAppendAtNs()

	clone := original.Clone()

	if !original.Equals(clone) {
		t.Error("Clone is not equal to original")
	}

	// Modify clone's data
	clone.Data[0] = 'X'

	if original.Equals(clone) {
		t.Error("Modifying clone should not affect original")
	}
}

func TestNeedleVersionConstants(t *testing.T) {
	if types.NeedleVersionV1 != 1 {
		t.Errorf("NeedleVersionV1 = %d, want 1", types.NeedleVersionV1)
	}
	if types.NeedleVersionV2 != 2 {
		t.Errorf("NeedleVersionV2 = %d, want 2", types.NeedleVersionV2)
	}
	if types.NeedleVersionV3 != 3 {
		t.Errorf("NeedleVersionV3 = %d, want 3", types.NeedleVersionV3)
	}
}

func TestNeedleSizeLimits(t *testing.T) {
	if MaxNeedleDataSize != 1<<32-1 {
		t.Errorf("MaxNeedleDataSize = %d, want %d", MaxNeedleDataSize, 1<<32-1)
	}
	if MaxNeedleNameSize != 255 {
		t.Errorf("MaxNeedleNameSize = %d, want 255", MaxNeedleNameSize)
	}
	if MaxNeedleMimeSize != 255 {
		t.Errorf("MaxNeedleMimeSize = %d, want 255", MaxNeedleMimeSize)
	}
	if MaxNeedlePairsSize != 65535 {
		t.Errorf("MaxNeedlePairsSize = %d, want 65535", MaxNeedlePairsSize)
	}
}

func TestWriteUint40BigEndian(t *testing.T) {
	tests := []struct {
		input    uint64
		expected []byte
	}{
		{0x0000000000, []byte{0x00, 0x00, 0x00, 0x00, 0x00}},
		{0x0000000001, []byte{0x00, 0x00, 0x00, 0x00, 0x01}},
		{0x0000000100, []byte{0x00, 0x00, 0x00, 0x01, 0x00}},
		{0x0000001000, []byte{0x00, 0x00, 0x00, 0x10, 0x00}},
		{0x0000010000, []byte{0x00, 0x00, 0x01, 0x00, 0x00}},
		{0x0000100000, []byte{0x00, 0x00, 0x10, 0x00, 0x00}},
		{0x0001000000, []byte{0x00, 0x01, 0x00, 0x00, 0x00}},
		{0x0010000000, []byte{0x00, 0x10, 0x00, 0x00, 0x00}},
		{0x0100000000, []byte{0x01, 0x00, 0x00, 0x00, 0x00}},
		{0x0123456789, []byte{0x01, 0x23, 0x45, 0x67, 0x89}},
		{0xFFFFFFFFF, []byte{0x0F, 0xFF, 0xFF, 0xFF, 0xFF}}, // Max 40-bit value
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			buf := make([]byte, 5)
			writeUint40BigEndian(buf, tt.input)

			if !bytes.Equal(buf, tt.expected) {
				t.Errorf("writeUint40BigEndian(%d) = %v, want %v", tt.input, buf, tt.expected)
			}
		})
	}
}

func TestReadUint40BigEndian(t *testing.T) {
	tests := []struct {
		input    []byte
		expected uint64
	}{
		{[]byte{0x00, 0x00, 0x00, 0x00, 0x00}, 0x0000000000},
		{[]byte{0x00, 0x00, 0x00, 0x00, 0x01}, 0x0000000001},
		{[]byte{0x00, 0x00, 0x00, 0x01, 0x00}, 0x0000000100},
		{[]byte{0x00, 0x00, 0x00, 0x10, 0x00}, 0x0000001000},
		{[]byte{0x00, 0x00, 0x01, 0x00, 0x00}, 0x0000010000},
		{[]byte{0x00, 0x00, 0x10, 0x00, 0x00}, 0x0000100000},
		{[]byte{0x00, 0x01, 0x00, 0x00, 0x00}, 0x0001000000},
		{[]byte{0x00, 0x10, 0x00, 0x00, 0x00}, 0x0010000000},
		{[]byte{0x01, 0x00, 0x00, 0x00, 0x00}, 0x0100000000},
		{[]byte{0x01, 0x23, 0x45, 0x67, 0x89}, 0x0123456789},
		{[]byte{0x0F, 0xFF, 0xFF, 0xFF, 0xFF}, 0xFFFFFFFFF},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			got := readUint40BigEndian(tt.input)
			if got != tt.expected {
				t.Errorf("readUint40BigEndian(%v) = %d, want %d", tt.input, got, tt.expected)
			}
		})
	}
}

func TestNeedleDataLen(t *testing.T) {
	n := &Needle{
		DataSize: 42,
		Data:     make([]byte, 42),
	}

	if n.DataLen() != 42 {
		t.Errorf("DataLen() = %d, want 42", n.DataLen())
	}
}

func TestNeedleSetCookie(t *testing.T) {
	n := &Needle{}
	n.SetCookie(0xABCDEF01)

	if n.Cookie != 0xABCDEF01 {
		t.Errorf("Cookie = %d, want 0xABCDEF01", n.Cookie)
	}

	if n.GetCookie() != 0xABCDEF01 {
		t.Errorf("GetCookie() = %d, want 0xABCDEF01", n.GetCookie())
	}
}
