// Package wire defines the self-describing LZ77 stream byte format.
package wire

import (
	"errors"
	"hash/crc32"
)

// Sentinel errors. All decode failures wrap one of these via CodeError.
var (
	ErrMagic      = errors.New("wire: bad magic")
	ErrVersion    = errors.New("wire: unsupported version")
	ErrBadTag     = errors.New("wire: unknown record tag")
	ErrZeroDist   = errors.New("wire: back-reference distance is zero")
	ErrDistOutput = errors.New("wire: distance exceeds bytes produced")
	ErrDistWindow = errors.New("wire: distance exceeds window capacity")
	ErrVarInt     = errors.New("wire: varint too long or overflows uint64")
	ErrLength     = errors.New("wire: stream length mismatch")
	ErrCRC        = errors.New("wire: checksum mismatch")
	ErrTrailing   = errors.New("wire: trailing bytes after end record")
	ErrTruncated  = errors.New("wire: truncated stream")
	ErrMaxOutput  = errors.New("wire: output size limit exceeded")
	ErrConfig     = errors.New("wire: invalid configuration")
)

// CodeError pairs a sentinel cause with the byte offset inside the stream.
type CodeError struct {
	Offset int
	Err    error
}

func (e *CodeError) Error() string { return e.Err.Error() }
func (e *CodeError) Unwrap() error { return e.Err }

// At wraps err with the stream offset at which it was detected.
func At(off int, err error) error { return &CodeError{Offset: off, Err: err} }

const (
	Magic   = "LZ77"
	Version = 1

	TagLiteral = 0
	TagMatch   = 1
	TagFlush   = 2
	TagEnd     = 3

	HeaderLen = 5 // 4 magic bytes + 1 version byte
)

// CRCTable is the IEEE CRC-32 used by end records.
var CRCTable = crc32.MakeTable(crc32.IEEE)

// AppendUvarint appends an unsigned LEB128 varint.
func AppendUvarint(dst []byte, v uint64) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

// DecodeUvarint decodes one varint. n is the consumed length; n==0 with an
// error means the buffer ended mid-varint, n>0 with err means a malformed
// (overlong/overflow) varint starting at the buffer front.
func DecodeUvarint(buf []byte) (v uint64, n int, err error) {
	var shift uint
	for i, b := range buf {
		if i == 10 {
			return 0, i, ErrVarInt
		}
		if i == 9 && b > 1 {
			return 0, i + 1, ErrVarInt
		}
		v |= uint64(b&0x7f) << shift
		n = i + 1
		if b&0x80 == 0 {
			return v, n, nil
		}
		shift += 7
	}
	return v, 0, ErrTruncated
}

// Header returns the fixed stream header.
func Header() []byte { return []byte{Magic[0], Magic[1], Magic[2], Magic[3], Version} }

// AppendEnd appends an end record: tag, original length, 4-byte big-endian CRC.
func AppendEnd(dst []byte, total uint64, sum uint32) []byte {
	dst = AppendUvarint(dst, TagEnd)
	dst = AppendUvarint(dst, total)
	return append(dst, byte(sum>>24), byte(sum>>16), byte(sum>>8), byte(sum))
}
