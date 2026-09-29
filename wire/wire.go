// Package wire defines the self-describing LZ77 stream byte format.
package wire

import "errors"

const (
	Magic   = "LZO7"
	Version = 1

	TagLiteral byte = 0
	TagMatch   byte = 1
	TagFlush   byte = 2
	TagEnd     byte = 3

	MaxVarintLen = 10
	MinMatchLen  = 3
	MaxMatchLen  = 512
)

var ErrVarint = errors.New("wire: varint too long or overflows uint64")

type CorruptError struct {
	Offset int
	Kind   error
}

func (e *CorruptError) Error() string { return "wire: corrupt at " + itoa(e.Offset) + ": " + e.Kind.Error() }
func (e *CorruptError) Unwrap() error { return e.Kind }

var (
	ErrHeader        = errors.New("bad magic or version")
	ErrZeroDistance  = errors.New("match distance is zero")
	ErrDistOutput    = errors.New("distance exceeds bytes emitted")
	ErrDistWindow    = errors.New("distance exceeds window capacity")
	ErrLength        = errors.New("match length out of range")
	ErrSizeMismatch  = errors.New("trailer size mismatch")
	ErrChecksum      = errors.New("checksum mismatch")
	ErrTrailingBytes = errors.New("trailing bytes after end")
	ErrTruncated     = errors.New("truncated stream")
)

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [24]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func AppendUvarint(dst []byte, v uint64) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

// ReadUvarint returns the value, bytes consumed, and ErrVarint on overflow.
func ReadUvarint(src []byte) (uint64, int, error) {
	var x uint64
	for i := 0; i < len(src) && i < MaxVarintLen; i++ {
		b := src[i]
		if i == MaxVarintLen-1 && b > 1 {
			return 0, i + 1, ErrVarint
		}
		x |= uint64(b&0x7f) << (7 * i)
		if b < 0x80 {
			return x, i + 1, nil
		}
		if i == MaxVarintLen-1 {
			return 0, MaxVarintLen, ErrVarint // 10 continuation bytes
		}
	}
	return 0, len(src), ErrTruncated
}
