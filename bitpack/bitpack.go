// Package bitpack implements fixed-width little-endian bit packing for
// unsigned integer words. Widths 1..64 are supported. Values are packed
// bit by bit into a byte stream; unused padding bits in the final byte
// must be zero.
package bitpack

import "errors"

// ErrWidth is returned when a bit width is outside [1, 64].
var ErrWidth = errors.New("bitpack: bit width out of range [1,64]")

// PackedSize reports how many bytes hold count values of the given width.
func PackedSize(count int, width uint) int {
	if count <= 0 || width == 0 {
		return 0
	}
	return (count*int(width) + 7) / 8
}

// Pack writes count values from src into a newly allocated byte slice.
// Values wider than width have their high bits silently discarded; callers
// must guarantee width is sufficient for their value range.
func Pack(src []uint64, count int, width uint) ([]byte, error) {
	if width < 1 || width > 64 {
		return nil, ErrWidth
	}
	if count < 0 || count > len(src) {
		return nil, errors.New("bitpack: count out of range")
	}
	if width == 64 {
		out := make([]byte, count*8)
		for i := 0; i < count; i++ {
			v := src[i]
			for b := 0; b < 8; b++ {
				out[i*8+b] = byte(v >> uint(b*8))
			}
		}
		return out, nil
	}
	dst := make([]byte, PackedSize(count, width))
	bit := 0
	for i := 0; i < count; i++ {
		v := src[i]
		for b := uint(0); b < width; b++ {
			if v&(1<<b) != 0 {
				dst[bit>>3] |= 1 << uint(bit&7)
			}
			bit++
		}
	}
	return dst, nil
}

// Unpack reads count values of the given width from src into dst. It
// validates that src has exactly the required length and that all padding
// bits beyond the packed values are zero, so truncated or corrupt input
// produces an error instead of partial results.
func Unpack(src []byte, count int, width uint, dst []uint64) error {
	if width < 1 || width > 64 {
		return ErrWidth
	}
	if count < 0 || count > len(dst) {
		return errors.New("bitpack: count out of range")
	}
	need := PackedSize(count, width)
	if len(src) != need {
		return errors.New("bitpack: packed block has wrong length")
	}
	if width == 64 {
		for i := 0; i < count; i++ {
			var v uint64
			for b := 0; b < 8; b++ {
				v |= uint64(src[i*8+b]) << uint(b*8)
			}
			dst[i] = v
		}
	return nil
	}
	bit := 0
	for i := 0; i < count; i++ {
		var v uint64
		for b := uint(0); b < width; b++ {
			if src[bit>>3]&(1<<uint(bit&7)) != 0 {
				v |= 1 << b
			}
			bit++
		}
		dst[i] = v
	}
	for ; bit < need*8; bit++ {
		if src[bit>>3]&(1<<uint(bit&7)) != 0 {
			return errors.New("bitpack: nonzero padding bits")
		}
	}
	return nil
}
