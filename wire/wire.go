// Package wire defines the byte format of the LZ77 stream.
package wire

import "errors"

const (
	Magic     = "LZ77A"
	Version   = byte(1)
	TagLit    = 0x00
	TagMatch  = 0x01
	TagFlush  = 0x02
	TagEnd    = 0x03
	MinMatch  = 3
	MaxVarint = 10
)

// StreamError carries a class of corruption and the byte offset in the stream.
type StreamError struct {
	Kind   error
	Offset int64
}

func (e *StreamError) Error() string {
	return "wire: " + e.Kind.Error() + " at offset " + itoa(e.Offset)
}
func (e *StreamError) Unwrap() error { return e.Kind }

// At wraps a sentinel with the stream offset where it was detected.
func At(kind error, off int64) error { return &StreamError{Kind: kind, Offset: off} }

var (
	ErrBadHeader      = errors.New("bad magic or version")
	ErrZeroDistance   = errors.New("match distance is zero")
	ErrDistOutput     = errors.New("distance exceeds bytes output")
	ErrDistWindow     = errors.New("distance exceeds window capacity")
	ErrVarintOverflow = errors.New("varint too long or overflows uint64")
	ErrLengthMismatch = errors.New("declared total length differs from output")
	ErrChecksum       = errors.New("checksum mismatch")
	ErrTrailingBytes  = errors.New("unexpected bytes after end record")
	ErrTruncated      = errors.New("truncated stream")
	ErrOutputLimit    = errors.New("output size limit exceeded")
	ErrBadConfig      = errors.New("invalid configuration")
)

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
	b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// PutUvarint appends an unsigned LEB128 varint to b.
func PutUvarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

// ReadUvarint returns the value, bytes consumed, need-more-input, overflow.
func ReadUvarint(b []byte) (v uint64, n int, need, overflow bool) {
	for shift := uint(0); shift < 64; shift += 7 {
		if n >= len(b) {
			return 0, n, true, false
		}
		c := b[n]
		n++
		if c < 0x80 {
			if shift == 63 && c > 1 {
				return 0, n, false, true
			}
			return v | uint64(c)<<shift, n, false, false
		}
		v |= uint64(c&0x7f) << shift
	}
	if n >= len(b) {
		return 0, n, true, false
	}
	c := b[n]
	n++
	if c > 1 {
		return 0, n, false, true
	}
	return v | uint64(c)<<63, n, false, false
}

// Header writes the fixed stream header.
func Header() []byte {
	h := make([]byte, 0, 6)
	h = append(h, Magic...)
	return append(h, Version)
}

// Literal appends a literal record; len(data) must be >= 1.
func Literal(b, data []byte) []byte {
	b = append(b, TagLit)
	b = PutUvarint(b, uint64(len(data)))
	return append(b, data...)
}

// Match appends a back-reference record.
func Match(b []byte, dist, length int) []byte {
	b = append(b, TagMatch)
	b = PutUvarint(b, uint64(dist))
	b = PutUvarint(b, uint64(length))
	return b
}

// Flush appends a flush marker.
func Flush(b []byte) []byte { return append(b, TagFlush) }

// End appends the terminator with total original length and checksum.
func End(b []byte, total int64, sum uint32) []byte {
	b = append(b, TagEnd)
	b = PutUvarint(b, uint64(total))
	return PutUvarint(b, uint64(sum))
}
