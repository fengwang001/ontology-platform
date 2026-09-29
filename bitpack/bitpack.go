// Package bitpack packs and unpacks fixed-width unsigned integers at any
// bit width from 1 to 64. Values are laid out LSB-first across bytes; the
// final, partially used byte is zero padded.
package bitpack

import "errors"

// ErrShortBuffer means the buffer cannot hold the requested number of bits.
var ErrShortBuffer = errors.New("bitpack: short buffer")

// Width returns the smallest bit width in [1,64] able to hold max.
func Width(max uint64) int {
	if max == 0 {
		return 1
	}
	w := 0
	for v := max; v != 0; v >>= 1 {
		w++
	}
	return w
}

// Size returns the number of bytes needed to store n values of width w.
func Size(n, w int) int {
	bits := n * w
	return (bits + 7) / 8
}

// Pack stores n values of width w. Values larger than the width can express
// are rejected. Returns an error for invalid widths.
func Pack(vals []uint64, w int) ([]byte, error) {
	if w < 1 || w > 64 {
		return nil, errors.New("bitpack: width out of range")
	}
	out := make([]byte, Size(len(vals), w))
	var limit uint64 = ^uint64(0)
	if w < 64 {
		limit = 1<<w - 1
	}
	for i, v := range vals {
		if v > limit {
			return nil, errors.New("bitpack: value exceeds width")
		}
		off := uint(i * w)
		idx := off / 8
		shift := off % 8
		out[idx] |= byte(v << shift)
		if shift > 0 && idx+1 < uint(len(out)) {
			out[idx+1] |= byte(v >> (8 - shift))
		}
		if shift > 8-(w%8) {
			// not reached: a value spans at most ceil((shift+w)/8) bytes;
			// the branch below covers the remaining high bytes.
		}
		// Emit any bytes beyond the first two for very wide shifted values.
		for b := 2; shift > 0 && uint(shift)+uint(w) > uint(8*b) && idx+uint(b) < uint(len(out)); b++ {
			out[idx+uint(b)] |= byte(v >> (8*uint(b) - shift))
		}
	}
	return out, nil
}

// Unpack reads exactly n values of width w from buf. A truncated buffer is
// reported as ErrShortBuffer; padding bits are never read as extra values.
func Unpack(buf []byte, w, n int) ([]uint64, error) {
	if w < 1 || w > 64 {
		return nil, errors.New("bitpack: width out of range")
	}
	if n < 0 || uint64(len(buf))*8 < uint64(n)*uint64(w) {
		return nil, ErrShortBuffer
	}
	out := make([]uint64, n)
	var mask uint64 = ^uint64(0)
	if w < 64 {
		mask = 1<<w - 1
	}
	for i := 0; i < n; i++ {
		off := uint(i * w)
		idx := off / 8
		shift := off % 8
		v := uint64(buf[idx]) >> shift
		for b := uint(1); shift > 0 && shift+uint(w) > 8*b; b++ {
			v |= uint64(buf[idx+b]) << (8*b - shift)
		}
		out[i] = v & mask
	}
	return out, nil
}
