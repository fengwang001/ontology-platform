// Package wire defines the byte format of the compressed stream.
package wire

import "errors"

// Record tags.
const (
	Magic0   byte = 0x4F // 'O'
	Magic1   byte = 0x4E // 'N'
	Version  byte = 0x01
	TagLit   byte = 0x10
	TagMatch byte = 0x20
	TagFlush byte = 0x30
	TagEnd   byte = 0x40
)

// Sentinel errors. OffsetError reports the stream byte offset when relevant.
var (
	ErrBadHeader   = errors.New("wire: bad magic or version")
	ErrZeroDist    = errors.New("wire: back-reference distance is zero")
	ErrDistOutput  = errors.New("wire: distance exceeds bytes produced")
	ErrDistWindow  = errors.New("wire: distance exceeds window capacity")
	ErrVarint      = errors.New("wire: varint too long or overflows 64 bits")
	ErrLength      = errors.New("wire: trailer length mismatch")
	ErrChecksum    = errors.New("wire: checksum mismatch")
	ErrTrailing    = errors.New("wire: trailing bytes after end record")
	ErrTruncated   = errors.New("wire: truncated stream")
	ErrOutputLimit = errors.New("wire: output size limit exceeded")
	ErrClosed      = errors.New("wire: decoder in terminal error state")
)

// OffsetError wraps a format error with the byte offset in the stream.
type OffsetError struct {
	Offset int
	Err    error
}

func (e *OffsetError) Error() string { return e.Err.Error() }
func (e *OffsetError) Unwrap() error { return e.Err }

// At wraps err with the stream offset.
func At(offset int, err error) error { return &OffsetError{Offset: offset, Err: err} }

// AppendUvarint appends an unsigned LEB128 value to b.
func AppendUvarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

// ReadUvarint consumes one uvarint from the front of b.
// It returns the value, bytes consumed, and ok=false if more input is needed.
// A sequence longer than 10 bytes or overflowing 64 bits returns err=ErrVarint.
func ReadUvarint(b []byte) (v uint64, n int, err error) {
	var shift uint
	for n = 0; n < len(b); n++ {
		c := b[n]
		if n == 9 && (c&0x7F) > 1 || n == 10 {
			return 0, n, ErrVarint
		}
		v |= uint64(c&0x7F) << shift
		if c&0x80 == 0 {
			return v, n + 1, nil
		}
		shift += 7
		if shift >= 64 {
			return 0, n, ErrVarint
		}
	}
	return 0, 0, nil
}

// UvarintLen reports the encoded length of v.
func UvarintLen(v uint64) int {
	n := 1
	for v >= 0x80 {
		v >>= 7
		n++
	}
	return n
}
