// Package wire defines the byte format of the LZ77 stream.
package wire

import "errors"

const (
	Magic0 byte = 'L'
	Magic1 byte = 'Z'
	Magic2 byte = '7'
	Magic3 byte = '7'

	Version uint64 = 1

	TagLiteral byte = 0
	TagMatch   byte = 1
	TagFlush   byte = 2
	TagEnd     byte = 3
)

var (
	ErrVarintTooLong  = errors.New("wire: varint longer than 10 bytes")
	ErrVarintOverflow = errors.New("wire: varint overflows uint64")
)

// AppendUvarint appends a LEB128 unsigned varint.
func AppendUvarint(dst []byte, v uint64) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

// ConsumeUvarint consumes one varint from b.
// It returns the value, the number of bytes consumed and whether more bytes
// are required (b is a prefix of a valid encoding).
func ConsumeUvarint(b []byte) (v uint64, n int, more bool, err error) {
	var shift uint
	for i, c := range b {
		if i == 10 {
			return 0, 0, false, ErrVarintTooLong
		}
		if i == 9 && c > 1 {
			return 0, 0, false, ErrVarintOverflow
		}
		v |= uint64(c&0x7f) << shift
		n = i + 1
		if c < 0x80 {
			return v, n, false, nil
		}
		shift += 7
	}
	return 0, 0, true, nil
}

func AppendHeader(dst []byte) []byte {
	dst = append(dst, Magic0, Magic1, Magic2, Magic3)
	return AppendUvarint(dst, Version)
}

func AppendLiteral(dst, lit []byte) []byte {
	dst = append(dst, TagLiteral)
	dst = AppendUvarint(dst, uint64(len(lit)))
	return append(dst, lit...)
}

func AppendMatch(dst []byte, dist, length int) []byte {
	dst = append(dst, TagMatch)
	dst = AppendUvarint(dst, uint64(dist))
	return AppendUvarint(dst, uint64(length))
}

func AppendFlush(dst []byte) []byte { return append(dst, TagFlush) }

func AppendEnd(dst []byte, totalLen, checksum uint64) []byte {
	dst = append(dst, TagEnd)
	dst = AppendUvarint(dst, totalLen)
	return AppendUvarint(dst, checksum)
}
