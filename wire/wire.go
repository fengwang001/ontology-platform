// Package wire defines the self-describing LZ77 byte stream format.
package wire

import "errors"

// Header is the fixed 5-byte stream prefix.
var Header = []byte{0x4c, 0x5a, 0x37, 0x37, 0x01} // "LZ77" version 1

// Record tags.
const (
	TagLiteral byte = 0
	TagMatch   byte = 1
	TagFlush   byte = 2
	TagEnd     byte = 3
)

// MaxVarintLen is the maximum encoded length of a 64-bit unsigned varint.
const MaxVarintLen = 10

var (
	// ErrVarint means a varint exceeds 10 bytes or overflows uint64.
	ErrVarint = errors.New("wire: varint too long or overflow")
	// ErrTruncated means the stream ends before a complete record.
	ErrTruncated = errors.New("wire: truncated stream")
)

// OffsetError attaches the offending absolute byte offset to a sentinel error.
type OffsetError struct {
	Op     string
	Offset int
	Err    error
}

func (e *OffsetError) Error() string { return e.Op + ": " + e.Err.Error() }
func (e *OffsetError) Unwrap() error { return e.Err }

// AppendUvarint appends an unsigned LEB128 integer.
func AppendUvarint(dst []byte, v uint64) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

// ReadUvarint reads one varint from p starting at off. It returns the value,
// the offset just past it, ErrTruncated when more bytes are needed, and
// ErrVarint when the encoding exceeds 10 bytes or overflows.
func ReadUvarint(p []byte, off int) (uint64, int, error) {
	var x uint64
	for s := uint(0); s < 64; s += 7 {
		if off >= len(p) {
			return 0, off, ErrTruncated
		}
		b := p[off]
		off++
		if b < 0x80 {
			if s > 0 && b == 0 { // non-canonical overlong zero tail
				return 0, off, ErrVarint
			}
			if s == 63 && b > 1 {
				return 0, off, ErrVarint
			}
			return x | uint64(b)<<s, off, nil
		}
		x |= uint64(b&0x7f) << s
		if s == 63 {
			return 0, off, ErrVarint
		}
}
	return 0, off, ErrVarint
}

// FNV64 streams the 64-bit FNV-1a checksum.
type FNV64 struct{ h uint64 }

// NewFNV64 returns a fresh checksum at the FNV offset basis.
func NewFNV64() *FNV64 { return &FNV64{h: 14695981039346656037} }

// Write folds bytes into the checksum.
func (f *FNV64) Write(p []byte) {
	for _, b := range p {
		f.h ^= uint64(b)
		f.h *= 1099511628211
	}
}

// Sum returns the current checksum.
func (f *FNV64) Sum() uint64 { return f.h }
