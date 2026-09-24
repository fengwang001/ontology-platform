// Package vint encodes and decodes unsigned varints: 7 data bits per byte,
// the high bit means "more follows", groups are little-endian.
package vint

import (
	"errors"
	"sync/atomic"
)

// Decoding failures, each a distinct sentinel error.
var (
	ErrIncomplete = errors.New("vint: incomplete input")
	ErrNonMinimal = errors.New("vint: non-minimal encoding")
	ErrOverflow   = errors.New("vint: value overflows uint64")
)

// lastRead records how many bytes the most recent Uvarint call read.
var lastRead atomic.Int64

// PutUvarint encodes v into buf (which must hold at least 10 bytes) and
// returns the number of bytes written.
func PutUvarint(buf []byte, v uint64) int {
	i := 0
	for v >= 0x80 {
		buf[i] = byte(v) | 0x80
		v >>= 7
		i++
	}
	buf[i] = byte(v)
	return i + 1
}

// Uvarint decodes one value from buf, returning the value and the number of
// bytes consumed. Non-minimal, truncated and overflowing inputs are rejected
// with distinct sentinel errors.
func Uvarint(buf []byte) (uint64, int, error) {
	var v uint64
	for i := 0; i < len(buf); i++ {
		b := buf[i]
		if i == 9 && b > 1 { // 10th byte may only carry bit 63
			lastRead.Store(int64(i + 1))
			return 0, 0, ErrOverflow
		}
		if b < 0x80 {
			if i > 0 && b == 0 { // trailing zero group: not minimal
				lastRead.Store(int64(i + 1))
				return 0, 0, ErrNonMinimal
			}
			lastRead.Store(int64(i + 1))
			return v | uint64(b)<<(7*i), i + 1, nil
		}
		v |= uint64(b&0x7f) << (7 * i)
	}
	lastRead.Store(int64(len(buf)))
	return 0, 0, ErrIncomplete
}
