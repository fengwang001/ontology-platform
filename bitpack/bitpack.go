// Package bitpack packs and unpacks fixed-width unsigned integers using
// exactly width bits per value (1 <= width <= 64). Values are laid out
// LSB-first in a byte stream; the final byte is zero-padded when the
// total bit count is not a multiple of 8.
package bitpack

import "errors"

var (
	// ErrWidth is returned when width is outside 1..64.
	ErrWidth = errors.New("bitpack: width out of range 1..64")
	// ErrShort is returned when the destination or source buffer is too short.
	ErrShort = errors.New("bitpack: buffer too short")
)

// ByteLen reports how many bytes n values of the given width occupy.
func ByteLen(n, width int) int { return (n*width + 7) / 8 }

// Mask returns the bit mask covering width low bits (all ones for width 64).
func Mask(width int) (uint64, error) {
	if width < 1 || width > 64 {
		return 0, ErrWidth
	}
	if width == 64 {
		return ^uint64(0), nil
	}
	return uint64(1)<<uint(width) - 1, nil
}

// Pack writes vals into dst using width bits each. Values larger than the
// width are truncated to their low width bits.
func Pack(dst []byte, vals []uint64, width int) error {
	if width < 1 || width > 64 {
		return ErrWidth
	}
	need := ByteLen(len(vals), width)
	if len(dst) < need {
		return ErrShort
	}
	for i, v := range vals {
		base := i * width
		for b := 0; b < width; b++ {
			pos := base + b
			if v&(uint64(1)<<uint(b)) != 0 {
				dst[pos>>3] |= 1 << uint(pos&7)
			}
		}
	}
	return nil
}

// Unpack reads n values of width bits each from src. Trailing padding bits
// never produce extra values because exactly n values are decoded.
func Unpack(src []byte, n, width int) ([]uint64, error) {
	if width < 1 || width > 64 {
		return nil, ErrWidth
	}
	if n < 0 || len(src) < ByteLen(n, width) {
		return nil, ErrShort
	}
	out := make([]uint64, n)
	for i := 0; i < n; i++ {
		var v uint64
		base := i * width
		for b := 0; b < width; b++ {
			pos := base + b
			if src[pos>>3]&(1<<uint(pos&7)) != 0 {
				v |= uint64(1) << uint(b)
			}
		}
		out[i] = v
	}
	return out, nil
}
