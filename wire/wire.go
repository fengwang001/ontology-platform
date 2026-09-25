// Package wire defines the byte-level format of the compressed stream.
package wire

import "errors"

const (
	Magic0 = 'L'
	Magic1 = 'Z'
	Magic2 = '7'
	Version = 1

	TagLit   = 0x00
	TagRef   = 0x01
	TagFlush = 0x02
	TagTail  = 0x03

	MaxVarintLen = 10
)

// Sentinel errors shared by enc/dec. Decode-side errors carry a byte offset.
var (
	ErrBadMagic      = errors.New("wire: bad magic")
	ErrBadVersion    = errors.New("wire: unsupported version")
	ErrBadTag        = errors.New("wire: unknown record tag")
	ErrVarintTooLong = errors.New("wire: varint longer than 10 bytes")
	ErrVarintOverflow = errors.New("wire: varint overflows uint64")
	ErrTruncated     = errors.New("wire: truncated stream")
)

// Header is the fixed 4-byte stream header.
type Header struct{}

// AppendHeader writes the fixed stream header.
func AppendHeader(p []byte) []byte {
	return append(p, Magic0, Magic1, Magic2, Version)
}

// HeaderLen returns the fixed header length.
func HeaderLen() int { return 4 }

// AppendUvarint appends x using unsigned LEB128.
func AppendUvarint(p []byte, x uint64) []byte {
	for x >= 0x80 {
		p = append(p, byte(x)|0x80)
		x >>= 7
	}
	return append(p, byte(x))
}

// ReadUvarint decodes one varuint from b starting at off. It returns the value,
// the new offset, ErrTruncated, ErrVarintTooLong or ErrVarintOverflow.
func ReadUvarint(b []byte, off int) (uint64, int, error) {
	var x uint64
	for i := 0; i < MaxVarintLen; i++ {
		if off >= len(b) {
			return 0, off, ErrTruncated
		}
		c := b[off]
		off++
		if i == MaxVarintLen-1 && (c > 0x01 || c&0x80 != 0) {
			return 0, off, ErrVarintOverflow
		}
		x |= uint64(c&0x7f) << (7 * i)
		if c&0x80 == 0 {
			return x, off, nil
		}
	}
	return 0, off, ErrVarintTooLong
}
