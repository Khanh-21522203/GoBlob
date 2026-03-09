package needle

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"time"

	"GoBlob/goblob/core/types"
)

var (
	ErrNotFound      = fmt.Errorf("needle not found")
	ErrDeleted       = fmt.Errorf("needle deleted")
	ErrChecksumMismatch = fmt.Errorf("needle checksum mismatch")
	ErrCookieMismatch   = fmt.Errorf("needle cookie mismatch")
)

// ReadFrom parses a needle from raw bytes at the given offset.
// The size parameter is the encoded size from the index.
func (n *Needle) ReadFrom(data []byte, offset int64, size types.Size, version types.NeedleVersion) error {
	if len(data) == 0 {
		return nil
	}

	// Check if we have enough data for the header (16 bytes)
	if len(data) < 16 {
		return fmt.Errorf("not enough data for header: got %d bytes", len(data))
	}

	// Parse header
	buf := data[0:16]
	n.Cookie = types.Cookie(binary.BigEndian.Uint32(buf[0:4]))
	n.Id = types.NeedleId(binary.BigEndian.Uint64(buf[4:12]))
	bodySize := binary.BigEndian.Uint32(buf[12:16])

	// If BodySize == 0, this is a tombstone or empty needle
	if bodySize == 0 {
		n.DataSize = 0
		n.Size = 16 // Header only
		return nil
	}

	// Check if we have enough data for body + footer
	if uint32(len(data)) < 16+bodySize {
		return fmt.Errorf("not enough data for body+footer: got %d bytes, need %d", len(data), 16+bodySize)
	}

	// Parse body bytes
	bodyStart := 16
	bodyEnd := bodyStart + int(bodySize)

	// Minimum body size: DataSize(4) + Flags(1) = 5 bytes
	if bodyEnd-bodyStart < 5 {
		return fmt.Errorf("body too small: %d bytes", bodyEnd-bodyStart)
	}

	body := data[bodyStart:bodyEnd]
	pos := 0

	// DataSize
	n.DataSize = binary.BigEndian.Uint32(body[pos : pos+4])
	pos += 4

	// Data
	if int(n.DataSize) > len(body)-pos {
		return fmt.Errorf("DataSize exceeds body: %d > %d", n.DataSize, len(body)-pos)
	}
	n.Data = body[pos : pos+int(n.DataSize)]
	pos += int(n.DataSize)

	// Flags
	if pos >= len(body) {
		return fmt.Errorf("unexpected end of body at flags")
	}
	n.Flags = body[pos]
	pos++

	// Name (optional)
	if n.HasName() {
		if pos >= len(body) {
			return fmt.Errorf("unexpected end of body at name size")
		}
		nameSize := int(body[pos])
		pos++
		if pos+nameSize > len(body) {
			return fmt.Errorf("unexpected end of body at name data")
		}
		n.Name = body[pos : pos+nameSize]
		pos += nameSize
	}

	// Mime (optional)
	if n.HasMime() {
		if pos >= len(body) {
			return fmt.Errorf("unexpected end of body at mime size")
		}
		mimeSize := int(body[pos])
		pos++
		if pos+mimeSize > len(body) {
			return fmt.Errorf("unexpected end of body at mime data")
		}
		n.Mime = body[pos : pos+mimeSize]
		pos += mimeSize
	}

	// Pairs (optional)
	if n.HasPairs() {
		if pos+2 > len(body) {
			return fmt.Errorf("unexpected end of body at pairs size")
		}
		pairsSize := int(binary.BigEndian.Uint16(body[pos : pos+2]))
		pos += 2
		if pos+pairsSize > len(body) {
			return fmt.Errorf("unexpected end of body at pairs data")
		}
		n.Pairs = body[pos : pos+pairsSize]
		pos += pairsSize
	}

	// LastModified (optional)
	if n.HasLastModified() {
		if pos+5 > len(body) {
			return fmt.Errorf("unexpected end of body at last modified")
		}
		n.LastModified = readUint40BigEndian(body[pos : pos+5])
		pos += 5
	}

	// Ttl (optional)
	if n.HasTtl() {
		if pos+2 > len(body) {
			return fmt.Errorf("unexpected end of body at ttl")
		}
		count := uint8(body[pos])
		unit := types.TTLUnit(body[pos+1])
		n.Ttl = types.TTL{Count: count, Unit: unit}
		pos += 2
	}

	// Footer size depends on version
	footerSize := 4
	if version >= types.NeedleVersionV3 {
		footerSize = 12
	}

	// Parse footer - it starts at current pos within the body slice
	footerStart := pos
	footerEnd := footerStart + footerSize

	if footerEnd > len(body) {
		return fmt.Errorf("body too small for footer: need %d bytes, have %d", footerEnd, len(body))
	}

	footer := body[footerStart:footerEnd]
	pos = 0

	// Checksum
	n.Checksum = CRC32(binary.BigEndian.Uint32(footer[pos : pos+4]))
	pos += 4

	// AppendAtNs (v3 only)
	if version >= types.NeedleVersionV3 {
		n.AppendAtNs = binary.BigEndian.Uint64(footer[pos : pos+8])
	}

	// Verify checksum
	if err := n.VerifyChecksum(); err != nil {
		return err
	}

	// Calculate total size including padding
	// bodySize already includes the footer bytes, so don't add footerSize again
	totalSize := 16 + int(bodySize)
	padding := NeedleAlignPadding(totalSize)
	n.Size = types.Size(totalSize + padding)

	return nil
}

// readUint40BigEndian reads a 40-bit unsigned integer from 5 big-endian bytes.
func readUint40BigEndian(buf []byte) uint64 {
	return uint64(buf[0])<<32 |
		uint64(buf[1])<<24 |
		uint64(buf[2])<<16 |
		uint64(buf[3])<<8 |
		uint64(buf[4])
}

// VerifyChecksum recomputes the checksum and compares it with the stored value.
func (n *Needle) VerifyChecksum() error {
	computed := NewCRC32(n.Cookie, n.Id, n.bodyBytes())
	if computed != n.Checksum {
		return ErrChecksumMismatch
	}
	return nil
}

// IsExpired returns true if the needle's TTL has expired.
// writeTimeUnixSec is the unix timestamp when the needle was written.
func (n *Needle) IsExpired(writeTimeUnixSec uint64) bool {
	if !n.HasTtl() || n.Ttl.Count == 0 {
		return false
	}

	secondsPerUnit := map[types.TTLUnit]uint64{
		types.TTLUnitMinute: 60,
		types.TTLUnitHour:   3600,
		types.TTLUnitDay:    86400,
		types.TTLUnitWeek:   604800,
		types.TTLUnitMonth:  2592000,
		types.TTLUnitYear:   31536000,
	}

	unitSeconds, ok := secondsPerUnit[n.Ttl.Unit]
	if !ok {
		return false
	}

	expireAt := writeTimeUnixSec + uint64(n.Ttl.Count)*unitSeconds
	return uint64(time.Now().Unix()) > expireAt
}

// ReadFromReader reads a needle from an io.Reader at the given offset and size.
func ReadFromReader(r io.Reader, offset int64, size types.Size, version types.NeedleVersion) (*Needle, error) {
	data := make([]byte, size)
	_, err := io.ReadFull(r, data)
	if err != nil {
		return nil, fmt.Errorf("failed to read needle data: %w", err)
	}

	n := &Needle{}
	if err := n.ReadFrom(data, offset, size, version); err != nil {
		return nil, err
	}

	return n, nil
}

// ReadNeedleFromFile reads a needle from a file at the given offset and size.
func ReadNeedleFromFile(f *os.File, offset int64, size types.Size, version types.NeedleVersion) (*Needle, error) {
	data := make([]byte, size)
	_, err := f.ReadAt(data, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to read needle at offset %d: %w", offset, err)
	}

	n := &Needle{}
	if err := n.ReadFrom(data, offset, size, version); err != nil {
		return nil, err
	}

	return n, nil
}

// GetBytes returns the needle's data bytes.
func (n *Needle) GetBytes() []byte {
	return n.Data
}

// GetAppendAtNs returns the append timestamp in nanoseconds.
func (n *Needle) GetAppendAtNs() uint64 {
	return n.AppendAtNs
}

// SetAppendAtNs sets the append timestamp to the current time in nanoseconds.
func (n *Needle) SetAppendAtNs() {
	n.AppendAtNs = uint64(time.Now().UnixNano())
}

// Equals checks if two needles are equal (for testing).
func (n *Needle) Equals(other *Needle) bool {
	if n == nil && other == nil {
		return true
	}
	if n == nil || other == nil {
		return false
	}

	if n.Cookie != other.Cookie || n.Id != other.Id || n.DataSize != other.DataSize {
		return false
	}

	if !bytes.Equal(n.Data, other.Data) {
		return false
	}

	if n.Flags != other.Flags {
		return false
	}

	if n.HasName() && !bytes.Equal(n.Name, other.Name) {
		return false
	}

	if n.HasMime() && !bytes.Equal(n.Mime, other.Mime) {
		return false
	}

	if n.HasPairs() && !bytes.Equal(n.Pairs, other.Pairs) {
		return false
	}

	if n.HasLastModified() && n.LastModified != other.LastModified {
		return false
	}

	if n.HasTtl() && n.Ttl != other.Ttl {
		return false
	}

	// Compare CRC32
	if n.Checksum != other.Checksum {
		return false
	}

	return true
}

// Clone creates a deep copy of the needle.
func (n *Needle) Clone() *Needle {
	if n == nil {
		return nil
	}

	clone := &Needle{
		Cookie:      n.Cookie,
		Id:          n.Id,
		Size:        n.Size,
		DataSize:    n.DataSize,
		Flags:       n.Flags,
		Checksum:    n.Checksum,
		AppendAtNs:  n.AppendAtNs,
		LastModified: n.LastModified,
	}

	if n.Data != nil {
		clone.Data = make([]byte, len(n.Data))
		copy(clone.Data, n.Data)
	}

	if n.Name != nil {
		clone.Name = make([]byte, len(n.Name))
		copy(clone.Name, n.Name)
	}

	if n.Mime != nil {
		clone.Mime = make([]byte, len(n.Mime))
		copy(clone.Mime, n.Mime)
	}

	if n.Pairs != nil {
		clone.Pairs = make([]byte, len(n.Pairs))
		copy(clone.Pairs, n.Pairs)
	}

	clone.Ttl = n.Ttl

	return clone
}
