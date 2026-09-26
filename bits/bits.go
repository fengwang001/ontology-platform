// Package bits implements fixed-width bitfield packing (LSB-first) and a
// bit-indexed view over a []byte bitmap. It depends on no other package.
package bits

import (
	"errors"
	"sync"
)

// bitMu serializes read-modify-write access to byte slices passed to the
// bitmap functions; a plain []byte has nowhere to keep a per-slice lock.
var bitMu sync.RWMutex

// Distinct sentinel errors so callers can judge every failure mode.
var (
	// ErrValueOverflow: a field value is outside its width's range.
	ErrValueOverflow = errors.New("bits: field value out of width range")
	// ErrBitIndex: a bitmap index is negative or >= 8*len(bitmap).
	ErrBitIndex = errors.New("bits: bit index out of range")
	// ErrBadWidth: a width is 0, exceeds 64, or field widths sum past 64.
	ErrBadWidth = errors.New("bits: invalid field width")
)

// Field is one fixed-width bitfield. Width is in bits 1..64.
type Field struct {
	Width  int
	Value  int64
	Signed bool
}

// inRange reports whether v fits in a width-bit field. Ranges:
// unsigned [0, 2^w-1]; signed [-2^(w-1), 2^(w-1)-1].
func inRange(f Field) bool {
	w := uint(f.Width)
	if f.Signed {
		if f.Width == 64 {
			return true // every int64 is representable in 64 bits
		}
		b := int64(uint64(1) << (w - 1))
		return f.Value >= -b && f.Value <= b-1
	}
	if f.Value < 0 {
		return false
	}
	if f.Width == 64 {
		return true // non-negative int64 fits in unsigned 64-bit field
	}
	return uint64(f.Value) < (uint64(1) << w)
}

// PackFields packs fields LSB-first into one uint64: field i starts at bit
// offset equal to the sum of earlier widths. All widths and values are
// validated first, so any failure returns (0, err) and touches nothing.
func PackFields(fields []Field) (uint64, error) {
	off := 0
	for _, f := range fields { // validate layout first
		if f.Width < 1 || f.Width > 64 {
			return 0, ErrBadWidth
		}
		off += f.Width
		if off > 64 {
			return 0, ErrBadWidth
		}
	}
	off = 0
	var word uint64
	for _, f := range fields { // then values; still no state to mutate
		if !inRange(f) {
			return 0, ErrValueOverflow
		}
		pattern := uint64(f.Value) // int64->uint64 yields two's-complement bits
		if f.Width < 64 {
			pattern &= uint64(1)<<uint(f.Width) - 1
		}
		word |= pattern << uint(off) // off <= 63 (width>=1 and total<=64)
		off += f.Width
	}
	return word, nil
}

// Extract pulls width bits at bit offset off out of word. Signed fields are
// sign-extended when bit width-1 is 1; unsigned fields are zero-extended.
func Extract(word uint64, off, width int, signed bool) int64 {
	var mask uint64
	if width == 64 {
		mask = ^uint64(0)
	} else {
		mask = uint64(1)<<uint(width) - 1
	}
	x := word >> uint(off) & mask
	if signed && width < 64 && x&(uint64(1)<<uint(width-1)) != 0 {
		x |= ^mask // sign-extend: set every bit above the field
	}
	return int64(x)
}

// SetBit sets bit i: byte i/8, mask 1<<(i%8). Out-of-range i returns
// ErrBitIndex and leaves the bitmap unchanged.
func SetBit(bm []byte, i int) error {
	bitMu.Lock()
	defer bitMu.Unlock()
	if i < 0 || i >= 8*len(bm) {
		return ErrBitIndex
	}
	bm[i>>3] |= 1 << uint(i&7)
	return nil
}

// ClearBit clears bit i. Out-of-range i returns ErrBitIndex and leaves the
// bitmap unchanged.
func ClearBit(bm []byte, i int) error {
	bitMu.Lock()
	defer bitMu.Unlock()
	if i < 0 || i >= 8*len(bm) {
		return ErrBitIndex
	}
	bm[i>>3] &^= 1 << uint(i&7)
	return nil
}

// TestBit reports bit i; the bitmap is never modified.
func TestBit(bm []byte, i int) (bool, error) {
	bitMu.RLock()
	defer bitMu.RUnlock()
	if i < 0 || i >= 8*len(bm) {
		return false, ErrBitIndex
	}
	return bm[i>>3]&(1<<uint(i&7)) != 0, nil
}
