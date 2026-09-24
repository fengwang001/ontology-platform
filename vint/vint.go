// Package vint encodes and decodes unsigned base-128 varints.
// Every byte carries 7 payload bits; the high bit means "more bytes follow".
// It depends on no other package in this module.
package vint

import (
	"errors"
	"sync/atomic"
)

var (
	// ErrIncomplete means the last byte still promises another byte.
	ErrIncomplete = errors.New("vint: incomplete varint")
	// ErrNonShortest means a small value was expressed with extra zero bytes.
	ErrNonShortest = errors.New("vint: non-shortest varint form")
	// ErrOverflow means more than 10 bytes or bits outside the uint64 range.
	ErrOverflow = errors.New("vint: varint overflows uint64")
)

const maxBytes = 10

// Decoder decodes varints. The unexported lastRead counter records how many
// bytes the most recent successful Uvarint consumed; it never appears in any
// public interface.
type Decoder struct {
	lastRead atomic.Uint64
}

func NewDecoder() *Decoder { return &Decoder{} }

// PutUvarint appends the shortest varint encoding of x to buf.
func PutUvarint(buf []byte, x uint64) []byte {
	for x >= 0x80 {
		buf = append(buf, byte(x)|0x80)
		x >>= 7
	}
	return append(buf, byte(x))
}

// Uvarint decodes one varint from buf, returning the value and the exact
// number of bytes consumed. The consumed count is also stored in the
// decoder's unexported counter. Failures never partially consume: the
// returned count is 0 on error.
func (d *Decoder) Uvarint(buf []byte) (uint64, int, error) {
	var x uint64
	for i := 0; i < len(buf) && i < maxBytes; i++ {
		b := buf[i]
		if i == maxBytes-1 {
			if b > 1 {
				return 0, 0, ErrOverflow
			}
			if b&0x80 != 0 {
				return 0, 0, ErrOverflow
			}
		}
		x |= uint64(b&0x7f) << (7 * i)
		if b&0x80 == 0 {
			if i > 0 && b == 0 {
				return 0, 0, ErrNonShortest
			}
			d.lastRead.Store(uint64(i + 1))
			return x, i + 1, nil
		}
	}
	if len(buf) > maxBytes || (len(buf) == maxBytes && buf[maxBytes-1]&0x80 != 0) {
		return 0, 0, ErrOverflow
	}
	return 0, 0, ErrIncomplete
}
