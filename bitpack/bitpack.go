// Package bitpack performs fixed-width bit packing and unpacking of unsigned
// integers. Widths 1..64 are supported. Values are packed LSB-first: the first
// value occupies the lowest bits of the first byte. Trailing padding bits are
// ignored on unpack, so a value count that is not a multiple of eight never
// produces spurious values.
package bitpack

import "errors"

var (
	// ErrWidth is returned when the requested bit width is outside 1..64.
	ErrWidth = errors.New("bitpack: bit width must be in [1,64]")
	// ErrOverflow is returned when a value does not fit the chosen width.
	ErrOverflow = errors.New("bitpack: value exceeds bit width")
	// ErrShortData is returned when the input holds fewer bits than requested.
	ErrShortData = errors.New("bitpack: truncated bit stream")
)

// PackedLen reports the number of bytes needed to pack n values of width bits.
func PackedLen(n, width int) int {
	return (n*width + 7) / 8
}

// Pack writes values into a freshly allocated byte slice.
func Pack(values []uint64, width int) ([]byte, error) {
	if width < 1 || width > 64 {
		return nil, ErrWidth
	}
	out := make([]byte, PackedLen(len(values), width))
	if err := PackInto(values, width, out); err != nil {
		return nil, err
	}
	return out, nil
}

// PackInto packs values into dst, which must be at least PackedLen(len(values)).
func PackInto(values []uint64, width int, dst []byte) error {
	if width < 1 || width > 64 {
		return ErrWidth
	}
	if len(dst) < PackedLen(len(values), width) {
		return ErrShortData
	}
	var mask uint64
	if width == 64 {
		mask = ^uint64(0)
	} else {
		mask = (uint64(1) << uint(width)) - 1
	}
	var bitPos int
	for _, v := range values {
		if v & ^mask != 0 {
			return ErrOverflow
		}
		offset := bitPos & 7
		idx := bitPos >> 3
		nBytes := (offset + width + 7) / 8
		dst[idx] |= byte(v << uint(offset))
		for b := 1; b < nBytes; b++ {
			dst[idx+b] |= byte(v >> uint(8*b-offset))
		}
		bitPos += width
	}
	return nil
}

// Unpack reads exactly count values of width bits from src.
func Unpack(src []byte, width, count int) ([]uint64, error) {
	if width < 1 || width > 64 {
		return nil, ErrWidth
	}
	if len(src) < PackedLen(count, width) {
		return nil, ErrShortData
	}
	out := make([]uint64, count)
	var mask uint64
	if width == 64 {
		mask = ^uint64(0)
	} else {
		mask = (uint64(1) << uint(width)) - 1
	}
	var bitPos int
	for i := 0; i < count; i++ {
		offset := bitPos & 7
		idx := bitPos >> 3
		var v uint64
		nBytes := (offset + width + 7) / 8
		v = uint64(src[idx]) >> uint(offset)
		for b := 1; b < nBytes; b++ {
			v |= uint64(src[idx+b]) << uint(8*b-offset)
		}
		out[i] = v & mask
		bitPos += width
	}
	return out, nil
}

// ZigZag maps a signed int64 onto an unsigned value so that small magnitude
// numbers (including negatives) need few bits.
func ZigZag(v int64) uint64 {
	return uint64(v<<1) ^ uint64(v>>63)
}

// UnZigZag reverses ZigZag.
func UnZigZag(u uint64) int64 {
	return int64(u>>1) ^ -int64(u&1)
}

// PackSigned zig-zag encodes and packs signed values.
func PackSigned(values []int64, width int) ([]byte, error) {
	u := make([]uint64, len(values))
	for i, v := range values {
		u[i] = ZigZag(v)
	}
	return Pack(u, width)
}

// UnpackSigned unpacks and zig-zag decodes signed values.
func UnpackSigned(src []byte, width, count int) ([]int64, error) {
	u, err := Unpack(src, width, count)
	if err != nil {
		return nil, err
	}
	out := make([]int64, count)
	for i, v := range u {
		out[i] = UnZigZag(v)
	}
	return out, nil
}
