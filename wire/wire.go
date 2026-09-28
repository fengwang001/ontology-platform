// Package wire defines the byte format of the compressed stream.
package wire

import (
	"errors"
	"fmt"
)

// Record tags.
const (
	TagLit   = 0
	TagRef   = 1
	TagFlush = 2
	TagEnd   = 3
)

// Header is the fixed 4-byte stream magic/version.
var Header = [4]byte{'O', 'L', 'Z', '1'}

// Sentinel errors. Class is one of the stable strings below; Offset is the
// byte offset in the compressed stream the error refers to.
var (
	ErrBadMagic     = errors.New("wire: bad magic or version")
	ErrTruncated    = errors.New("wire: truncated stream")
	ErrVarint       = errors.New("wire: varint too long or overflows 64 bits")
	ErrZeroDist     = errors.New("wire: back-reference distance is zero")
	ErrDistHistory  = errors.New("wire: distance exceeds emitted bytes")
	ErrDistWindow   = errors.New("wire: distance exceeds window capacity")
	ErrBadLength    = errors.New("wire: declared length mismatch")
	ErrChecksum     = errors.New("wire: checksum mismatch")
	ErrTrailingData = errors.New("wire: trailing bytes after end record")
)

// OffsetError attaches a stream byte offset to a sentinel error.
type OffsetError struct {
	Err    error
	Offset int
}

func (e *OffsetError) Error() string {
	return fmt.Sprintf("%v at byte %d", e.Err, e.Offset)
}

func (e *OffsetError) Unwrap() error { return e.Err }

// At wraps err with an offset.
func At(err error, offset int) error {
	return &OffsetError{Err: err, Offset: offset}
}

// PutUvarint appends an unsigned LEB128 integer to b.
func PutUvarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

// ReadUvarint reads an unsigned LEB128 at b[0:]. It returns the value, the
// number of bytes consumed and false when the integer is incomplete (more
// bytes may arrive later). An overflowing / >10-byte integer yields ok=true
// with err set.
func ReadUvarint(b []byte) (v uint64, n int, incomplete bool, err error) {
	for shift := uint(0); shift < 64; shift += 7 {
		if n >= len(b) {
			return 0, n, true, nil
		}
		c := b[n]
		n++
		if c < 0x80 {
			if shift == 63 && c > 1 {
				return 0, n, false, ErrVarint
			}
			v |= uint64(c) << shift
			return v, n, false, nil
		}
		v |= uint64(c&0x7f) << shift
	}
	return 0, n, false, ErrVarint
}
