// Package wire defines the on-the-wire byte format of the LZ77 stream.
package wire

import "errors"

const (
	Magic   = "LZ77ONTO"
	Version = uint64(1)
)

// Record tags stored in the leading varint.
const (
	TagLiteral = 0
	TagMatch   = 1 // leading varint carries 1|(dist<<1)
	TagFlush   = 2
	TagEnd     = 3
)

var ErrVarintOverflow = errors.New("wire: varint longer than 10 bytes or overflowed 64 bits")

// AppendVarint appends an unsigned LEB128 value.
func AppendVarint(buf []byte, v uint64) []byte {
	for v >= 0x80 {
		buf = append(buf, byte(v)|0x80)
		v >>= 7
	}
	return append(buf, byte(v))
}

// DecodeVarint decodes one unsigned LEB128. n is the number of bytes consumed;
// n==0 means the input ends mid-varint (truncation).
func DecodeVarint(b []byte) (v uint64, n int, err error) {
	for i := 0; i < len(b); i++ {
		c := b[i]
		if i == 9 && c > 1 {
			return 0, 0, ErrVarintOverflow
		}
		if i == 10 {
			return 0, 0, ErrVarintOverflow
		}
		v |= uint64(c&0x7f) << (7 * i)
		if c < 0x80 {
			return v, i + 1, nil
		}
	}
	return 0, 0, nil
}

// AppendHeader appends the stream header.
func AppendHeader(buf []byte) []byte {
	buf = append(buf, Magic...)
	return AppendVarint(buf, Version)
}

// AppendLiteral appends one literal run.
func AppendLiteral(buf, data []byte) []byte {
	buf = AppendVarint(buf, TagLiteral)
	buf = AppendVarint(buf, uint64(len(data)))
	return append(buf, data...)
}

// AppendMatch appends a back-reference. d>=1, l>=1.
func AppendMatch(buf []byte, d, l uint64) []byte {
	buf = AppendVarint(buf, 1|(d<<1))
	return AppendVarint(buf, l)
}

// AppendFlush appends a flush marker.
func AppendFlush(buf []byte) []byte { return AppendVarint(buf, TagFlush) }

// AppendEnd appends the stream tail.
func AppendEnd(buf []byte, totalLen, checksum uint64) []byte {
	buf = AppendVarint(buf, TagEnd)
	buf = AppendVarint(buf, totalLen)
	return AppendVarint(buf, checksum)
}

// IsMatch reports whether a leading varint denotes a match record.
func IsMatch(t uint64) bool { return t&1 == 1 }

// MatchDist extracts the distance from a match record's leading varint.
func MatchDist(t uint64) uint64 { return t >> 1 }
}
