// Package wire defines the self-describing LZ77 stream byte format.
package wire

import "errors"

const (
	Magic   uint16 = 0x4F5A
	Version byte   = 1
)

// Low two bits of a record tag: 0 literal, 1 back-reference, 2 flush, 3 end.
const (
	TagLiteral = 0
	TagBackref = 1
	TagFlush   = 2
	TagEnd     = 3
)

var (
	ErrMagic       = errors.New("wire: bad magic")
	ErrVersion     = errors.New("wire: unsupported version")
	ErrTruncated   = errors.New("wire: truncated stream")
	ErrBadTag      = errors.New("wire: bad record tag")
	ErrZeroDist    = errors.New("wire: back-reference distance is zero")
	ErrDistWindow  = errors.New("wire: back-reference distance exceeds window capacity")
	ErrDistHistory = errors.New("wire: back-reference distance exceeds available history")
	ErrVarint      = errors.New("wire: varint overlong or overflows 64 bits")
	ErrLength      = errors.New("wire: declared total length mismatch")
	ErrChecksum    = errors.New("wire: checksum mismatch")
	ErrTrailing    = errors.New("wire: trailing bytes after stream end")
	ErrOutputLimit = errors.New("wire: output limit exceeded")
	ErrConfig      = errors.New("wire: invalid configuration")
)

// CorruptError wraps a sentinel with the byte offset in the compressed stream.
type CorruptError struct {
	Offset int
	Err    error
}

func (e *CorruptError) Error() string { return e.Err.Error() }
func (e *CorruptError) Unwrap() error { return e.Err }

// At wraps err with the stream offset where it was detected.
func At(off int, err error) error { return &CorruptError{Offset: off, Err: err} }

func AppendUvarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

// ReadUvarint returns n==0 when more bytes are needed (truncation).
func ReadUvarint(buf []byte) (v uint64, n int, err error) {
	for i := 0; i < len(buf) && i < 10; i++ {
		c := buf[i]
		if i == 9 && c > 1 {
			return 0, i + 1, ErrVarint
		}
		v |= uint64(c&0x7f) << (7 * i)
		if c < 0x80 {
			return v, i + 1, nil
		}
}
	if len(buf) >= 10 {
		return 0, 10, ErrVarint
	}
	return 0, 0, nil
}

func AppendHeader(b []byte, windowCap int) []byte {
	b = append(b, byte(Magic&0xff), byte(Magic>>8), Version)
	return AppendUvarint(b, uint64(windowCap))
}

func AppendLiteral(b, p []byte) []byte {
	b = AppendUvarint(append(b, TagLiteral), uint64(len(p)))
	return append(b, p...)
}

func AppendBackref(b []byte, dist, length int) []byte {
	b = AppendUvarint(append(b, TagBackref), uint64(dist))
	return AppendUvarint(b, uint64(length))
}

func AppendFlush(b []byte) []byte { return append(b, TagFlush) }

func AppendEnd(b []byte, total int, crc uint32) []byte {
	b = AppendUvarint(append(b, TagEnd), uint64(total))
	return AppendUvarint(b, uint64(crc))
}
