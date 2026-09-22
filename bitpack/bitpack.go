// Package bitpack packs and unpacks fixed-width unsigned integers
// into a bit stream. Widths from 1 to 64 bits are supported, and the
// final byte is zero-padded when the bit count is not byte aligned.
package bitpack

import (
	"errors"
	"math/bits"
)

// ErrShortBuffer is returned when the input does not contain enough
// bytes to decode the requested number of values.
var ErrShortBuffer = errors.New("bitpack: input too short")

// ErrBadWidth is returned for widths outside [1, 64].
var ErrBadWidth = errors.New("bitpack: width must be in [1, 64]")

// PackedLen returns the number of bytes needed to pack n values of
// the given bit width, including zero padding of the final byte.
func PackedLen(n int, width uint8) int {
	if n <= 0 {
		return 0
	}
	return (n*int(width) + 7) / 8
}

// MinWidth returns the smallest width in [1, 64] that can hold max.
func MinWidth(max uint64) uint8 {
	w := bits.Len64(max)
	if w == 0 {
		w = 1
	}
	return uint8(w)
}

// Pack appends vals packed at the given width to dst and returns the
// extended slice. Bits are laid out LSB-first: value i occupies bits
// [i*width, (i+1)*width) of the stream, low bits in low byte bits.
func Pack(dst []byte, vals []uint64, width uint8) []byte {
	need := PackedLen(len(vals), width)
	base := len(dst)
	dst = append(dst, make([]byte, need)...)
	buf := dst[base:]
	bitPos := 0
	for _, v := range vals {
		remaining := int(width)
		val := v
		for remaining > 0 {
			byteIdx := bitPos >> 3
			bitOff := uint(bitPos & 7)
			take := 8 - bitOff
			if take > uint(remaining) {
				take = uint(remaining)
			}
			mask := uint64(1<<take) - 1
			buf[byteIdx] |= byte((val & mask) << bitOff)
			val >>= take
			remaining -= int(take)
			bitPos += int(take)
		}
	}
	return dst
}

// Unpack decodes n values of the given width from data. It reads
// exactly the bits of the n values and never touches padding bits
// beyond them, so no extra values can appear.
func Unpack(data []byte, n int, width uint8) ([]uint64, error) {
	if width < 1 || width > 64 {
		return nil, ErrBadWidth
	}
	if len(data) < PackedLen(n, width) {
		return nil, ErrShortBuffer
	}
	out := make([]uint64, n)
	bitPos := 0
	for i := range out {
		remaining := int(width)
		var v uint64
		var shift uint
		for remaining > 0 {
			byteIdx := bitPos >> 3
			bitOff := uint(bitPos & 7)
			take := 8 - bitOff
			if take > uint(remaining) {
				take = uint(remaining)
			}
			mask := (uint64(1<<take) - 1) << bitOff
			v |= (uint64(data[byteIdx]) & mask) >> bitOff << shift
			shift += take
			remaining -= int(take)
			bitPos += int(take)
		}
		out[i] = v
	}
	return out, nil
}
