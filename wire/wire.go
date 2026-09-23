// Package wire defines the on-the-wire byte format of the LZ77 stream.
package wire

import (
	"encoding/binary"
	"errors"
	"hash/fnv"
)

const (
	Magic   = "LZZ7"
	Version = 1
)

// Record tags. Stored as uvarint; current values all fit one byte.
const (
	TagLit   uint64 = 0
	TagMatch uint64 = 1
	TagFlush uint64 = 2
	TagEnd   uint64 = 3
)

var (
	// ErrBadMagic is reported when the stream does not start with Magic.
	ErrBadMagic = errors.New("wire: bad magic")
	// ErrBadVersion is reported when the header version is unsupported.
	ErrBadVersion = errors.New("wire: unsupported version")
	// ErrTruncated means the stream ended before a complete record was read.
	ErrTruncated = errors.New("wire: truncated stream")
	// ErrVarintOverflow means a uvarint exceeds 10 bytes or overflows uint64.
	ErrVarintOverflow = errors.New("wire: varint too long or overflow")
)

// AppendUvarint appends v to b using LEB128 unsigned encoding.
func AppendUvarint(b []byte, v uint64) []byte {
	var tmp [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(tmp[:], v)
	return append(b, tmp[:n]...)
}

// ReadUvarint reads one uvarint from the front of src. It returns the value,
// the remaining bytes, the number of bytes consumed and an error.
func ReadUvarint(src []byte) (uint64, []byte, int, error) {
	v, n := binary.Uvarint(src)
	if n == 0 {
		return 0, src, 0, ErrTruncated
	}
	if n < 0 {
		return 0, src, -n, ErrVarintOverflow
	}
	return v, src[n:], n, nil
}

// AppendHeader appends the stream header.
func AppendHeader(b []byte) []byte {
	b = append(b, Magic...)
	return AppendUvarint(b, Version)
}

// ParseHeader verifies and consumes the stream header.
func ParseHeader(src []byte) ([]byte, int, error) {
	if len(src) < len(Magic) {
		return src, 0, ErrTruncated
	}
	if string(src[:len(Magic)]) != Magic {
		return src, 0, ErrBadMagic
	}
	src = src[len(Magic):]
	v, rest, n, err := ReadUvarint(src)
	if err != nil {
		return src, len(Magic), err
	}
	if v != Version {
		return rest, len(Magic) + n, ErrBadVersion
	}
	return rest, len(Magic) + n, nil
}

// Checksum returns the FNV-1a 64-bit checksum of data.
func Checksum(data []byte) uint64 {
	h := fnv.New64a()
	h.Write(data)
	return h.Sum64()
}
