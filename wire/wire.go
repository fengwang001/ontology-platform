// Package wire defines the byte-level format of the LZ77 stream.
package wire

import "errors"

const (
	Magic   = "L17"
	Version = uint64(1)
)

// Record tags.
const (
	TagLit   = 0
	TagMatch = 1
	TagFlush = 2
	TagEnd   = 3
)

var (
	ErrBadMagic   = errors.New("wire: bad magic")
	ErrBadVersion = errors.New("wire: unsupported version")
	ErrVarint     = errors.New("wire: varint too long or overflow")
)

// OffsetError attaches a byte offset inside the compressed stream.
type OffsetError struct {
	Offset int64
	Err    error
}

func (e *OffsetError) Error() string { return e.Err.Error() }
func (e *OffsetError) Unwrap() error { return e.Err }

// At wraps err with the byte offset off.
func At(off int64, err error) error {
	if err == nil {
		return nil
	}
	return &OffsetError{Offset: off, Err: err}
}

// AppendUvarint appends an unsigned LEB128 integer.
func AppendUvarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

// DecodeUvarint decodes one varint from buf[0:]. It returns the value, the
// number of bytes consumed, and whether the encoding is complete/valid.
func DecodeUvarint(buf []byte) (uint64, int, bool) {
	var v uint64
	for i := 0; i < len(buf) && i < 10; i++ {
		b := buf[i]
		if i == 9 && b > 1 {
			return 0, i + 1, false
		}
		v |= uint64(b&0x7f) << (7 * i)
		if b < 0x80 {
			return v, i + 1, true
		}
	}
	if len(buf) >= 10 {
		return 0, 10, false
	}
	return 0, 0, false
}

// AppendHeader appends the stream header: magic, version, window size.
func AppendHeader(b []byte, windowSize uint64) []byte {
	b = append(b, Magic...)
	b = AppendUvarint(b, Version)
	b = AppendUvarint(b, windowSize)
	return b
}

// AppendLit appends a literal record.
func AppendLit(b, lit []byte) []byte {
	b = append(b, TagLit)
	b = AppendUvarint(b, uint64(len(lit)))
	return append(b, lit...)
}

// AppendMatch appends a back-reference record.
func AppendMatch(b []byte, dist, length int) []byte {
	b = append(b, TagMatch)
	b = AppendUvarint(b, uint64(dist))
	b = AppendUvarint(b, uint64(length))
	return b
}

// AppendFlush appends a flush marker.
func AppendFlush(b []byte) []byte { return append(b, TagFlush) }

// AppendEnd appends the stream trailer.
func AppendEnd(b []byte, totalLen, crc uint64) []byte {
	b = append(b, TagEnd)
	b = AppendUvarint(b, totalLen)
	b = AppendUvarint(b, crc)
	return b
}
