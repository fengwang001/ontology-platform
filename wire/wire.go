// Package wire defines the byte-level format of the LZ77 stream.
// It depends on no other package.
package wire

import "errors"

// MaxVarintLen is the maximal legal length of an unsigned LEB128 integer.
const MaxVarintLen = 10

// Header layout and version.
var Header = []byte{'L', 'Z', '7', '7', 'X', 1}

const (
	TagFlush byte = 0xC0 // 11000000
	TagEnd   byte = 0xC1 // 11000001
)

// IsHeader reports whether b begins with an exact stream header.
func IsHeader(b []byte) bool {
	return len(b) >= len(Header) && string(b[:len(Header)]) == string(Header)
}

// IsLiteralTag reports a literal record tag: top bit clear.
func IsLiteralTag(t byte) bool { return t&0x80 == 0 }

// LiteralLen extracts the literal length (low 7 bits of the tag).
func LiteralLen(t byte) int { return int(t & 0x7F) }

// IsMatchTag reports a match record tag: top two bits 10.
func IsMatchTag(t byte) bool { return t&0xC0 == 0x80 }

// ErrVarint marks a truncated, over-long (>10 bytes) or overflowing varint.
var ErrVarint = errors.New("wire: invalid varint")

// AppendVarint appends v as unsigned LEB128.
func AppendVarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

// ReadVarint reads an unsigned LEB128 from b, returning the value and bytes used.
// It returns ErrVarint on truncation, >10 bytes, or uint64 overflow.
func ReadVarint(b []byte) (uint64, int, error) {
	var v uint64
	for i := 0; i < MaxVarintLen; i++ {
		if i >= len(b) {
			return 0, i, ErrVarint
		}
		c := b[i]
		if i == MaxVarintLen-1 && c > 1 { // only bit 0 of byte 10 may be set
			return 0, i + 1, ErrVarint
		}
		v |= uint64(c&0x7F) << (7 * i)
		if c&0x80 == 0 {
			return v, i + 1, nil
		}
	}
	return 0, MaxVarintLen, ErrVarint
}

// AppendLiteral appends a literal record (length must be 0..127).
func AppendLiteral(b, lit []byte) []byte {
	if len(lit) > 0x7F {
		panic("wire: literal run too long")
	}
	b = append(b, byte(len(lit)))
	return append(b, lit...)
}

// AppendMatch appends a back-reference. Length is encoded as (length-MinMatch):
// tag low 6 bits hold its bits 0..5; the trailing varint holds the rest >> 6.
func AppendMatch(b []byte, dist, length int) []byte {
	e := uint64(length - MinMatch)
	b = append(b, 0x80|byte(e&0x3F))
	b = AppendVarint(b, uint64(dist))
	return AppendVarint(b, e>>6)
}

// DecodeMatchLen reconstructs the match length from tag and the trailing varint.
func DecodeMatchLen(tag byte, high uint64) int {
	return MinMatch + int(uint64(tag&0x3F)|(high<<6))
}

// AppendFlush appends a flush marker.
func AppendFlush(b []byte) []byte { return append(b, TagFlush) }

// AppendEnd appends the stream tail: original length and FNV-1a checksum.
func AppendEnd(b []byte, totalLen uint64, checksum uint64) []byte {
	b = append(b, TagEnd)
	b = AppendVarint(b, totalLen)
	return AppendVarint(b, checksum)
}

const (
	fnvOffset = 14695981039346656037
	fnvPrime  = 1099511628211
)

// FNV1a64 computes the FNV-1a 64-bit checksum of p.
func FNV1a64(p []byte) uint64 {
	h := uint64(fnvOffset)
	for _, c := range p {
		h ^= uint64(c)
		h *= fnvPrime
	}
	return h
}

// MinMatch is the minimum match length that is emitted as a back-reference.
const MinMatch = 3
