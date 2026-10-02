// Package bitpack packs and unpacks fixed-width unsigned integers
// (width 1..64) into a little-endian bit stream. Value i occupies bits
// [i*width, (i+1)*width) of the byte stream, LSB first. Trailing bits
// of the final byte are zero padding and never produce extra values.
package bitpack

// ByteLen returns the number of bytes needed to pack n values at width.
func ByteLen(n int, width uint8) int {
	if n <= 0 {
		return 0
	}
	return (n*int(width) + 7) / 8
}

// Pack encodes vals at the given bit width (1..64) into a fresh slice.
// Values are masked to the low width bits.
func Pack(vals []uint64, width uint8) []byte {
	out := make([]byte, ByteLen(len(vals), width))
	bitPos := 0
	for _, v := range vals {
		rem := int(width)
		for rem > 0 {
			idx := bitPos >> 3
			off := uint(bitPos & 7)
			take := 8 - off
			if int(take) > rem {
				take = uint(rem)
			}
			mask := uint64(1<<take) - 1
			out[idx] |= byte((v & mask) << off)
			v >>= take
			rem -= int(take)
			bitPos += int(take)
		}
	}
	return out
}

// Unpack decodes exactly n values at the given bit width from src.
// The caller must guarantee len(src) >= ByteLen(n, width); segment
// decoding validates this before calling, so truncated data can never
// reach here. Padding bits beyond n*width are ignored.
func Unpack(src []byte, width uint8, n int) []uint64 {
	out := make([]uint64, n)
	bitPos := 0
	for i := range out {
		rem := int(width)
		var v uint64
		var shift uint
		for rem > 0 {
			idx := bitPos >> 3
			off := uint(bitPos & 7)
			take := 8 - off
			if int(take) > rem {
				take = uint(rem)
			}
			mask := uint64(1<<take) - 1
			v |= (uint64(src[idx]>>off) & mask) << shift
			shift += take
			rem -= int(take)
			bitPos += int(take)
		}
		out[i] = v
	}
	return out
}
