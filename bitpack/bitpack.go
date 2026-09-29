// Package bitpack implements fixed-width bit packing for unsigned integers.
// Widths 1..64 are supported; the final partial byte is zero-padded.
package bitpack

import "errors"

// ErrWidth is returned for widths outside 1..64.
var ErrWidth = errors.New("bitpack: invalid bit width")

// PackedLen reports the number of bytes needed for n values of width bits.
func PackedLen(n, width int) int {
	if width <= 0 {
		return 0
	}
	return (n*width + 7) / 8
}

// Pack writes n values, each using the low width bits, into a fresh byte slice.
// Bits beyond width in the inputs are discarded.
func Pack(vals []uint64, width int) []byte {
	if width < 1 || width > 64 {
		return nil
	}
	buf := make([]byte, PackedLen(len(vals), width))
	bit := 0
	var mask uint64
	if width == 64 {
		mask = ^uint64(0)
	} else {
		mask = 1<<uint(width) - 1
	}
	for _, v := range vals {
		v &= mask
		for b := 0; b < width; b++ {
			if v&(1<<uint(b)) != 0 {
				buf[bit/8] |= 1 << uint(bit%8)
			}
			bit++
		}
	}
	return buf
}

// Unpack reads n values of width bits from buf.
// It returns an error when buf is shorter than the required byte count,
// so truncated input never yields partially decoded values.
func Unpack(buf []byte, n, width int) ([]uint64, error) {
	if width < 1 || width > 64 {
		return nil, ErrWidth
	}
	need := PackedLen(n, width)
	if len(buf) < need {
		return nil, errors.New("bitpack: truncated packed data")
	}
	out := make([]uint64, n)
	bit := 0
	for i := 0; i < n; i++ {
		var v uint64
		for b := 0; b < width; b++ {
			if buf[bit/8]&(1<<uint(bit%8)) != 0 {
				v |= 1 << uint(b)
			}
			bit++
		}
		out[i] = v
	}
	return out, nil
}

// Width returns the smallest bit width that can hold codes 0..maxCode.
// maxCode must be non-negative; width 1 is the minimum.
func Width(maxCode int) int {
	w := 1
	for (1 << uint(w)) <= maxCode {
		w++
		if w == 64 {
			return 64
		}
	}
	return w
}
