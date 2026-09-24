package wire

import "errors"

const (
	Magic     = "LZL1"
	Version   = uint64(1)
	MaxVarint = 10
)

const (
	RecordLiteral byte = 1
	RecordMatch   byte = 2
	RecordFlush   byte = 3
	RecordEnd     byte = 4
)

var (
	ErrBadMagic      = errors.New("lz77: bad stream magic")
	ErrBadVersion    = errors.New("lz77: unsupported stream version")
	ErrTruncated     = errors.New("lz77: truncated stream")
	ErrVarintTooLong = errors.New("lz77: varint exceeds ten bytes")
	ErrVarintRange   = errors.New("lz77: varint overflows uint64")
)

type OffsetError struct {
	Offset int
	Err    error
}

func (e *OffsetError) Error() string { return e.Err.Error() }

func (e *OffsetError) Unwrap() error { return e.Err }

func AppendHeader(dst []byte) []byte {
	dst = append(dst, Magic...)
	return AppendVarint(dst, Version)
}

func AppendVarint(dst []byte, value uint64) []byte {
	for value >= 0x80 {
		dst = append(dst, byte(value)|0x80)
		value >>= 7
	}
	return append(dst, byte(value))
}

func AppendLiteral(dst []byte, data []byte) []byte {
	dst = append(dst, RecordLiteral)
	dst = AppendVarint(dst, uint64(len(data)))
	return append(dst, data...)
}

func AppendMatch(dst []byte, distance, length uint64) []byte {
	dst = append(dst, RecordMatch)
	dst = AppendVarint(dst, distance)
	return AppendVarint(dst, length)
}

func AppendFlush(dst []byte) []byte { return append(dst, RecordFlush) }

func AppendEnd(dst []byte, size, checksum uint64) []byte {
	dst = append(dst, RecordEnd)
	dst = AppendVarint(dst, size)
	return AppendVarint(dst, checksum)
}

func ReadHeader(src []byte) (rest []byte, offset int, err error) {
	if len(src) < len(Magic) {
		return nil, len(src), ErrTruncated
	}
	if string(src[:len(Magic)]) != Magic {
		return nil, 0, ErrBadMagic
	}
	version, rest, used, err := ReadVarint(src[len(Magic):])
	if err != nil {
		return nil, len(Magic) + used, err
	}
	if version != Version {
		return nil, len(Magic) + used, ErrBadVersion
	}
	return rest, len(Magic) + used, nil
}

func ReadVarint(src []byte) (value uint64, rest []byte, offset int, err error) {
	for shift := uint(0); shift < 64; shift += 7 {
		if offset >= len(src) {
			return 0, nil, offset, ErrTruncated
		}
		b := src[offset]
		offset++
		if offset > MaxVarint {
			return 0, nil, offset, ErrVarintTooLong
		}
		if offset == MaxVarint && (b&^1 != 0) {
			if b&0x80 != 0 && b&0x7f <= 1 {
				return 0, nil, offset, ErrVarintTooLong
			}
			return 0, nil, offset, ErrVarintRange
		}
		value |= uint64(b&0x7f) << shift
		if b&0x80 == 0 {
			return value, src[offset:], offset, nil
		}
	}
	return 0, nil, offset, ErrVarintTooLong
}
