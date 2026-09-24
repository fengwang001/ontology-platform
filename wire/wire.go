// Package wire defines the byte-level format of the LZ77 compressed stream.
package wire

import "errors"

const (
	// Header is the fixed 5-byte stream header: "LZ77" plus version 1.
	Header = "LZ77\x01"
	// TagLit, TagMatch, TagFlush, TagEnd live in the top 2 bits of a record byte.
	TagLit   = 0x00
	TagMatch = 0x40
	TagFlush = 0x80
	TagEnd   = 0xC0
	tagMask  = 0xC0
)

// Sentinel errors shared by encoders and decoders. All are detectable by ==.
var (
	ErrBadMagic      = errors.New("wire: bad magic or version")
	ErrZeroDistance  = errors.New("wire: back-reference distance is zero")
	ErrDistPast      = errors.New("wire: distance exceeds produced output")
	ErrDistWindow    = errors.New("wire: distance exceeds window capacity")
	ErrVarintLong    = errors.New("wire: varint longer than 10 bytes")
	ErrVarintRange   = errors.New("wire: varint overflows 64 bits")
	ErrLengthMismatch = errors.New("wire: end total length mismatch")
	ErrChecksum      = errors.New("wire: checksum mismatch")
	ErrTrailingBytes = errors.New("wire: trailing bytes after end record")
	ErrTruncated     = errors.New("wire: truncated stream")
	ErrOutputLimit   = errors.New("wire: output size limit exceeded")
	ErrClosed        = errors.New("wire: terminal state after error")
)

// CorruptError wraps a corruption error with the byte offset in the stream.
type CorruptError struct {
	Offset int
	Err    error
}

func (e *CorruptError) Error() string { return e.Err.Error() }
func (e *CorruptError) Unwrap() error { return e.Err }

// PutUvarint appends an unsigned LEB128 value to b.
func PutUvarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

// ReadUvarint consumes one unsigned LEB128 from b. It returns the value, the
// number of bytes consumed (0 when more bytes are needed) and an error.
func ReadUvarint(b []byte, off int) (uint64, int, error) {
	var x uint64
	for i := 0; i < 10; i++ {
		if off+i >= len(b) {
			return 0, 0, nil
		}
		c := b[off+i]
		if i == 9 && c > 1 {
			return 0, 0, ErrVarintRange
		}
		x |= uint64(c&0x7f) << (7 * i)
		if c < 0x80 {
			return x, i + 1, nil
		}
	}
	return 0, 0, ErrVarintLong
}

// TagOf returns the record tag of a byte.
func TagOf(c byte) byte { return c & tagMask }

// AppendLit appends a literal record. p must be non-empty.
func AppendLit(b, p []byte) []byte {
	b = append(b, TagLit)
	b = PutUvarint(b, uint64(len(p)))
	return append(b, p...)
}

// AppendMatch appends a back-reference record.
func AppendMatch(b []byte, dist, length int) []byte {
	b = append(b, TagMatch)
	b = PutUvarint(b, uint64(dist))
	b = PutUvarint(b, uint64(length))
	return b
}

// AppendFlush appends a flush marker.
func AppendFlush(b []byte) []byte { return append(b, TagFlush) }

// AppendEnd appends the stream tail.
func AppendEnd(b []byte, total int, crc uint32) []byte {
	b = append(b, TagEnd)
	b = PutUvarint(b, uint64(total))
	b = PutUvarint(b, uint64(crc))
	return b
}
