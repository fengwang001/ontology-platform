// Package wire defines the LZ77O compressed-stream byte format.
package wire

import "errors"

// Magic and Version prefix every stream.
const (
	Magic   = "LZ77O"
	Version = byte(1)
)

// Record tags.
const (
	TagLiteral = 0
	TagMatch   = 1
	TagFlush   = 2
	TagEnd     = 3
)

// ErrVarintOverflow means a varint exceeded 10 bytes or 64 bits.
var ErrVarintOverflow = errors.New("wire: varint overflow")

// MaxVarintLen is the maximum encoded length of a uint64.
const MaxVarintLen = 10

// PutVarint appends an unsigned LEB128 encoding of v to dst.
func PutVarint(dst []byte, v uint64) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

// ReadVarint decodes one unsigned LEB128 from b.
// It returns the value, the number of bytes consumed (0 when incomplete),
// and ErrVarintOverflow on a >10 byte / >64 bit encoding.
func ReadVarint(b []byte) (uint64, int, error) {
	var v uint64
	for i := 0; i < MaxVarintLen; i++ {
		if i >= len(b) {
			return 0, 0, nil
		}
		c := b[i]
		if i == MaxVarintLen-1 && c > 1 {
			return 0, 0, ErrVarintOverflow
		}
		v |= uint64(c&0x7f) << (7 * i)
		if c < 0x80 {
			return v, i + 1, nil
		}
}
	return 0, 0, ErrVarintOverflow
}

// FNV-1a 64-bit checksum, implemented locally to avoid external hashes.
const (
	FNVOffset64 = 14695981039346656037
	FNVPrime64  = 1099511628211
)

// Checksum returns the FNV-1a 64 digest of data.
func Checksum(data []byte) uint64 {
	h := uint64(FNVOffset64)
	for _, c := range data {
		h ^= uint64(c)
		h *= FNVPrime64
	}
	return h
}
