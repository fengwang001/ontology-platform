// Package vint encodes and decodes unsigned varints: 7 data bits per
// byte, high bit as continuation flag, little-endian group order.
package vint

import (
	"errors"
	"sync/atomic"
)

// Sentinel errors; each rejected decode fails with exactly one of them.
var (
	ErrIncomplete = errors.New("vint: incomplete input")
	ErrNonMinimal = errors.New("vint: non-minimal encoding")
	ErrOverflow   = errors.New("vint: value overflows uint64")
)

// maxLen is the longest legal encoding: ceil(64/7) = 10 bytes.
const maxLen = 10

// state is the only shared mutable state; lastRead records how many
// bytes the most recent decode consumed. Never exposed publicly.
var state struct{ lastRead atomic.Int64 }

// PutUvarint encodes v into buf and returns the number of bytes
// written. buf must be at least maxLen bytes to hold any uint64.
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

// Uvarint decodes one value from buf, returning the value and the
// number of bytes consumed. On error both are zero.
func Uvarint(buf []byte) (uint64, int, error) {
	var v uint64
	for i := 0; ; i++ {
		if i >= len(buf) {
			return 0, 0, ErrIncomplete
		}
		if i >= maxLen {
			return 0, 0, ErrOverflow
		}
		b := buf[i]
		if i == maxLen-1 && b > 1 {
			return 0, 0, ErrOverflow
		}
		v |= uint64(b&0x7f) << (7 * i)
		if b < 0x80 {
			if i > 0 && b == 0 {
				return 0, 0, ErrNonMinimal
			}
			state.lastRead.Store(int64(i + 1))
			return v, i + 1, nil
		}
	}
}
