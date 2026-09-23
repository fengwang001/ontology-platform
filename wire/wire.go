package wire

import "errors"

const Version = 1

const (
	TagLiteral = 0x00
	TagMatch   = 0x01
	TagFlush   = 0x02
	TagEnd     = 0x03
)

var (
	ErrTruncated = errors.New("wire: truncated stream")
	ErrBadVarint = errors.New("wire: varint too long or overflow")
	ErrBadLength = errors.New("wire: invalid zero length")
)

func Header() []byte { return []byte{'L', 'Z', Version} }

func PutUvarint(dst []byte, v uint64) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

func Uvarint(buf []byte) (uint64, int, error) {
	var x uint64
	for i, b := range buf {
		if i == 9 && b > 1 || i >= 10 {
			return 0, i + 1, ErrBadVarint
		}
		if b < 0x80 {
			if i == 9 {
				x |= uint64(b) << 63
			} else {
				x |= uint64(b) << (7 * i)
			}
			return x, i + 1, nil
		}
		x |= uint64(b&0x7f) << (7 * i)
	}
	return 0, len(buf), ErrTruncated
}

func Literal(dst, p []byte) []byte {
	if len(p) == 0 {
		return dst
	}
	dst = append(dst, TagLiteral)
	dst = PutUvarint(dst, uint64(len(p)))
	return append(dst, p...)
}

func Match(dst []byte, distance, length uint64) []byte {
	if distance == 0 || length == 0 {
		panic(ErrBadLength)
	}
	dst = append(dst, TagMatch)
	dst = PutUvarint(dst, distance)
	return PutUvarint(dst, length)
}

func Flush(dst []byte) []byte { return append(dst, TagFlush) }

func End(dst []byte, originalLength, checksum uint64) []byte {
	dst = append(dst, TagEnd)
	dst = PutUvarint(dst, originalLength)
	return PutUvarint(dst, checksum)
}

func RecordSize(tag byte, fields ...uint64) int {
	n := 1
	for _, v := range fields {
		n += varintLen(v)
	}
	if tag == TagLiteral {
		n += int(fields[0])
	}
	return n
}

func varintLen(v uint64) int {
	n := 1
	for v >= 0x80 {
		v >>= 7
		n++
	}
	return n
}
