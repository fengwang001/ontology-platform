package wire

import "errors"

const (
	Magic   = "LZ77"
	Version = uint64(1)
)

const (
	TagLiteral = 0
	TagMatch   = 1
	TagFlush   = 2
	TagEnd     = 3
)

var (
	ErrBadMagic   = errors.New("wire: bad magic")
	ErrBadVersion = errors.New("wire: bad version")
	ErrVarintLong = errors.New("wire: varint exceeds 10 bytes")
	ErrVarintBig  = errors.New("wire: varint overflows uint64")
)

func AppendUvarint(dst []byte, v uint64) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

// ReadUvarint returns the value, bytes consumed, and whether a complete varint
// is present. More-than-ten bytes or uint64 overflow is reported separately.
func ReadUvarint(src []byte) (uint64, int, error) {
	var v uint64
	for i := 0; i < len(src) && i < 10; i++ {
		b := src[i]
		if i == 9 && b > 1 {
			return 0, i + 1, ErrVarintBig
		}
		v |= uint64(b&0x7f) << (7 * i)
		if b < 0x80 {
			return v, i + 1, nil
		}
	}
	if len(src) >= 11 {
		return 0, 10, ErrVarintLong
	}
	return 0, 0, nil
}

func AppendHeader(dst []byte) []byte {
	dst = append(dst, Magic...)
	return AppendUvarint(dst, Version)
}

func AppendLiteral(dst []byte, lit []byte) []byte {
	dst = append(dst, TagLiteral)
	dst = AppendUvarint(dst, uint64(len(lit)))
	return append(dst, lit...)
}

func AppendMatch(dst []byte, distance, length int) []byte {
	dst = append(dst, TagMatch)
	dst = AppendUvarint(dst, uint64(distance))
	return AppendUvarint(dst, uint64(length))
}

func AppendFlush(dst []byte) []byte { return append(dst, TagFlush) }

func AppendEnd(dst []byte, originalLength, checksum uint64) []byte {
	dst = append(dst, TagEnd)
	dst = AppendUvarint(dst, originalLength)
	return AppendUvarint(dst, checksum)
}
