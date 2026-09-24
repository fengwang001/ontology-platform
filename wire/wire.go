// Package wire defines the byte format of the compressed stream.
package wire

import (
	"errors"
	"fmt"
)

// Sentinel errors for each distinguishable corruption class.
var (
	ErrHeader      = errors.New("wire: bad magic or version")
	ErrZeroDist    = errors.New("wire: back-reference distance is zero")
	ErrDistOutput  = errors.New("wire: distance exceeds bytes produced so far")
	ErrDistWindow  = errors.New("wire: distance exceeds window capacity")
	ErrVarint      = errors.New("wire: varint too long or overflows uint64")
	ErrLength      = errors.New("wire: declared length does not match output")
	ErrChecksum    = errors.New("wire: checksum mismatch")
	ErrTrailing    = errors.New("wire: trailing bytes after stream end")
	ErrTruncated   = errors.New("wire: truncated stream")
	ErrZeroMatch   = errors.New("wire: match length must be positive")
	ErrOutputLimit = errors.New("wire: output size limit exceeded")
)

// OffsetError wraps a corruption error with the byte offset in the stream.
type OffsetError struct {
	Offset int
	Err    error
}

func (e *OffsetError) Error() string {
	return fmt.Sprintf("%v (at byte %d)", e.Err, e.Offset)
}
func (e *OffsetError) Unwrap() error { return e.Err }

// At wraps err with a stream byte offset.
func At(off int, err error) error { return &OffsetError{Offset: off, Err: err} }

// Record tags.
const (
	TagLiteral byte = 0x00
	TagMatch   byte = 0x01
	TagFlush   byte = 0x02
	TagEnd     byte = 0x03
)

// Magic and version.
var Magic = []byte{'L', 'Z', '7', 0x7A}
const Version byte = 0x01

// PutUvarint appends a LEB128 unsigned integer to b.
func PutUvarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

// GetUvarint decodes one varint at b. It returns the value, bytes consumed and
// nil on success; ErrTruncated when more input may complete it; ErrVarint when
// the encoding exceeds 10 bytes or overflows 64 bits.
func GetUvarint(b []byte) (uint64, int, error) {
	var x uint64
	for i := 0; i < len(b) && i < 10; i++ {
		c := b[i]
		if i == 9 {
			if c > 1 {
				return 0, 0, ErrVarint // 10th byte carries overflow bits
			}
			if c >= 0x80 {
				return 0, 0, ErrVarint // 11th byte would be needed
			}
		}
		x |= uint64(c&0x7F) << (7 * i)
		if c < 0x80 {
			return x, i + 1, nil
		}
	}
	return 0, 0, ErrTruncated
}

// Header appends the stream header.
func Header(b []byte, windowCap uint64) []byte {
	b = append(b, Magic...)
	b = append(b, Version)
	return PutUvarint(b, windowCap)
}

// Literal appends a literal record.
func Literal(b, p []byte) []byte {
	b = append(b, TagLiteral)
	b = PutUvarint(b, uint64(len(p)))
	return append(b, p...)
}

// Match appends a back-reference record.
func Match(b []byte, dist, length uint64) []byte {
	b = append(b, TagMatch)
	b = PutUvarint(b, dist)
	b = PutUvarint(b, length)
	return b
}

// Flush appends a flush marker.
func Flush(b []byte) []byte { return append(b, TagFlush) }

// End appends the stream footer.
func End(b []byte, total, crc uint64) []byte {
	b = append(b, TagEnd)
	b = PutUvarint(b, total)
	b = PutUvarint(b, crc)
	return b
}
