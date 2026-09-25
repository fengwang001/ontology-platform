// Package wire defines the compressed stream byte format.
package wire

import "errors"

// Header is the fixed stream prefix.
var Header = []byte{'L', 'Z', 1}

// Record tags.
const (
	TagLiteral = 0
	TagMatch   = 1
	TagFlush   = 2
	TagEnd     = 3
)

// MaxVarintLen is the maximal legal length of a Uvarint encoding.
const MaxVarintLen = 10

// ErrVarintTooLong indicates a varint exceeding 10 bytes or overflowing 64 bits.
var ErrVarintTooLong = errors.New("wire: varint too long or overflow")

// PutUvarint appends an unsigned LEB128 value to b.
func PutUvarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

// ReadUvarint consumes one Uvarint from the front of buf.
// It returns the value, the remaining bytes, and whether a complete varint was
// available. ErrVarintTooLong is returned for an overlong/overflowing encoding.
func ReadUvarint(buf []byte) (uint64, []byte, error) {
	var x uint64
	var s uint
	for i := 0; i < MaxVarintLen && i < len(buf); i++ {
		b := buf[i]
		if b < 0x80 {
			if i == MaxVarintLen-1 && (b > 1 || s != 63) {
				return 0, nil, ErrVarintTooLong
			}
			return x | uint64(b)<<s, buf[i+1:], nil
		}
		if i == MaxVarintLen-1 {
			return 0, nil, ErrVarintTooLong
		}
		x |= uint64(b&0x7f) << s
		s += 7
	}
	return 0, nil, nil // incomplete; caller must retain the bytes
}

// Literal appends a literal record.
func Literal(b, p []byte) []byte {
	b = append(b, TagLiteral)
	b = PutUvarint(b, uint64(len(p)))
	return append(b, p...)
}

// Match appends a back-reference record (distance and length, both >= 1).
func Match(b []byte, distance, length int) []byte {
	b = append(b, TagMatch)
	b = PutUvarint(b, uint64(distance))
	b = PutUvarint(b, uint64(length))
	return b
}

// Flush appends a flush marker.
func Flush(b []byte) []byte { return append(b, TagFlush) }

// End appends the stream trailer.
func End(b []byte, origLen uint64, checksum uint32) []byte {
	b = append(b, TagEnd)
	b = PutUvarint(b, origLen)
	return PutUvarint(b, uint64(checksum))
}
