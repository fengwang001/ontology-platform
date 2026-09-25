// Package wire defines the byte-level format of the compressed stream.
// It has no dependencies on the other packages.
package wire

import "errors"

const (
	MinMatch = 3
	Magic0   = 'L'
	Magic1   = 'Z'
	Magic2   = '7'
	Version  = byte(1)
	HeaderN  = 5 // 3 magic bytes, version, then two varints follow
)

// Record tags stored in the low two bits of the first varint.
const (
	TagLit   = 0
	TagMatch = 1
	TagFlush = 2
	TagDict  = 3
	TagEnd   = 4
)

var (
	ErrVarint = errors.New("wire: varint too long or overflows 64 bits")
	ErrTag    = errors.New("wire: unknown record tag")
	ErrEncode = errors.New("wire: value cannot be encoded")
)

// AppendUvarint appends x as an unsigned LEB128 varint.
func AppendUvarint(b []byte, x uint64) []byte {
	for x >= 0x80 {
		b = append(b, byte(x)|0x80)
		x >>= 7
	}
	return append(b, byte(x))
}

// ReadUvarint decodes one varint from b. It returns the value, the number of
// bytes consumed (0 when incomplete) and ErrVarint on a malformed varint.
func ReadUvarint(b []byte) (uint64, int, error) {
	var x uint64
	for i := 0; i < len(b); i++ {
		c := b[i]
		if i == 9 && c > 1 {
			return 0, 0, ErrVarint
		}
		if i >= 10 {
			return 0, 0, ErrVarint
		}
		x |= uint64(c&0x7f) << (7 * i)
		if c < 0x80 {
			return x, i + 1, nil
		}
	}
	return 0, 0, nil
}

// AppendTag emits tag combined with the first payload value v: (v<<2)|tag.
func AppendTag(b []byte, tag uint64, v uint64) []byte {
	return AppendUvarint(b, (v<<2)|tag)
}

// ReadTag reads a combined tag/value varint.
func ReadTag(b []byte) (tag uint64, v uint64, n int, err error) {
	x, n, err := ReadUvarint(b)
	if err != nil || n == 0 {
		return 0, 0, n, err
	}
	if t := x & 3; t > TagEnd {
		return 0, 0, n, ErrTag
	} else {
		return t, x >> 2, n, nil
	}
}

// AppendHeader writes the stream header.
func AppendHeader(b []byte, windowSize, maxChain uint64) []byte {
	b = append(b, Magic0, Magic1, Magic2, Version)
	b = AppendUvarint(b, windowSize)
	return AppendUvarint(b, maxChain)
}

// FNV-1a 64, implemented locally to avoid any dependency choices leaking.
const (
	fnvOffset = 14695981039346656037
	fnvPrime  = 1099511628211
)

func FNV(h uint64, p []byte) uint64 {
	for _, c := range p {
		h ^= uint64(c)
		h *= fnvPrime
	}
	return h
}

func FNVInitial() uint64 { return fnvOffset }
