// Package bitpack performs fixed-width bit packing for unsigned code words.
// Bit stream layout is least-significant-bit first: value i occupies bit
// positions [i*width, i*width+width). Trailing padding bits are never read.
package bitpack

import "errors"

var (
	// ErrWidth reports a width outside 1..64.
	ErrWidth = errors.New("bitpack: width must be in 1..64")
	// ErrShort reports a payload too short for the requested values.
	ErrShort = errors.New("bitpack: truncated payload")
)

// ByteSize is the exact number of bytes needed for count words of width bits.
func ByteSize(count int, width uint8) int {
	if count <= 0 || width == 0 {
		return 0
	}
	return int(((int64(count)*int64(width)) + 7) / 8)
}

// Pack encodes vals at the given width. Values must fit in width bits.
func Pack(vals []uint64, width uint8) []byte {
	if width < 1 || width > 64 {
		return nil
	}
	n := len(vals)
	buf := make([]byte, ByteSize(n, width))
	for i, v := range vals {
		base := uint(i) * uint(width)
		for b := uint(0); b < uint(width); b++ {
			if v&(1<<b) != 0 {
				pos := base + b
				buf[pos>>3] |= 1 << (pos & 7)
			}
		}
	}
	return buf
}

// Unpack decodes exactly count words of width bits from data.
// It returns ErrShort when data cannot hold that many words; padding bits
// beyond count*width are ignored so no extra values are ever produced.
func Unpack(data []byte, width uint8, count int) ([]uint64, error) {
	if width < 1 || width > 64 {
		return nil, ErrWidth
	}
	if count < 0 {
		return nil, ErrShort
	}
	if count == 0 {
		return []uint64{}, nil
	}
	need := ByteSize(count, width)
	if len(data) < need {
		return nil, ErrShort
	}
	out := make([]uint64, count)
	for i := 0; i < count; i++ {
		base := uint(i) * uint(width)
		var v uint64
		for b := uint(0); b < uint(width); b++ {
			pos := base + b
			if data[pos>>3]&(1<<(pos&7)) != 0 {
				v |= 1 << b
			}
		}
		out[i] = v
	}
	return out, nil
}

// WidthFor reports the minimum width that can hold every value in vals.
// WidthFor of an empty slice is 1 (one bit still needs a width).
func WidthFor(vals []uint64) uint8 {
	var m uint64
	for _, v := range vals {
		if v > m {
			m = v
		}
	}
	return WidthMax(m)
}

// WidthMax reports the minimum width holding maxValue.
func WidthMax(maxValue uint64) uint8 {
	switch {
	case maxValue == 0:
		return 1
	case maxValue >= 1<<63:
		return 64
	}
	var w uint8
	for v := maxValue; v != 0; v >>= 1 {
		w++
	}
	return w
}
