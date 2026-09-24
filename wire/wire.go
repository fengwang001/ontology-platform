// Package wire defines the byte-level format of the LZ77 stream.
// It depends on no other package of the module.
package wire

import "errors"

const (
	Version = 1
	// Tag values occupy a single varint byte (0..3).
	TagLiteral = 0
	TagMatch   = 1
	TagFlush   = 2
	TagEnd     = 3

	MaxVarintLen = 10
	MinMatchLen  = 3
)

var Magic = [2]byte{'L', 'Z'}

// AppendUvarint appends x as unsigned LEB128.
func AppendUvarint(b []byte, x uint64) []byte {
	for x >= 0x80 {
		b = append(b, byte(x)|0x80)
		x >>= 7
	}
	return append(b, byte(x))
}

var ErrVarint = errors.New("wire: malformed or overflowing varint")

// ReadUvarint decodes one varint starting at b[off]. It returns the value,
// the offset just past it, and ErrVarint when the varint exceeds 10 bytes or
// overflows 64 bits.
func ReadUvarint(b []byte, off int) (uint64, int, error) {
	var x uint64
	start := off
	for shift := uint(0); ; shift += 7 {
		if off >= len(b) {
			return 0, off, nil // incomplete: caller decides truncated vs corrupt
		}
		c := b[off]
		off++
		if shift == 63 && c > 1 {
			return 0, start, ErrVarint
		}
		if off-start > MaxVarintLen {
			return 0, start, ErrVarint
		}
		x |= uint64(c&0x7f) << shift
		if c < 0x80 {
			return x, off, nil
		}
	}
}

// AppendHeader writes magic, version and window capacity.
func AppendHeader(b []byte, windowCap uint64) []byte {
	b = append(b, Magic[:]...)
	b = AppendUvarint(b, Version)
	b = AppendUvarint(b, windowCap)
	return b
}

func AppendLiteral(b []byte, p []byte) []byte {
	b = AppendUvarint(b, TagLiteral)
	b = AppendUvarint(b, uint64(len(p)))
	return append(b, p...)
}

func AppendMatch(b []byte, dist, length uint64) []byte {
	b = AppendUvarint(b, TagMatch)
	b = AppendUvarint(b, dist)
	b = AppendUvarint(b, length)
	return b
}

func AppendFlush(b []byte) []byte { return AppendUvarint(b, TagFlush) }

// AppendEnd writes the trailer: original length and FNV-1a 64 checksum.
func AppendEnd(b []byte, total, checksum uint64) []byte {
	b = AppendUvarint(b, TagEnd)
	b = AppendUvarint(b, total)
	b = AppendUvarint(b, checksum)
	return b
}

const (
	fnvOffset = 14695981039346656037
	fnvPrime  = 1099511628211
)

// FNV64 continues the 64-bit FNV-1a hash of p from seed h.
func FNV64(h uint64, p []byte) uint64 {
	for _, c := range p {
		h ^= uint64(c)
		h *= fnvPrime
	}
	return h
}

func NewHash() uint64 { return fnvOffset }
