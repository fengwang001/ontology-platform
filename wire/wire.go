// Package wire defines the self-describing byte format of the LZ77 stream.
package wire

import "errors"

// Tags and fixed header bytes.
const (
	Magic0   byte = 0xF1
	Magic1   byte = 0xD7
	Version  byte = 0x01
	TagLit   byte = 0
	TagMatch byte = 1
	TagFlush byte = 2
	TagEnd   byte = 3
)

// ErrVarintOverflow reports a varint longer than 10 bytes or wider than 64 bits.
var ErrVarintOverflow = errors.New("wire: varint overflow")

// AppendHeader appends the stream header carrying the decoder window capacity.
func AppendHeader(dst []byte, windowCap uint64) []byte {
	dst = append(dst, Magic0, Magic1, Version)
	return AppendUvarint(dst, windowCap)
}

// AppendLit appends a literal-length record followed by its bytes.
func AppendLit(dst []byte, p []byte) []byte {
	dst = append(dst, TagLit)
	dst = AppendUvarint(dst, uint64(len(p)))
	return append(dst, p...)
}

// AppendMatch appends a back-reference. Distance and length must be positive.
func AppendMatch(dst []byte, dist, length uint64) []byte {
	dst = append(dst, TagMatch)
	dst = AppendUvarint(dst, dist)
	return AppendUvarint(dst, length)
}

// AppendFlush appends a flush boundary marker.
func AppendFlush(dst []byte) []byte { return append(dst, TagFlush) }

// AppendEnd appends the stream trailer.
func AppendEnd(dst []byte, total, checksum uint64) []byte {
	dst = append(dst, TagEnd)
	dst = AppendUvarint(dst, total)
	return AppendUvarint(dst, checksum)
}

// AppendUvarint writes v as an unsigned little-endian base-128 varint.
func AppendUvarint(dst []byte, v uint64) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

// ReadUvarint decodes one varint from the front of p, returning its value, the
// number of bytes consumed, whether more input is required, and overflow.
func ReadUvarint(p []byte) (value uint64, n int, incomplete, overflow bool) {
	var shift uint
	for n = 0; n < len(p) && n < 10; n++ {
		b := p[n]
		if n == 9 && b > 1 {
			return 0, n + 1, false, true
		}
		value |= uint64(b&0x7F) << shift
		if b < 0x80 {
			return value, n + 1, false, false
		}
		shift += 7
	}
	if len(p) <= 10 {
		return 0, len(p), true, false
}
	return 0, 11, false, true
}

// FNV1a64 incrementally folds b into a 64-bit FNV-1a checksum.
func FNV1a64(sum uint64, b byte) uint64 {
	const offset, prime = 14695981039346656037, 1099511628211
	return (sum ^ uint64(b)) * prime
}
