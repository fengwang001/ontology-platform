package wire

import (
	"errors"
	"hash/fnv"
)

const (
	Magic0   byte = 0x4C
	Magic1   byte = 0x5A
	Magic2   byte = 0x37
	Version  byte = 0x01
	TagLit   byte = 0
	TagMatch byte = 1
	TagFlush byte = 2
	TagTail  byte = 3
	MinMatch      = 3
)

var (
	ErrHeader   = errors.New("wire: bad magic or version")
	ErrTag      = errors.New("wire: unknown record tag")
	ErrVarint   = errors.New("wire: varint too long or overflows uint64")
	ErrValue    = errors.New("wire: invalid encoded value")
	ErrTruncated = errors.New("wire: compressed stream is truncated")
)

func Header() []byte { return []byte{Magic0, Magic1, Magic2, Version} }

func CheckHeader(p []byte) error {
	if len(p) < 4 || p[0] != Magic0 || p[1] != Magic1 || p[2] != Magic2 || p[3] != Version {
		return ErrHeader
	}
	return nil
}

func AppendUvarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

// ReadUvarint decodes at pos; max 10 bytes, rejects non-minimal overflow
// (10th byte may only use its low bit).
func ReadUvarint(p []byte, pos int) (uint64, int, error) {
	var v uint64
	for i := 0; i < 10; i++ {
		if pos >= len(p) {
			return 0, pos, ErrTruncated
		}
		c := p[pos]
		pos++
		if i == 9 && c > 1 {
			return 0, pos, ErrVarint
		}
		v |= uint64(c&0x7F) << (7 * i)
		if c < 0x80 {
			if i >= 9 && c >= 0x80 {
				return 0, pos, ErrVarint
			}
			return v, pos, nil
		}
	}
	return 0, pos, ErrVarint
}

func Checksum(data []byte) uint64 {
	h := fnv.New64a()
	h.Write(data)
	return h.Sum64()
}

func AppendLit(b, data []byte) []byte {
	b = append(b, TagLit)
	b = AppendUvarint(b, uint64(len(data)))
	return append(b, data...)
}

func AppendMatch(b []byte, dist, length int) []byte {
	b = append(b, TagMatch)
	b = AppendUvarint(b, uint64(dist))
	b = AppendUvarint(b, uint64(length))
	return b
}

func AppendTail(b []byte, totalLength int, checksum uint64) []byte {
	b = append(b, TagTail)
	b = AppendUvarint(b, uint64(totalLength))
	return AppendUvarint(b, checksum)
}
