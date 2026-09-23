// Package wire defines the on-the-wire byte format of the LZ77 stream.
package wire

import (
	"errors"
	"hash/crc32"
)

const (
	TagHeader    byte = 0x00
	TagLiteral   byte = 0x01
	TagMatch     byte = 0x02
	TagEnd       byte = 0x03
	TagFlush     byte = 0x04
	Version      byte = 1
	MaxVarintLen      = 10
)

// Magic identifies an LZ stream immediately after the header tag.
var Magic = [4]byte{'O', 'N', 'L', 'Z'}

var (
	ErrMagic      = errors.New("wire: bad magic number")
	ErrVersion    = errors.New("wire: unsupported version")
	ErrVarint     = errors.New("wire: varint overflow or longer than 10 bytes")
	ErrBadTag     = errors.New("wire: unknown record tag")
	ErrTruncated  = errors.New("wire: truncated stream")
	ErrConfig     = errors.New("wire: invalid configuration")
	ErrDistZero   = errors.New("wire: back-reference distance is zero")
	ErrDistOutput = errors.New("wire: distance exceeds bytes already output")
	ErrDistWindow = errors.New("wire: distance exceeds window capacity")
	ErrLength     = errors.New("wire: declared total length mismatch")
	ErrChecksum   = errors.New("wire: checksum mismatch")
	ErrTrailing   = errors.New("wire: trailing bytes after end record")
	ErrOutputLimit = errors.New("wire: output exceeds configured limit")
)

// CorruptError wraps a format error with the byte offset inside the stream.
type CorruptError struct {
	Op     error
	Offset int
}

func (e *CorruptError) Error() string { return e.Op.Error() + " at byte " + itoa(e.Offset) }
func (e *CorruptError) Unwrap() error { return e.Op }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func AppendUvarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

// ReadUvarint returns the value, bytes consumed, whether the varint is
// complete, and an error on 64-bit overflow or a varint longer than 10 bytes.
func ReadUvarint(b []byte) (v uint64, n int, complete bool, err error) {
	for shift := uint(0); ; shift += 7 {
		if n >= len(b) {
			return 0, n, false, nil
		}
		c := b[n]
		n++
		if shift == 63 && c > 1 { // payload >1 (or a continuation => 11th byte)
			return 0, n, true, ErrVarint
		}
		v |= uint64(c&0x7f) << shift
		if c < 0x80 {
			return v, n, true, nil
		}
	}
}

func AppendHeader(b []byte, windowCap, chainLimit int) []byte {
	b = append(b, TagHeader)
	b = append(b, Magic[:]...)
	b = append(b, Version)
	b = AppendUvarint(b, uint64(windowCap))
	b = AppendUvarint(b, uint64(chainLimit))
	return b
}

func AppendLiteral(b, data []byte) []byte {
	b = AppendUvarint(append(b, TagLiteral), uint64(len(data)))
	return append(b, data...)
}

func AppendMatch(b []byte, dist, length int) []byte {
	b = AppendUvarint(append(b, TagMatch), uint64(dist))
	b = AppendUvarint(b, uint64(length))
	return b
}

func AppendEnd(b []byte, total int, crc uint32) []byte {
	b = AppendUvarint(append(b, TagEnd), uint64(total))
	return AppendUvarint(b, uint64(crc))
}

func NewCRC() uint32 { return crc32.ChecksumIEEE(nil) }
