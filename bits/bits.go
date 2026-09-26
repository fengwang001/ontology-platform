// Package bits implements fixed-width bitfield packing into a uint64
// (least-significant-bits first) and bitmap set/clear/test by bit index.
package bits

import "errors"

var (
	// ErrBadWidth: a field width is not in [1,64], or total width exceeds 64.
	ErrBadWidth = errors.New("bits: illegal width")
	// ErrValueOverflow: a value does not fit its field's width; never truncated.
	ErrValueOverflow = errors.New("bits: field value overflows width")
	// ErrBitIndex: bit index out of range (>= 8*len(bitmap)).
	ErrBitIndex = errors.New("bits: bit index out of range")
)

// Field is one fixed-width bitfield. Width in [1,64]; Value must fit Width
// (signed: [-2^(w-1), 2^(w-1)-1]; unsigned: [0, 2^w-1]).
type Field struct {
	Width  int
	Value  int64
	Signed bool
}

// encode returns the low-Width bits pattern for f.Value, or ErrValueOverflow.
func encode(f Field) (uint64, error) {
	if f.Signed {
		if f.Width < 64 {
			lo := -(int64(1) << (f.Width - 1))
			hi := (int64(1) << (f.Width - 1)) - 1
			if f.Value < lo || f.Value > hi {
				return 0, ErrValueOverflow
			}
		}
		return uint64(f.Value) & mask(f.Width), nil
	}
	if f.Value < 0 || (f.Width < 64 && uint64(f.Value) > mask(f.Width)) {
		return 0, ErrValueOverflow
	}
	return uint64(f.Value), nil
}

func mask(width int) uint64 {
	if width >= 64 {
		return ^uint64(0)
	}
	return (uint64(1) << width) - 1
}

// PackFields packs fields from the least significant bit upward: field i
// starts at the sum of all previous widths. Any invalid field fails the
// whole pack with (0, err); nothing is partially applied.
func PackFields(fields []Field) (uint64, error) {
	total := 0
	for _, f := range fields {
		if f.Width < 1 || f.Width > 64 {
			return 0, ErrBadWidth
		}
		total += f.Width
	}
	if total > 64 {
		return 0, ErrBadWidth
	}
	var word uint64
	off := 0
	for _, f := range fields {
		pat, err := encode(f)
		if err != nil {
			return 0, err
		}
		word |= pat << off
		off += f.Width
	}
	return word, nil
}

// Extract reads width bits of word starting at off. Callers must guarantee
// 1 <= width <= 64 and off+width <= 64 (PackFields' layout does). Signed
// fields are sign-extended from bit width-1; unsigned are zero-extended.
func Extract(word uint64, off, width int, signed bool) int64 {
	v := (word >> off) & mask(width)
	if signed && width < 64 && v&(uint64(1)<<(width-1)) != 0 {
		v |= ^mask(width)
	}
	return int64(v)
}

// SetBit sets bit idx (byte idx/8, bit idx%8, LSB first within a byte).
func SetBit(b []byte, idx int) error {
	if idx < 0 || idx >= 8*len(b) {
		return ErrBitIndex
	}
	b[idx/8] |= 1 << (idx % 8)
	return nil
}

// ClearBit clears bit idx.
func ClearBit(b []byte, idx int) error {
	if idx < 0 || idx >= 8*len(b) {
		return ErrBitIndex
	}
	b[idx/8] &^= 1 << (idx % 8)
	return nil
}

// TestBit reports whether bit idx is set.
func TestBit(b []byte, idx int) (bool, error) {
	if idx < 0 || idx >= 8*len(b) {
		return false, ErrBitIndex
	}
	return b[idx/8]&(1<<(idx%8)) != 0, nil
}
