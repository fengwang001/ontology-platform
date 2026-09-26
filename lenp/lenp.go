// Package lenp encodes/decodes a 4-byte little-endian unsigned length prefix.
// It depends on no other package in this module.
package lenp

import (
	"encoding/binary"
	"errors"
	"math"
)

// Width is the fixed size of a length prefix in bytes.
const Width = 4

var (
	// ErrShortBuffer is returned when fewer than 4 bytes are available
	// to read a length prefix.
	ErrShortBuffer = errors.New("lenp: short buffer: need 4 bytes to read length prefix")
	// ErrLengthOutOfRange is returned when the prefix value does not fit
	// into an int on the running platform.
	ErrLengthOutOfRange = errors.New("lenp: length prefix value out of int range")
)

// PutLength returns a fresh 4-byte little-endian encoding of n.
// n is always a byte length, so callers never pass negative values.
func PutLength(n int) []byte {
	b := make([]byte, Width)
	binary.LittleEndian.PutUint32(b, uint32(n))
	return b
}

// GetLength reads a 4-byte little-endian length from the first bytes of b.
// It returns ErrShortBuffer when b holds fewer than Width bytes and
// ErrLengthOutOfRange when the unsigned value does not fit in int.
func GetLength(b []byte) (int, error) {
	if len(b) < Width {
		return 0, ErrShortBuffer
	}
	v := binary.LittleEndian.Uint32(b[:Width])
	if uint64(v) > uint64(math.MaxInt) {
		return 0, ErrLengthOutOfRange
	}
	return int(v), nil
}
