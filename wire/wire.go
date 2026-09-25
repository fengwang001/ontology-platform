// Package wire defines the self-describing LZ77 byte stream format.
package wire

import "errors"

// Record tags.
const (
	TagLit   = 0x00
	TagRef   = 0x01
	TagFlush = 0x02
	TagTail  = 0x04
)

// Magic is the 3-byte stream header, immediately followed by version 1.
var Header = []byte{'O', 'L', 'Z', 0x01}

// MaxVarintLen is the maximum legal length of an unsigned LEB128 integer (64 bits).
const MaxVarintLen = 10

// ErrVarint reports an overlong (>10 bytes) or 64-bit-overflowing varint.
var ErrVarint = errors.New("wire: varint overlong or overflow")

// AppendUvarint encodes x using unsigned LEB128.
func AppendUvarint(dst []byte, x uint64) []byte {
	for x >= 0x80 {
		dst = append(dst, byte(x)|0x80)
		x >>= 7
	}
	return append(dst, byte(x))
}

// ReadUvarint decodes one unsigned LEB128 integer from b starting at off.
// It returns the value and the offset just past it. A malformed encoding
// yields ErrVarint; running past the end yields io.ErrUnexpectedEOF-like
// behavior via the truncated flag so callers can retry with more bytes.
func ReadUvarint(b []byte, off int) (uint64, int, bool, error) {
	var x uint64
	for i := 0; i < MaxVarintLen; i++ {
		if off >= len(b) {
			return 0, off, false, nil
		}
		c := b[off]
		off++
		if i == MaxVarintLen-1 && c > 1 {
			return 0, off, true, ErrVarint
		}
		x |= uint64(c&0x7f) << (7 * i)
		if c < 0x80 {
			return x, off, true, nil
		}
	}
	return 0, off, true, ErrVarint
}

// PutLit appends a literal run record.
func PutLit(dst, lit []byte) []byte {
	dst = append(dst, TagLit)
	dst = AppendUvarint(dst, uint64(len(lit)))
	return append(dst, lit...)
}

// PutRef appends a back-reference record (1-based distance, length).
func PutRef(dst []byte, dist, length int) []byte {
	dst = append(dst, TagRef)
	dst = AppendUvarint(dst, uint64(dist))
	dst = AppendUvarint(dst, uint64(length))
	return dst
}

// PutFlush appends a flush marker.
func PutFlush(dst []byte) []byte { return append(dst, TagFlush) }

// PutTail appends the stream trailer with original length and checksum.
func PutTail(dst []byte, origLen uint64, crc uint32) []byte {
	dst = append(dst, TagTail)
	dst = AppendUvarint(dst, origLen)
	return AppendUvarint(dst, uint64(crc))
}
