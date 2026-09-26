package wire

import (
	"errors"
	"fmt"
)

const (
	Magic   uint64 = 0x4f4c5a
	Version uint64 = 1
)

const (
	TagLiteral = iota
	TagMatch
	TagFlush
	TagEnd
)

var (
	ErrMagic          = errors.New("olz: bad magic")
	ErrVersion        = errors.New("olz: unsupported version")
	ErrTruncated      = errors.New("olz: truncated stream")
	ErrVarint         = errors.New("olz: invalid varint")
	ErrDistanceZero   = errors.New("olz: distance is zero")
	ErrDistancePast   = errors.New("olz: distance beyond produced output")
	ErrDistanceWindow = errors.New("olz: distance beyond window")
	ErrLength         = errors.New("olz: declared length is invalid")
	ErrChecksum       = errors.New("olz: checksum mismatch")
	ErrTrailing       = errors.New("olz: trailing bytes after end")
	ErrConfig         = errors.New("olz: invalid configuration")
	ErrOutputLimit    = errors.New("olz: output limit exceeded")
)

type FrameError struct {
	Offset int64
	Err    error
}

func (e *FrameError) Error() string {
	return fmt.Sprintf("%s at byte %d", e.Err.Error(), e.Offset)
}

func (e *FrameError) Unwrap() error { return e.Err }

func At(offset int64, err error) error {
	if err == nil {
		return nil
	}
	return &FrameError{Offset: offset, Err: err}
}

func AppendUvarint(dst []byte, v uint64) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

func AppendHeader(dst []byte, window uint64) []byte {
	dst = AppendUvarint(dst, Magic)
	dst = AppendUvarint(dst, Version)
	return AppendUvarint(dst, window)
}

func AppendLiteral(dst []byte, p []byte) []byte {
	dst = AppendUvarint(dst, TagLiteral)
	dst = AppendUvarint(dst, uint64(len(p)))
	return append(dst, p...)
}

func AppendMatch(dst []byte, distance, length uint64) []byte {
	dst = AppendUvarint(dst, TagMatch)
	dst = AppendUvarint(dst, distance)
	return AppendUvarint(dst, length)
}

func AppendFlush(dst []byte) []byte {
	return AppendUvarint(dst, TagFlush)
}

func AppendEnd(dst []byte, originalLength, checksum uint64) []byte {
	dst = AppendUvarint(dst, TagEnd)
	dst = AppendUvarint(dst, originalLength)
	return AppendUvarint(dst, checksum)
}

type UvarintReader struct {
	value uint64
	bits  uint
	count int
}

func (r *UvarintReader) Reset() { *r = UvarintReader{} }

func (r *UvarintReader) Read(p []byte) (uint64, int, error) {
	for i, b := range p {
		if r.count == 10 && b != 0 || r.count >= 10 {
			return 0, i + 1, ErrVarint
		}
		r.value |= uint64(b&0x7f) << r.bits
		r.bits += 7
		r.count++
		if b < 0x80 {
			v := r.value
			r.Reset()
			return v, i + 1, nil
		}
	}
	return 0, 0, nil
}
