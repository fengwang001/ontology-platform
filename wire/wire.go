package wire

import "errors"

const (
	Magic       = "LZ7"
	Version byte = 1
)

const (
	TagLiteral byte = iota
	TagMatch
	TagFlush
	TagEnd
)

type CorruptError struct {
	Offset int
	Kind   error
}

func (e *CorruptError) Error() string { return e.Kind.Error() }
func (e *CorruptError) Unwrap() error { return e.Kind }

var (
	ErrHeader   = errors.New("invalid stream header")
	ErrDistance = errors.New("zero back-reference distance")
	ErrVarint   = errors.New("invalid variable-length integer")
	ErrTrailing = errors.New("trailing bytes after stream end")
)

func AppendHeader(dst []byte) []byte { return append(append(dst, Magic...), Version) }

func AppendLiteral(dst, p []byte) []byte {
	dst = appendUvarint(append(dst, TagLiteral), uint64(len(p)))
	return append(dst, p...)
}

func AppendMatch(dst []byte, distance, length int) []byte {
	dst = appendUvarint(append(dst, TagMatch), uint64(distance))
	return appendUvarint(dst, uint64(length))
}

func AppendFlush(dst []byte) []byte { return append(dst, TagFlush) }

func AppendEnd(dst []byte, size int, checksum uint32) []byte {
	dst = appendUvarint(append(dst, TagEnd), uint64(size))
	return appendUvarint(dst, uint64(checksum))
}

func appendUvarint(dst []byte, v uint64) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

// ReadUvarint returns the value, bytes consumed, and whether a complete value exists.
func ReadUvarint(src []byte) (uint64, int, bool) {
	var v uint64
	for i := 0; i < len(src) && i < 10; i++ {
		b := src[i]
		if i == 9 && b > 1 {
			return 0, 0, false
		}
		v |= uint64(b&0x7f) << (7 * i)
		if b < 0x80 {
			return v, i + 1, true
		}
	}
	return 0, 0, false
}

func Bad(kind error, offset int) error { return &CorruptError{Offset: offset, Kind: kind} }
