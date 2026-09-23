package wire

import (
	"errors"
	"fmt"
	"io"
)

const (
	TagLiteral = 0
	TagMatch   = 1
	TagFlush   = 2
	TagEnd     = 3
)

var (
	ErrBadHeader       = errors.New("wire: bad header")
	ErrZeroDistance    = errors.New("wire: zero back-reference distance")
	ErrDistanceTooFar  = errors.New("wire: distance exceeds output history")
	ErrWindowExceeded  = errors.New("wire: distance exceeds window capacity")
	ErrVarintOverflow  = errors.New("wire: varint overflow")
	ErrLengthMismatch  = errors.New("wire: original length mismatch")
	ErrChecksum        = errors.New("wire: checksum mismatch")
	ErrTrailingBytes   = errors.New("wire: trailing bytes after stream end")
	ErrTruncated       = errors.New("wire: truncated stream")
	ErrInvalidRecord   = errors.New("wire: invalid record")
	ErrOutputLimit     = errors.New("wire: output limit exceeded")
)

type OffsetError struct {
	Op     string
	Offset int
	Err    error
}

func (e *OffsetError) Error() string {
	return fmt.Sprintf("%s at compressed offset %d: %v", e.Op, e.Offset, e.Err)
}

func (e *OffsetError) Unwrap() error { return e.Err }

func Header() []byte { return []byte{'O', 'L', 'Z', 1} }

func Literal(dst, p []byte) []byte {
	dst = append(dst, TagLiteral)
	dst = AppendVarint(dst, uint64(len(p)))
	return append(dst, p...)
}

func Match(dst []byte, distance, length int) []byte {
	dst = append(dst, TagMatch)
	dst = AppendVarint(dst, uint64(distance))
	return AppendVarint(dst, uint64(length))
}

func Flush(dst []byte) []byte { return append(dst, TagFlush) }

func End(dst []byte, originalLength int, checksum uint64) []byte {
	dst = append(dst, TagEnd)
	dst = AppendVarint(dst, uint64(originalLength))
	return AppendVarint(dst, checksum)
}

func AppendVarint(dst []byte, v uint64) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

func ReadVarint(p []byte) (uint64, int, error) {
	var v uint64
	for i := 0; i < len(p); i++ {
		b := p[i]
		if i == 9 && b > 1 {
			return 0, i + 1, ErrVarintOverflow
		}
		if i >= 10 {
			return 0, 10, ErrVarintOverflow
		}
		v |= uint64(b&0x7f) << (7 * i)
		if b < 0x80 {
			return v, i + 1, nil
		}
	}
	return 0, len(p), io.ErrUnexpectedEOF
}

func Checksum(b []byte) uint64 {
	const offset64 = uint64(14695981039346656037)
	const prime64 = uint64(1099511628211)
	h := offset64
	for _, c := range b {
		h ^= uint64(c)
		h *= prime64
	}
	return h
}
