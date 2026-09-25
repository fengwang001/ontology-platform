// Package wire defines the self-described LZ77 byte stream format.
package wire

import (
	"errors"
	"fmt"
	"hash/crc32"
)

// Record tags.
const (
	TagLit   byte = 0x00 // literal run: varint length, then raw bytes
	TagMatch byte = 0x01 // back reference: varint distance, varint length
	TagFlush byte = 0x02 // flush boundary, no payload
	TagEnd   byte = 0x03 // stream end: varint original size, varint checksum
)

// Header is the fixed stream prefix.
var Header = []byte{'O', 'L', 'Z', 1}

// Distinguishable sentinel errors. OffsetError wraps any parse error with the
// absolute byte offset in the compressed stream at which it was detected.
var (
	ErrMagic       = errors.New("wire: bad magic or version")
	ErrTruncated   = errors.New("wire: truncated stream")
	ErrZeroDist    = errors.New("wire: back-reference distance is zero")
	ErrDistWindow  = errors.New("wire: distance exceeds window capacity")
	ErrDistHistory = errors.New("wire: distance exceeds available history")
	ErrVarintLong  = errors.New("wire: varint longer than 10 bytes")
	ErrVarintRange = errors.New("wire: varint overflows 64 bits")
	ErrBadTag      = errors.New("wire: unknown record tag")
	ErrZeroLen     = errors.New("wire: zero-length record payload")
	ErrSize        = errors.New("wire: end record size mismatch")
	ErrChecksum    = errors.New("wire: checksum mismatch")
	ErrTrailing    = errors.New("wire: trailing bytes after end record")
	ErrTerminal    = errors.New("wire: decoder already in terminal error state")
	ErrOutputLimit = errors.New("wire: decompressed output exceeds limit")
	ErrBadConfig   = errors.New("wire: invalid configuration")
)

// OffsetError pairs an error with the compressed-stream byte offset.
type OffsetError struct {
	Offset int
	Err    error
}

func (e *OffsetError) Error() string {
	return fmt.Sprintf("%v at compressed offset %d", e.Err, e.Offset)
}

func (e *OffsetError) Unwrap() error { return e.Err }

// PutUvarint appends an unsigned LEB128 value to b.
func PutUvarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

// ReadUvarint consumes one LEB128 value from data. It enforces the 10-byte /
// 64-bit limits and reports both the value and the bytes consumed.
func ReadUvarint(data []byte) (uint64, int, error) {
	var v uint64
	for i := 0; i < len(data) && i < 10; i++ {
		b := data[i]
		if i == 9 && b > 1 {
			return 0, i + 1, ErrVarintRange
		}
		v |= uint64(b&0x7f) << (7 * i)
		if b < 0x80 {
			return v, i + 1, nil
		}
	}
	if len(data) >= 10 {
		return 0, 10, ErrVarintLong
	}
	return 0, len(data), ErrTruncated
}

// Checksum returns the stream checksum of the original data.
func Checksum(p []byte) uint32 { return crc32.ChecksumIEEE(p) }
