// Package bitpack packs fixed-width unsigned integers into a bit stream and
// unpacks them. Widths 1..64 are supported; trailing bits that do not fill a
// whole byte are zero padded. It depends on no other package in this module.
package bitpack

import "errors"

// ErrWidth is returned when a width is outside [1,64].
var ErrWidth = errors.New("bitpack: width must be in 1..64")

// PackedLen reports the number of bytes needed for n values of width bits.
func PackedLen(n int, width uint) int {
	return (n*int(width) + 7) / 8
}

// Pack writes n unsigned values, each using the lowest width bits, LSB-first.
// Values must fit in width bits. Returns the byte slice of length PackedLen.
func Pack(values []uint64, width uint) ([]byte, error) {
	if width < 1 || width > 64 {
		return nil, ErrWidth
	}
	buf := make([]byte, PackedLen(len(values), width))
	var bitPos uint
	for _, v := range values {
		// Spread the value across byte boundaries without undefined shifts.
		remaining := width
		shift := bitPos & 7
		i := bitPos / 8
		x := v
		for remaining > 0 {
			bits := 8 - shift
			if bits > remaining {
				bits = remaining
			}
			buf[i] |= byte(x & ((uint64(1) << bits) - 1)) << shift
			x >>= bits
			remaining -= bits
			i++
			shift = 0
		}
		bitPos += width
	}
	return buf, nil
}

// Unpack reads exactly n width-bit values from buf. Trailing pad bits are
// ignored and no extra value is ever produced.
func Unpack(buf []byte, n int, width uint) ([]uint64, error) {
	if width < 1 || width > 64 {
		return nil, ErrWidth
	}
	if len(buf) < PackedLen(n, width) {
		return nil, errors.New("bitpack: buffer truncated")
	}
	out := make([]uint64, n)
	var bitPos uint
	for k := 0; k < n; k++ {
		var v uint64
		var got, outShift uint
		for got < width {
			i := bitPos / 8
			shift := bitPos & 7
			bits := 8 - shift
			if need := width - got; bits > need {
				bits = need
			}
			v |= (uint64(buf[i]>>shift) & ((uint64(1) << bits) - 1)) << outShift
			got += bits
			outShift += bits
			bitPos += bits
		}
		out[k] = v
	}
	return out, nil
}

// ZigZag maps signed int64 to unsigned while preserving small magnitude
// efficiency: 0->0, -1->1, 1->2, -2->3, ...
func ZigZag(v int64) uint64 {
	return uint64(v<<1) ^ uint64(v>>63)
}

// UnZigZag is the inverse of ZigZag.
func UnZigZag(u uint64) int64 {
	return int64(u>>1) ^ -int64(u&1)
}

// UWidth reports the minimum width in [1,64] needed to hold v.
func UWidth(v uint64) uint {
	for w := uint(1); w < 64; w++ {
		if v < uint64(1)<<w {
			return w
		}
	}
	return 64
}
