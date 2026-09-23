// Package wire defines the compressed stream byte format.
package wire

import (
	"errors"
	"hash/crc32"
)

// Magic and version identify an OLZ stream header.
const (
	Magic0  = 0x4F // 'O'
	Magic1  = 0x4C // 'L'
	Magic2  = 0x5A // 'Z'
	Version = 1
)

// Record tags.
const (
	TagLiteral = 0
	TagMatch   = 1
	TagFlush   = 2
	TagEnd     = 3
)

// Varint sentinels. ErrIncomplete means more bytes are needed.
var (
	ErrIncomplete      = errors.New("wire: incomplete varint")
	ErrVarintTooLong   = errors.New("wire: varint longer than 10 bytes")
	ErrVarintOverflow  = errors.New("wire: varint overflows 64 bits")
)

// Header is the full stream header.
var Header = []byte{Magic0, Magic1, Magic2, Version}

// Checksum returns the CRC32 (IEEE) of the original data.
func Checksum(data []byte) uint32 { return crc32.ChecksumIEEE(data) }

// AppendUvarint appends x as an unsigned LEB128 varint.
func AppendUvarint(b []byte, x uint64) []byte {
	for x >= 0x80 {
		b = append(b, byte(x)|0x80)
		x >>= 7
	}
	return append(b, byte(x))
}

// AppendTag appends a single record tag.
func AppendTag(b []byte, tag byte) []byte { return append(b, tag) }

// AppendLiteral appends a literal segment (n>0).
func AppendLiteral(b, p []byte) []byte {
	b = AppendTag(b, TagLiteral)
	b = AppendUvarint(b, uint64(len(p)))
	return append(b, p...)
}

// AppendMatch appends a back-reference.
func AppendMatch(b []byte, dist, length int) []byte {
	b = AppendTag(b, TagMatch)
	b = AppendUvarint(b, uint64(dist))
	return AppendUvarint(b, uint64(length))
}

// ReadUvarint decodes one varint from the front of b.
// It returns the value and bytes consumed. ErrIncomplete means b ended
// mid-varint (feed more bytes); ErrVarintTooLong / ErrVarintOverflow are fatal.
func ReadUvarint(b []byte) (uint64, int, error) {
	var x uint64
	for i := 0; i < len(b); i++ {
		c := b[i]
		if i == 10 {
			return 0, 0, ErrVarintTooLong
		}
		if i == 9 && c > 1 {
			return 0, 0, ErrVarintOverflow
		}
		x |= uint64(c&0x7F) << (7 * i)
		if c < 0x80 {
			return x, i + 1, nil
		}
	}
	return 0, 0, ErrIncomplete
}
