package wire

import (
	"errors"
	"io"
)

const (
	Magic   = "LZ77O"
	Version = byte(1)

	TypeLiteral = 0
	TypeMatch   = 1
	TypeFlush   = 2
	TypeTail    = 3
)

var (
	ErrTruncated = errors.New("lz77: truncated stream")
	ErrVarint    = errors.New("lz77: invalid varint")
)

func Header() []byte { return append([]byte(Magic), Version) }

func AppendUvarint(dst []byte, value uint64) []byte {
	for value >= 0x80 {
		dst = append(dst, byte(value)|0x80)
		value >>= 7
	}
	return append(dst, byte(value))
}

func ReadUvarint(r io.ByteReader) (uint64, int, error) {
	var value uint64
	for i := uint(0); i < 10; i++ {
		b, err := r.ReadByte()
		if err != nil {
			if err == io.EOF && i == 0 {
				return 0, 0, io.EOF
			}
			if err == io.EOF {
				return 0, int(i), ErrTruncated
			}
			return 0, int(i), err
		}
		if i == 9 && b > 1 {
			return 0, int(i) + 1, ErrVarint
		}
		value |= uint64(b&0x7f) << (7 * i)
		if b < 0x80 {
			return value, int(i) + 1, nil
		}
	}
	return 0, 10, ErrVarint
}

func AppendLiteral(dst, p []byte) []byte {
	dst = AppendUvarint(append(dst, TypeLiteral), uint64(len(p)))
	return append(dst, p...)
}

func AppendMatch(dst []byte, distance, length uint64) []byte {
	dst = AppendUvarint(append(dst, TypeMatch), distance)
	return AppendUvarint(dst, length)
}

func AppendFlush(dst []byte) []byte { return append(dst, TypeFlush) }

func AppendTail(dst []byte, totalLength, checksum uint64) []byte {
	dst = AppendUvarint(append(dst, TypeTail), totalLength)
	return AppendUvarint(dst, checksum)
}
