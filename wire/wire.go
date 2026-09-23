// Package wire defines the self-described LZ77 byte stream format.
package wire

import "errors"

const (
	Magic0   = 'L'
	Magic1   = 'Z'
	Magic2   = '7'
	Version  = 1
	TagLit   = 0
	TagMatch = 1
	TagFlush = 2
	TagEnd   = 3
)

// Header is the fixed 5-byte stream prefix followed by a varint window size.
type Header struct {
	Window int
}

var ErrVarint = errors.New("wire: varint too long or overflows 64 bits")

// AppendUvarint encodes x using unsigned LEB128 (max 10 bytes).
func AppendUvarint(dst []byte, x uint64) []byte {
	for x >= 0x80 {
		dst = append(dst, byte(x)|0x80)
		x >>= 7
	}
	return append(dst, byte(x))
}

// ReadUvarint decodes one varint starting at b[off], returning its end offset.
// It counts only bytes that actually exist, so ErrVarint doubles as truncation
// when the encoded integer simply runs out of stream.
func ReadUvarint(b []byte, off int) (uint64, int, error) {
	var x uint64
	for i := 0; i < 10; i++ {
		if off >= len(b) {
			return 0, off, ErrVarint
		}
		c := b[off]
		off++
		if i == 9 && c > 1 {
			return 0, off, ErrVarint
		}
		x |= uint64(c&0x7f) << (7 * i)
		if c < 0x80 {
			return x, off, nil
		}
	}
	return 0, off, ErrVarint
}

// AppendHeader writes the stream header.
func AppendHeader(dst []byte, w int) []byte {
	dst = append(dst, Magic0, Magic1, Magic2, Version)
	return AppendUvarint(dst, uint64(w))
}

// ParseHeader parses the prefix; need reports bytes required when truncated.
func ParseHeader(b []byte) (h Header, end int, need int, err error) {
	if len(b) < 4 {
		return Header{}, 0, 4 - len(b), nil
	}
	if b[0] != Magic0 || b[1] != Magic1 || b[2] != Magic2 || b[3] != Version {
		return Header{}, 0, 0, errors.New("wire: bad magic or version")
	}
	w, off, e := ReadUvarint(b, 4)
	if e != nil {
		return Header{}, off, 1, nil
	}
	return Header{Window: int(w)}, off, 0, nil
}

// AppendLiteral writes a literal run.
func AppendLiteral(dst, lit []byte) []byte {
	dst = append(dst, TagLit)
	dst = AppendUvarint(dst, uint64(len(lit)))
	return append(dst, lit...)
}

// AppendMatch writes a back reference.
func AppendMatch(dst []byte, dist, length int) []byte {
	dst = append(dst, TagMatch)
	dst = AppendUvarint(dst, uint64(dist))
	return AppendUvarint(dst, uint64(length))
}
