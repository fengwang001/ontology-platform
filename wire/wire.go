// Package wire defines the self-describing byte format of the compressed stream.
package wire

import "errors"

// Record tags: low two bits of the tag byte.
const (
	TagLiteral byte = 0x00 // length<<2 | 00, then raw bytes
	TagFlush   byte = 0x01 // no payload
	TagMatch   byte = 0x02 // then distance varint, length varint
	TagEnd     byte = 0x03 // then totalLength varint, checksum varint
)

const (
	// Magic is the fixed 3-byte stream header.
	Magic   = "LZ"
	Version = byte(1)
)

// ErrVarintOverflow means a varint exceeded 10 bytes or did not fit in uint64.
var ErrVarintOverflow = errors.New("wire: varint overflow")

// Header writes the stream header into dst.
func Header(dst []byte) []byte {
	return append(dst, Magic[0], Magic[1], Version)
}

// PutUvarint appends an unsigned LEB128 integer.
func PutUvarint(dst []byte, v uint64) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

// ReadUvarint consumes one varint from src. It returns the value, the remaining
// bytes and ErrVarintOverflow on a malformed/overflowing encoding. A lack of
// bytes is reported by ok=false (the caller decides whether the stream is
// truncated).
func ReadUvarint(src []byte) (v uint64, rest []byte, ok bool, err error) {
	var x uint64
	for i := 0; i < 10; i++ {
		if i >= len(src) {
			return 0, src, false, nil
		}
		b := src[i]
		if i == 9 && b > 1 { // 64 bits: only the single stop bit may remain.
			return 0, src, false, ErrVarintOverflow
		}
		x |= uint64(b&0x7f) << (7 * i)
		if b < 0x80 {
			return x, src[i+1:], true, nil
		}
	}
	return 0, src, false, ErrVarintOverflow
}

// Literal appends a literal run to dst.
func Literal(dst []byte, p []byte) []byte {
	dst = PutUvarint(dst, uint64(len(p))<<2|uint64(TagLiteral))
	return append(dst, p...)
}

// Match appends a back-reference (distance is 1-based, length >= 3).
func Match(dst []byte, distance, length int) []byte {
	dst = PutUvarint(dst, uint64(TagMatch))
	dst = PutUvarint(dst, uint64(distance))
	return PutUvarint(dst, uint64(length))
}

// Flush appends a flush marker.
func Flush(dst []byte) []byte { return append(dst, TagFlush) }

// End appends the stream trailer.
func End(dst []byte, totalLength int, checksum uint64) []byte {
	dst = PutUvarint(dst, uint64(TagEnd))
	dst = PutUvarint(dst, uint64(totalLength))
	return PutUvarint(dst, checksum)
}

// FNV64 is the 64-bit FNV-1a checksum used by the trailer.
func FNV64(data []byte) uint64 {
	const offset, prime = uint64(14695981039346656037), uint64(1099511628211)
	h := offset
	for _, b := range data {
		h ^= uint64(b)
		h *= prime
	}
	return h
}
