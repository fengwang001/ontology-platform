package wire

import "errors"

const (
	Magic       = "LZ77"
	Version     = 1
	TagLiteral  = 0
	TagMatch    = 1
	TagFlush    = 2
	TagEnd      = 3
	MinMatch    = 3
	MaxVarintN  = 10
)

var ErrVarintTooLong = errors.New("wire: varint exceeds 10 bytes")
var ErrVarintOverflow = errors.New("wire: varint overflows uint64")

func Header() []byte { return append([]byte(Magic), Version) }

func PutUvarint(dst []byte, value uint64) []byte {
	for value >= 0x80 {
		dst = append(dst, byte(value)|0x80)
		value >>= 7
	}
	return append(dst, byte(value))
}

func ReadUvarint(src []byte) (uint64, int, error) {
	var value uint64
	for i := 0; i < len(src) && i < MaxVarintN; i++ {
		b := src[i]
		if i == MaxVarintN-1 && b > 1 {
			return 0, i, ErrVarintOverflow
		}
		value |= uint64(b&0x7f) << (7 * i)
		if b < 0x80 {
			return value, i + 1, nil
		}
	}
	if len(src) >= MaxVarintN {
		return 0, MaxVarintN, ErrVarintTooLong
	}
	return 0, len(src), errors.New("wire: truncated varint")
}

func Literal(dst, data []byte) []byte {
	dst = append(dst, TagLiteral)
	dst = PutUvarint(dst, uint64(len(data)))
	return append(dst, data...)
}

func Match(dst []byte, distance, length int) []byte {
	dst = append(dst, TagMatch)
	dst = PutUvarint(dst, uint64(distance-1))
	return PutUvarint(dst, uint64(length-MinMatch))
}

func Flush(dst []byte) []byte { return append(dst, TagFlush) }

func End(dst []byte, size int, checksum uint64) []byte {
	dst = append(dst, TagEnd)
	dst = PutUvarint(dst, uint64(size))
	return PutUvarint(dst, checksum)
}

func Checksum(data []byte) uint64 {
	const offset64 = 14695981039346656037
	const prime64 = 1099511628211
	h := uint64(offset64)
	for _, b := range data {
		h ^= uint64(b)
		h *= prime64
	}
	return h
}
