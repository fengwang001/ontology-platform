// Package wire defines the byte-level format of the LZ77 stream.
package wire

import "errors"

// Tag bytes of the four record kinds.
const (
	TagLiteral byte = 0x00
	TagMatch   byte = 0x01
	TagFlush   byte = 0x02
	TagEnd     byte = 0x03
)

// Header is the fixed stream header: magic "LZ7" plus format version 1.
var Header = [5]byte{'L', 'Z', '7', 0x01, 0x00}

// MaxVarintLen is the maximum encoded length of a 64-bit unsigned varint.
const MaxVarintLen = 10

var (
	// ErrVarintTooLong reports a varint using more than 10 bytes.
	ErrVarintTooLong = errors.New("wire: varint longer than 10 bytes")
	// ErrVarintOverflow reports a varint that overflows uint64.
	ErrVarintOverflow = errors.New("wire: varint overflow")
)

// AppendUvarint appends x to b in LEB128 unsigned encoding.
func AppendUvarint(b []byte, x uint64) []byte {
	for x >= 0x80 {
		b = append(b, byte(x)|0x80)
		x >>= 7
	}
	return append(b, byte(x))
}

// ReadUvarint reads one varint from the front of p. It returns the value, the
// number of bytes consumed and ok=false when more input is needed. A varint of
// more than MaxVarintLen bytes or one overflowing uint64 is a hard error.
func ReadUvarint(p []byte) (uint64, int, bool, error) {
	var x uint64
	for i := 0; i < len(p) && i < MaxVarintLen; i++ {
		c := p[i]
		if i == MaxVarintLen-1 {
			if c&0x80 != 0 {
				return 0, i + 1, true, ErrVarintTooLong
			}
			if c > 1 { // only the lowest payload bit can remain.
				return 0, i + 1, true, ErrVarintOverflow
			}
		}
		x |= uint64(c&0x7f) << (7 * i)
		if c < 0x80 {
			return x, i + 1, true, nil
		}
	}
	if len(p) >= MaxVarintLen {
		return 0, MaxVarintLen, true, ErrVarintTooLong
	}
	return 0, 0, false, nil
}

// AppendLiteral emits a literal-length record followed by the bytes.
func AppendLiteral(b, lit []byte) []byte {
	b = append(b, TagLiteral)
	b = AppendUvarint(b, uint64(len(lit)))
	return append(b, lit...)
}

// AppendMatch emits a distance/length back-reference record.
func AppendMatch(b []byte, dist, length int) []byte {
	b = append(b, TagMatch)
	b = AppendUvarint(b, uint64(dist))
	return AppendUvarint(b, uint64(length))
}

// AppendFlush emits a flush marker.
func AppendFlush(b []byte) []byte { return append(b, TagFlush) }

// AppendEnd emits the trailer: original length and CRC32 checksum.
func AppendEnd(b []byte, size, checksum uint64) []byte {
	b = append(b, TagEnd)
	b = AppendUvarint(b, size)
	return AppendUvarint(b, checksum)
}
