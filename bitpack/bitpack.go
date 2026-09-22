// Package bitpack provides fixed-width integer bit packing and unpacking.
// Widths from 1 to 64 bits are supported; values are packed little-endian
// bit order and the final partial byte is zero-padded.
package bitpack

import "errors"

// ErrWidth is returned for bit widths outside 1..64.
var ErrWidth = errors.New("bitpack: width must be in 1..64")

// ErrShort means the input buffer does not hold the full payload.
var ErrShort = errors.New("bitpack: short buffer")

// ErrValue is returned when a value does not fit in width bits.
var ErrValue = errors.New("bitpack: value exceeds width")

// Pack encodes n unsigned values, each using width bits (1..64), into a
// newly allocated byte slice. Bits beyond the last value are zero.
func Pack(values []uint64, width int) ([]byte, error) {
	if width < 1 || width > 64 {
		return nil, ErrWidth
	}
	out := make([]byte, PackedLen(len(values), width))
	mask := widthMask(width)
	var bitPos uint
	for _, v := range values {
		if v&^mask != 0 {
			return nil, ErrValue
		}
		off := int(bitPos / 8)
		shift := int(bitPos % 8)
		numBytes := (shift + width + 7) / 8
		for j := 0; j < numBytes; j++ {
			right := 8*j - shift
			if right >= 0 {
				out[off+j] |= byte(v >> right)
			} else {
				out[off+j] |= byte(v << -right)
			}
		}
		bitPos += uint(width)
	}
	return out, nil
}

// Unpack decodes n width-bit values from data. It returns an error if data
// is shorter than the encoded payload.
func Unpack(data []byte, n, width int) (out []uint64, err error) {
	if width < 1 || width > 64 {
		return nil, ErrWidth
	}
	if n < 0 {
		return nil, ErrShort
	}
	if n == 0 {
		return []uint64{}, nil
	}
	if len(data) < PackedLen(n, width) {
		return nil, ErrShort
	}
	mask := widthMask(width)
	out = make([]uint64, n)
	var bitPos uint
	for i := 0; i < n; i++ {
		off := int(bitPos / 8)
		shift := int(bitPos % 8)
		var word uint64
		numBytes := (shift + width + 7) / 8
		for j := 0; j < numBytes; j++ {
			right := 8*j - shift
			if right >= 0 {
				word |= uint64(data[off+j]) << right
			} else {
				word |= uint64(uint64(data[off+j]) >> -right)
			}
		}
		out[i] = word & mask
		bitPos += uint(width)
	}
	return out, nil
}

func widthMask(width int) uint64 {
	if width == 64 {
		return ^uint64(0)
	}
	return uint64(1)<<width - 1
}

// PackedLen reports the number of bytes required for n width-bit values.
func PackedLen(n, width int) int {
	if n <= 0 || width <= 0 {
		return 0
	}
	return (n*width + 7) / 8
}
