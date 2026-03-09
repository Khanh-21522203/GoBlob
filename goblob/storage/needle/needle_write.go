package needle

import (
	"encoding/binary"
	"fmt"
	"io"

	"GoBlob/goblob/core/types"
)

// WriteTo writes the needle to the writer in the exact binary format.
// Returns the total bytes written (including header, body, footer, and padding).
func (n *Needle) WriteTo(w io.Writer, version types.NeedleVersion) (int64, error) {
	// Step 1: Build body bytes
	body := n.buildBodyBytes()

	// Step 2: Compute CRC32 over cookie + id + body
	checksum := NewCRC32(n.Cookie, n.Id, body)
	n.Checksum = checksum

	// Step 3: Build footer
	footerSize := 4 // Checksum
	if version >= types.NeedleVersionV3 {
		footerSize += 8 // AppendAtNs
	}
	footer := n.buildFooterBytes(version)

	// Step 4: BodySize = len(body) + len(footer)
	bodySize := uint32(len(body) + len(footer))

	// Step 5: Write header (16 bytes)
	header := make([]byte, 16)
	binary.BigEndian.PutUint32(header[0:4], uint32(n.Cookie))
	binary.BigEndian.PutUint64(header[4:12], uint64(n.Id))
	binary.BigEndian.PutUint32(header[12:16], bodySize)

	if _, err := w.Write(header); err != nil {
		return 0, fmt.Errorf("failed to write header: %w", err)
	}

	// Step 6: Write body bytes
	if _, err := w.Write(body); err != nil {
		return 0, fmt.Errorf("failed to write body: %w", err)
	}

	// Step 7: Write footer bytes
	if _, err := w.Write(footer); err != nil {
		return 0, fmt.Errorf("failed to write footer: %w", err)
	}

	totalSize := int64(len(header) + len(body) + len(footer))

	// Step 8: Write padding
	padding := NeedleAlignPadding(int(totalSize))
	if padding > 0 {
		paddingBytes := make([]byte, padding)
		if _, err := w.Write(paddingBytes); err != nil {
			return 0, fmt.Errorf("failed to write padding: %w", err)
		}
		totalSize += int64(padding)
	}

	// Update Size to total on-disk size
	n.Size = types.Size(totalSize)

	return totalSize, nil
}

// buildBodyBytes builds the body bytes of the needle.
func (n *Needle) buildBodyBytes() []byte {
	// Calculate total body size
	// Body layout: DataSize(4) + Data(n.DataSize) + Flags(1) + optional fields
	bodySize := uint32(4) + n.DataSize + 1 // DataSize field + Data + Flags

	if n.HasName() {
		bodySize += 1 + uint32(len(n.Name)) // NameSize + Name
	}
	if n.HasMime() {
		bodySize += 1 + uint32(len(n.Mime)) // MimeSize + Mime
	}
	if n.HasPairs() {
		bodySize += 2 + uint32(len(n.Pairs)) // PairsSize + Pairs
	}
	if n.HasLastModified() {
		bodySize += 5 // LastMod (5 bytes)
	}
	if n.HasTtl() {
		bodySize += 2 // Ttl (2 bytes)
	}

	body := make([]byte, bodySize)
	offset := 0

	// DataSize (4 bytes, big-endian)
	binary.BigEndian.PutUint32(body[offset:offset+4], n.DataSize)
	offset += 4

	// Data
	copy(body[offset:offset+int(n.DataSize)], n.Data)
	offset += int(n.DataSize)

	// Flags
	body[offset] = n.Flags
	offset++

	// Name (optional)
	if n.HasName() {
		body[offset] = byte(len(n.Name))
		offset++
		copy(body[offset:offset+len(n.Name)], n.Name)
		offset += len(n.Name)
	}

	// Mime (optional)
	if n.HasMime() {
		body[offset] = byte(len(n.Mime))
		offset++
		copy(body[offset:offset+len(n.Mime)], n.Mime)
		offset += len(n.Mime)
	}

	// Pairs (optional)
	if n.HasPairs() {
		pairsSize := uint16(len(n.Pairs))
		binary.BigEndian.PutUint16(body[offset:offset+2], pairsSize)
		offset += 2
		copy(body[offset:offset+len(n.Pairs)], n.Pairs)
		offset += len(n.Pairs)
	}

	// LastModified (optional, 5 bytes big-endian)
	if n.HasLastModified() {
		writeUint40BigEndian(body[offset:offset+5], n.LastModified)
		offset += 5
	}

	// Ttl (optional, 2 bytes)
	if n.HasTtl() {
		ttlBytes := n.Ttl.Bytes()
		copy(body[offset:offset+2], ttlBytes[:])
		offset += 2
	}

	return body
}

// buildFooterBytes builds the footer bytes of the needle.
func (n *Needle) buildFooterBytes(version types.NeedleVersion) []byte {
	var footerSize int
	if version >= types.NeedleVersionV3 {
		footerSize = 12 // Checksum + AppendAtNs
	} else {
		footerSize = 4 // Checksum only
	}

	footer := make([]byte, footerSize)
	offset := 0

	// Checksum (4 bytes, big-endian)
	binary.BigEndian.PutUint32(footer[offset:offset+4], uint32(n.Checksum))
	offset += 4

	// AppendAtNs (v3 only, 8 bytes, big-endian)
	if version >= types.NeedleVersionV3 {
		binary.BigEndian.PutUint64(footer[offset:offset+8], n.AppendAtNs)
	}

	return footer
}

// writeUint40BigEndian writes a 40-bit unsigned integer as 5 big-endian bytes.
func writeUint40BigEndian(buf []byte, v uint64) {
	buf[0] = byte((v >> 32) & 0xFF)
	buf[1] = byte((v >> 24) & 0xFF)
	buf[2] = byte((v >> 16) & 0xFF)
	buf[3] = byte((v >> 8) & 0xFF)
	buf[4] = byte(v & 0xFF)
}

// NeedleAlignPadding calculates the padding needed to align to 8-byte boundary.
func NeedleAlignPadding(n int) int {
	rem := n % int(types.NeedleAlignmentSize)
	if rem == 0 {
		return 0
	}
	return int(types.NeedleAlignmentSize) - rem
}

// bodyBytes reconstructs the body bytes for checksum verification.
func (n *Needle) bodyBytes() []byte {
	body := make([]byte, 0, n.BodySize())

	// DataSize
	buf := make([]byte, 4)
	binary.BigEndian.PutUint32(buf, n.DataSize)
	body = append(body, buf...)

	// Data
	body = append(body, n.Data...)

	// Flags
	body = append(body, n.Flags)

	// Name (optional)
	if n.HasName() {
		body = append(body, byte(len(n.Name)))
		body = append(body, n.Name...)
	}

	// Mime (optional)
	if n.HasMime() {
		body = append(body, byte(len(n.Mime)))
		body = append(body, n.Mime...)
	}

	// Pairs (optional)
	if n.HasPairs() {
		buf := make([]byte, 2)
		binary.BigEndian.PutUint16(buf, uint16(len(n.Pairs)))
		body = append(body, buf...)
		body = append(body, n.Pairs...)
	}

	// LastModified (optional)
	if n.HasLastModified() {
		buf := make([]byte, 5)
		writeUint40BigEndian(buf, n.LastModified)
		body = append(body, buf...)
	}

	// Ttl (optional)
	if n.HasTtl() {
		ttlBytes := n.Ttl.Bytes()
		body = append(body, ttlBytes[:]...)
	}

	return body
}
