package needle

import (
	"GoBlob/goblob/core/types"
)

// Needle represents a blob stored in a volume file.
// The binary layout is critical for data integrity.
type Needle struct {
	// Identity
	Cookie types.Cookie
	Id     types.NeedleId
	Size   types.Size // total on-disk size including header+body+footer+padding

	// Data
	DataSize uint32
	Data     []byte

	// Optional metadata (presence controlled by Flags byte)
	Flags        uint8
	Name         []byte // max 255 bytes
	Mime         []byte // max 255 bytes
	Pairs        []byte // JSON key=value, max 64KB
	LastModified uint64 // unix seconds, stored as 5-byte uint40
	Ttl          types.TTL

	// Footer
	Checksum   CRC32
	AppendAtNs uint64 // v3 only: nanoseconds since epoch
}

// Flag bitmask constants for the Flags byte
const (
	FlagHasName         uint8 = 0x01
	FlagHasMime         uint8 = 0x02
	FlagHasLastModified uint8 = 0x04
	FlagHasTtl          uint8 = 0x08
	FlagHasPairs        uint8 = 0x10
	FlagIsCompressed    uint8 = 0x20
)

// Size limits
const (
	MaxNeedleDataSize  = 1<<32 - 1 // 4GB
	MaxNeedleNameSize  = 255
	MaxNeedleMimeSize  = 255
	MaxNeedlePairsSize = 65535
)

type CRC32 uint32

// IsCompressed returns true if the needle data is compressed.
func (n *Needle) IsCompressed() bool {
	return n.Flags&FlagIsCompressed != 0
}

// HasName returns true if the needle has a name field.
func (n *Needle) HasName() bool {
	return n.Flags&FlagHasName != 0
}

// HasMime returns true if the needle has a mime type field.
func (n *Needle) HasMime() bool {
	return n.Flags&FlagHasMime != 0
}

// HasLastModified returns true if the needle has a last modified field.
func (n *Needle) HasLastModified() bool {
	return n.Flags&FlagHasLastModified != 0
}

// HasTtl returns true if the needle has a TTL field.
func (n *Needle) HasTtl() bool {
	return n.Flags&FlagHasTtl != 0
}

// HasPairs returns true if the needle has a pairs field.
func (n *Needle) HasPairs() bool {
	return n.Flags&FlagHasPairs != 0
}

// SetCompressed marks the needle as compressed.
func (n *Needle) SetCompressed() {
	n.Flags |= FlagIsCompressed
}

// SetName sets the name and enables the name flag.
func (n *Needle) SetName(name string) {
	if len(name) > MaxNeedleNameSize {
		name = name[:MaxNeedleNameSize]
	}
	n.Name = []byte(name)
	n.Flags |= FlagHasName
}

// SetMime sets the mime type and enables the mime flag.
func (n *Needle) SetMime(mime string) {
	if len(mime) > MaxNeedleMimeSize {
		mime = mime[:MaxNeedleMimeSize]
	}
	n.Mime = []byte(mime)
	n.Flags |= FlagHasMime
}

// SetLastModified sets the last modified time and enables the flag.
func (n *Needle) SetLastModified(ts uint64) {
	n.LastModified = ts
	n.Flags |= FlagHasLastModified
}

// SetTtl sets the TTL and enables the TTL flag.
func (n *Needle) SetTtl(ttl types.TTL) {
	n.Ttl = ttl
	n.Flags |= FlagHasTtl
}

// SetPairs sets the pairs and enables the pairs flag.
func (n *Needle) SetPairs(pairs string) {
	if len(pairs) > MaxNeedlePairsSize {
		pairs = pairs[:MaxNeedlePairsSize]
	}
	n.Pairs = []byte(pairs)
	n.Flags |= FlagHasPairs
}

// GetLastModified returns the last modified timestamp.
// For v1/v2 needles without the field, returns 0.
func (n *Needle) GetLastModified() uint64 {
	if n.HasLastModified() {
		return n.LastModified
	}
	return 0
}

// GetMime returns the mime type as a string.
func (n *Needle) GetMime() string {
	if n.HasMime() {
		return string(n.Mime)
	}
	return ""
}

// GetName returns the name as a string.
func (n *Needle) GetName() string {
	if n.HasName() {
		return string(n.Name)
	}
	return ""
}

// GetPairs returns the pairs as a string.
func (n *Needle) GetPairs() string {
	if n.HasPairs() {
		return string(n.Pairs)
	}
	return ""
}

// DataLen returns the actual data length (for v1 compatibility).
func (n *Needle) DataLen() uint32 {
	return n.DataSize
}

// BodySize returns the size of the body (DataSize field + Data + flags + optional fields).
func (n *Needle) BodySize() uint32 {
	size := uint32(4) + n.DataSize + 1 // DataSize field + Data + Flags

	if n.HasName() {
		size += 1 + uint32(len(n.Name)) // NameSize + Name
	}
	if n.HasMime() {
		size += 1 + uint32(len(n.Mime)) // MimeSize + Mime
	}
	if n.HasPairs() {
		size += 2 + uint32(len(n.Pairs)) // PairsSize + Pairs
	}
	if n.HasLastModified() {
		size += 5 // LastMod is 5 bytes
	}
	if n.HasTtl() {
		size += 2 // Ttl is 2 bytes
	}

	return size
}

// IsDeleted returns true if the needle represents a deletion (tombstone).
func (n *Needle) IsDeleted() bool {
	return n.DataSize == 0
}

// GetCookie returns the cookie value.
func (n *Needle) GetCookie() types.Cookie {
	return n.Cookie
}

// SetCookie sets the cookie value.
func (n *Needle) SetCookie(cookie types.Cookie) {
	n.Cookie = cookie
}
