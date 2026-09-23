// Package wire defines the self-describing compressed byte stream format.
package wire

import "errors"

const (
	Magic   uint64 = 0x4F4E
	Version uint64 = 1

	TagLiteral = 1
	TagMatch   = 2
	TagFlush   = 3
	TagEnd     = 4

	MinMatch = 3
)

// Errors returned by the varint reader; decoders map them to stream errors.
var (
	ErrVarint = errors.New("wire: varint longer than 10 bytes or overflow")
	ErrShort  = errors.New("wire: truncated record")
)

// AppendUvarint appends x as a little-endian base128 varint.
func AppendUvarint(b []byte, x uint64) []byte {
	for x >= 0x80 {
		b = append(b, byte(x)|0x80)
		x >>= 7
	}
	return append(b, byte(x))
}

// ReadUvarint consumes one varint from the front of b.
// It returns the value, the remaining bytes, and ErrShort/ErrVarint on error.
func ReadUvarint(b []byte) (uint64, []byte, error) {
	var x uint64
	for i := 0; i < 10; i++ {
		if i >= len(b) {
			return 0, b, ErrShort
		}
		c := b[i]
		if i == 9 && c > 1 {
			return 0, b, ErrVarint
		}
		x |= uint64(c&0x7F) << (7 * i)
		if c < 0x80 {
			return x, b[i+1:], nil
		}
	}
	return 0, b, ErrVarint
}

// Header builds the stream header.
func Header(windowCap uint64) []byte {
	b := AppendUvarint(nil, Magic)
	b = AppendUvarint(b, Version)
	return AppendUvarint(b, windowCap)
}

// Literal builds a literal-length record; the n raw bytes follow directly.
func Literal(n uint64) []byte {
	b := AppendUvarint(nil, TagLiteral)
	return AppendUvarint(b, n)
}

// Match builds a back-reference record (dist>=1, length>=3).
func Match(dist, length uint64) []byte {
	b := AppendUvarint(nil, TagMatch)
	b = AppendUvarint(b, dist)
	return AppendUvarint(b, length)
}

// Flush builds the flush marker.
func Flush() []byte { return AppendUvarint(nil, TagFlush) }

// End builds the stream trailer.
func End(origLen, crc uint64) []byte {
	b := AppendUvarint(nil, TagEnd)
	b = AppendUvarint(b, origLen)
	return AppendUvarint(b, crc)
}
