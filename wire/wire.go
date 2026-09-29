package wire

import "errors"

const (
	TagLiteral = iota
	TagMatch
	TagFlush
	TagEnd
)

const (
	Version = 1
	Magic0  = 'L'
	Magic1  = 'Z'
	Magic2  = '7'
)

var (
	ErrBadMagic        = errors.New("lz77wire: bad magic")
	ErrBadVersion      = errors.New("lz77wire: unsupported version")
	ErrVarintOverflow  = errors.New("lz77wire: varint overflow")
	ErrUnexpectedBytes = errors.New("lz77wire: bytes after end record")
)

type OffsetError struct {
	Offset int
	Err    error
}

func (e *OffsetError) Error() string { return e.Err.Error() }
func (e *OffsetError) Unwrap() error { return e.Err }

func At(offset int, err error) error { return &OffsetError{Offset: offset, Err: err} }

func Header() []byte { return []byte{Magic0, Magic1, Magic2, Version} }

func ReadHeader(p []byte) error {
	if len(p) < 4 {
		return errors.New("lz77wire: truncated header")
	}
	if p[0] != Magic0 || p[1] != Magic1 || p[2] != Magic2 {
		return ErrBadMagic
	}
	if p[3] != Version {
		return ErrBadVersion
	}
	return nil
}

func AppendUvarint(p []byte, v uint64) []byte {
	var b [10]byte
	n := 0
	for v >= 0x80 {
		b[n] = byte(v) | 0x80
		v >>= 7
		n++
	}
	b[n] = byte(v)
	return append(p, b[:n+1]...)
}

func ReadUvarint(p []byte) (uint64, int, error) {
	var v uint64
	for i := 0; i < len(p) && i < 10; i++ {
		c := p[i]
		if i == 9 && c > 1 {
			return 0, i + 1, ErrVarintOverflow
		}
		v |= uint64(c&0x7f) << (7 * i)
		if c < 0x80 {
			return v, i + 1, nil
		}
	}
	if len(p) >= 10 {
		return 0, 10, ErrVarintOverflow
	}
	return 0, len(p), errors.New("lz77wire: truncated varint")
}

func AppendLiteral(p []byte, lit []byte) []byte {
	p = AppendUvarint(p, TagLiteral)
	p = AppendUvarint(p, uint64(len(lit)))
	return append(p, lit...)
}

func AppendMatch(p []byte, dist, length int) []byte {
	p = AppendUvarint(p, TagMatch)
	p = AppendUvarint(p, uint64(dist))
	return AppendUvarint(p, uint64(length))
}

func AppendEnd(p []byte, size int, sum uint64) []byte {
	p = AppendUvarint(p, TagEnd)
	p = AppendUvarint(p, uint64(size))
	return AppendUvarint(p, sum)
}

func Checksum(p []byte) uint64 {
	const (
		offset64 = 14695981039346656037
		prime64  = 1099511628211
	)
	h := uint64(offset64)
	for _, c := range p {
		h ^= uint64(c)
		h *= prime64
	}
	return h
}
