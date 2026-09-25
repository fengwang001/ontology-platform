// Package wire defines the self-describing LZ77 stream byte format.
package wire

import (
	"errors"
	"hash/fnv"
)

const (
	Magic   uint64 = 0xC0FFEE
	Version uint64 = 1

	TagLiteral byte = 0
	TagMatch   byte = 1
	TagFlush   byte = 2
	TagEnd     byte = 3
)

// Sentinel errors classify every detectable stream corruption.
var (
	ErrBadMagic         = errors.New("wire: bad magic")
	ErrBadVersion       = errors.New("wire: unsupported version")
	ErrVarint           = errors.New("wire: varint too long or overflows 64 bits")
	ErrTruncated        = errors.New("wire: truncated stream")
	ErrZeroDistance     = errors.New("wire: back-reference distance is zero")
	ErrDistBeyondOutput = errors.New("wire: distance exceeds produced bytes")
	ErrDistBeyondWindow = errors.New("wire: distance exceeds window capacity")
	ErrLengthMismatch   = errors.New("wire: trailer length mismatch")
	ErrChecksum         = errors.New("wire: checksum mismatch")
	ErrTrailingBytes    = errors.New("wire: trailing bytes after end marker")
	ErrOutputLimit      = errors.New("wire: output size limit exceeded")
)

// AppendUvarint appends an unsigned LEB128 varint.
func AppendUvarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

// Uvarint decodes one varint. It returns ErrTruncated when more bytes are
// needed and ErrVarint when the encoding exceeds 10 bytes or overflows.
func Uvarint(b []byte) (uint64, int, error) {
	var x uint64
	for i := 0; i < len(b) && i < 10; i++ {
		c := b[i]
		if i == 9 && c > 1 {
			return 0, 0, ErrVarint
		}
		x |= uint64(c&0x7f) << (7 * i)
		if c < 0x80 {
			return x, i + 1, nil
		}
	}
	if len(b) >= 10 {
		return 0, 0, ErrVarint
	}
	return 0, 0, ErrTruncated
}

// AppendHeader writes the stream header.
func AppendHeader(b []byte) []byte {
	b = AppendUvarint(b, Magic)
	return AppendUvarint(b, Version)
}

// ParseHeader consumes the stream header.
func ParseHeader(b []byte) (int, error) {
	m, n, err := Uvarint(b)
	if err != nil {
		return 0, err
	}
	if m != Magic {
		return 0, ErrBadMagic
	}
	v, k, err := Uvarint(b[n:])
	if err != nil {
		return 0, err
	}
	if v != Version {
		return 0, ErrBadVersion
	}
	return n + k, nil
}

// NewChecksum returns the running FNV-1a 64-bit checksum of the original data.
func NewChecksum() interface {
	Write([]byte) (int, error)
	Sum64() uint64
} {
	return fnv.New64a()
}
