// Package bitpack performs fixed-width bit packing of unsigned integers.
// Values are laid out LSB-first; the last word is zero padded.
package bitpack

import "errors"

// ErrBadWidth is returned when a width is outside [1,64].
var ErrBadWidth = errors.New("bitpack: width out of range 1..64")

// ErrShortData is returned when the payload cannot hold m values of width w.
var ErrShortData = errors.New("bitpack: truncated payload")

// Zig maps int64 bijectively to uint64, preserving magnitude ordering.
func Zig(v int64) uint64 { return uint64(v<<1) ^ uint64(v>>63) }

// Unzig is the inverse of Zig.
func Unzig(u uint64) int64 { return int64(u>>1) ^ -int64(u&1) }

// Width64 returns the number of bits needed to represent u (0 for u==0).
func Width64(u uint64) int {
	w := 0
	for u != 0 {
		u >>= 1
		w++
	}
	return w
}

// PackedLen is ceil(m*w/8).
func PackedLen(m, w int) int { return (m*w + 7) >> 3 }

// Pack encodes m values, each truncated to w bits, into a freshly allocated byte slice.
func Pack(vals []uint64, w int) []byte {
	if w < 1 || w > 64 {
		panic(ErrBadWidth)
	}
	out := make([]byte, PackedLen(len(vals), w))
	for i, v := range vals {
		base := i * w
		for b := 0; b < w; b++ {
			if v&(uint64(1)<<uint(b)) != 0 {
				p := base + b
				out[p>>3] |= 1 << uint(p&7)
			}
		}
	}
	return out
}

// Unpack decodes m values of width w. It never reads past len(data).
func Unpack(data []byte, w, m int) ([]uint64, error) {
	if w < 1 || w > 64 {
		return nil, ErrBadWidth
	}
	if len(data) != PackedLen(m, w) {
		return nil, ErrShortData
	}
	out := make([]uint64, m)
	var mask uint64
	if w == 64 {
		mask = ^uint64(0)
	} else {
		mask = uint64(1)<<uint(w) - 1
	}
	for i := 0; i < m; i++ {
		var v uint64
		base := i * w
		for b := 0; b < w; b++ {
			p := base + b
			if data[p>>3]&(1<<uint(p&7)) != 0 {
				v |= uint64(1) << uint(b)
			}
		}
		out[i] = v & mask
	}
	return out, nil
}
