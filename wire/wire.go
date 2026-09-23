package wire

import (
	"errors"
	"fmt"
	"hash/fnv"
)

const (
	TagLiteral = 0
	TagMatch   = 1
	TagFlush   = 2
	TagEnd     = 3
)

var (
	ErrTruncated       = errors.New("compressed stream is truncated")
	ErrBadHeader       = errors.New("bad stream magic or version")
	ErrZeroDistance    = errors.New("back-reference distance is zero")
	ErrDistanceTooFar  = errors.New("back-reference distance exceeds decoded output")
	ErrWindowOverflow  = errors.New("back-reference distance exceeds window capacity")
	ErrVarInt          = errors.New("varint is too long or overflows uint64")
	ErrLengthMismatch  = errors.New("stream length does not match decoded output")
	ErrChecksum        = errors.New("stream checksum does not match decoded output")
	ErrTrailingBytes   = errors.New("trailing bytes after stream end")
	ErrInvalidArgument = errors.New("invalid configuration")
)

type OffsetError struct {
	Offset int64
	Err    error
}

func (e OffsetError) Error() string {
	return fmt.Sprintf("%s at byte %d", e.Err, e.Offset)
}

func (e OffsetError) Unwrap() error { return e.Err }

func Header() []byte {
	return []byte{'O', 'N', 'L', 'Z', 1}
}

func CheckHeader(data []byte) (int, error) {
	if len(data) < 5 {
		return len(data), OffsetError{int64(len(data)), ErrTruncated}
	}
	if string(data[:4]) != "ONLZ" || data[4] != 1 {
		return 0, OffsetError{0, ErrBadHeader}
	}
	return 5, nil
}

func AppendUvarint(dst []byte, value uint64) []byte {
	var buf [10]byte
	size := PutUvarint(buf[:], value)
	return append(dst, buf[:size]...)
}

func PutUvarint(buf []byte, value uint64) int {
	for value >= 0x80 {
		buf[0] = byte(value) | 0x80
		value >>= 7
		buf = buf[1:]
	}
	buf[0] = byte(value)
	return cap(buf) - len(buf) + 1
}

func ReadUvarint(data []byte, offset int) (uint64, int, error) {
	start := offset
	var value uint64
	for shift := uint(0); shift < 64; shift += 7 {
		if offset >= len(data) {
			return 0, offset, OffsetError{int64(offset), ErrTruncated}
		}
		b := data[offset]
		offset++
		if shift == 63 && b > 1 {
			return 0, offset, OffsetError{int64(start), ErrVarInt}
		}
		value |= uint64(b&0x7f) << shift
		if b < 0x80 {
			return value, offset, nil
		}
	}
	return 0, offset, OffsetError{int64(offset), ErrVarInt}
}

func Checksum(data []byte) uint64 {
	h := fnv.New64a()
	_, _ = h.Write(data)
	return h.Sum64()
}
