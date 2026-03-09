package needle

import (
	"encoding/binary"
	"hash/crc32"

	"GoBlob/goblob/core/types"
)

// NewCRC32 calculates CRC32 checksum over cookie, needle ID, and body bytes.
func NewCRC32(cookie types.Cookie, id types.NeedleId, body []byte) CRC32 {
	h := crc32.NewIEEE()
	var buf [12]byte
	binary.BigEndian.PutUint32(buf[0:4], uint32(cookie))
	binary.BigEndian.PutUint64(buf[4:12], uint64(id))
	h.Write(buf[:])
	h.Write(body)
	return CRC32(h.Sum32())
}
