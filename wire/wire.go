// Package wire defines the self-described LZ77 stream byte format.
package wire

import (
	"errors"
	"fmt"
)

const (
	Magic   = "LZ77"
	Version = uint64(1)
)

const (
	TagLiteral byte = 0
	TagMatch   byte = 1
	TagFlush   byte = 2
	TagEnd     byte = 3
)

// Sentinel errors. Callers distinguish corruption classes through errors.Is.
var (
	ErrHeader       = errors.New("wire: bad magic or version")
	ErrTruncated    = errors.New("wire: truncated stream")
	ErrVarint       = errors.New("wire: varint too long or overflows 64 bits")
	ErrBadTag       = errors.New("wire: unknown record tag")
	ErrZeroDist     = errors.New("wire: back-reference distance is zero")
	ErrDistOutput   = errors.New("wire: distance exceeds emitted bytes")
	ErrDistWindow   = errors.New("wire: distance exceeds window capacity")
	ErrLength       = errors.New("wire: declared length exceeds output limit")
	ErrTotal        = errors.New("wire: end total length mismatch")
	ErrChecksum     = errors.New("wire: checksum mismatch")
	ErrTrailing     = errors.New("wire: trailing bytes after stream end")
	ErrClosed       = errors.New("wire: decoder in terminal error state")
	ErrInvalidConfig = errors.New("wire: invalid configuration")
)

// FormatError annotates a sentinel with the byte offset inside the stream.
type FormatError struct {
	Offset int64
	Err    error
}

func (e *FormatError) Error() string {
	return fmt.Sprintf("%v at byte %d", e.Err, e.Offset)
}
func (e *FormatError) Unwrap() error { return e.Err }

// AppendUvarint appends an unsigned LEB128 varint (max 10 bytes).
func AppendUvarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

// ConsumeUvarint reads one varint. n==0 with err==nil means "need more bytes".
func ConsumeUvarint(buf []byte) (v uint64, n int, err error) {
	var shift uint
	for i, c := range buf {
		if i == 10 {
			return 0, i, ErrVarint
		}
		if i == 9 && c > 1 {
			return 0, i + 1, ErrVarint
		}
		v |= uint64(c&0x7f) << shift
		if c&0x80 == 0 {
			return v, i + 1, nil
		}
		shift += 7
		if i == 9 {
			return 0, i + 1, ErrVarint
		}
	}
	return 0, 0, nil
}

func Header() []byte {
	b := []byte(Magic)
	return AppendUvarint(b, Version)
}

func AppendLiteral(b, data []byte) []byte {
	b = append(b, TagLiteral)
	b = AppendUvarint(b, uint64(len(data)))
	return append(b, data...)
}

func AppendMatch(b []byte, dist, length int) []byte {
	b = append(b, TagMatch)
	b = AppendUvarint(b, uint64(dist))
	return AppendUvarint(b, uint64(length))
}

func AppendFlush(b []byte) []byte { return append(b, TagFlush) }

func AppendEnd(b []byte, total int, sum uint32) []byte {
	b = append(b, TagEnd)
	b = AppendUvarint(b, uint64(total))
	return AppendUvarint(b, uint64(sum))
}
