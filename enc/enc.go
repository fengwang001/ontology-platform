// Package enc implements protobuf wire-format primitives: varint, zigzag,
// fixed32/64 and field keys. It depends on nothing outside the stdlib.
package enc

import (
	"encoding/binary"
	"errors"
)

// ErrOverflow is returned when a varint exceeds 10 bytes or 64 bits.
var ErrOverflow = errors.New("enc: varint overflow")

// ErrTruncated is returned when the input ends before a value is complete.
var ErrTruncated = errors.New("enc: truncated input")

// EncodeVarint encodes u as unsigned LEB128 (7 bits per group, LSB first).
func EncodeVarint(u uint64) []byte {
	var b [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(b[:], u)
	out := make([]byte, n)
	copy(out, b[:n])
	return out
}

// DecodeVarint decodes one varint from b, returning the value and the number
// of bytes consumed. Non-canonical forms (e.g. 80 00) are accepted; more
// than 10 bytes or bits beyond uint64 are ErrOverflow.
func DecodeVarint(b []byte) (uint64, int, error) {
	var u uint64
	for i := 0; i < len(b); i++ {
		if i >= 10 {
			return 0, 0, ErrOverflow
		}
		c := b[i]
		if i == 9 && c > 1 { // 10th byte may carry at most 1 bit
			return 0, 0, ErrOverflow
		}
		u |= uint64(c&0x7f) << (7 * i)
		if c&0x80 == 0 {
			return u, i + 1, nil
		}
	}
	return 0, 0, ErrTruncated
}

// Zigzag32 maps a signed 32-bit value to unsigned: (n<<1) ^ (n>>31).
func Zigzag32(n int32) uint32 { return uint32(n<<1) ^ uint32(n>>31) }

// Unzigzag32 inverts Zigzag32: (u>>1) ^ -(u&1).
func Unzigzag32(u uint32) int32 { return int32(u>>1) ^ -int32(u&1) }

// Zigzag64 maps a signed 64-bit value to unsigned: (n<<1) ^ (n>>63).
func Zigzag64(n int64) uint64 { return uint64(n<<1) ^ uint64(n>>63) }

// Unzigzag64 inverts Zigzag64: (u>>1) ^ -(u&1).
func Unzigzag64(u uint64) int64 { return int64(u>>1) ^ -int64(u&1) }

// PutFixed32 appends v as 4 little-endian bytes.
func PutFixed32(dst []byte, v uint32) []byte {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], v)
	return append(dst, b[:]...)
}

// PutFixed64 appends v as 8 little-endian bytes.
func PutFixed64(dst []byte, v uint64) []byte {
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], v)
	return append(dst, b[:]...)
}

// GetFixed32 reads 4 little-endian bytes from b.
func GetFixed32(b []byte) (uint32, error) {
	if len(b) < 4 {
		return 0, ErrTruncated
	}
	return binary.LittleEndian.Uint32(b), nil
}

// GetFixed64 reads 8 little-endian bytes from b.
func GetFixed64(b []byte) (uint64, error) {
	if len(b) < 8 {
		return 0, ErrTruncated
	}
	return binary.LittleEndian.Uint64(b), nil
}

// KeyNum extracts the field number from a field key.
func KeyNum(key uint64) int { return int(key >> 3) }

// KeyWire extracts the wire type (low 3 bits) from a field key.
func KeyWire(key uint64) int { return int(key & 0x07) }
