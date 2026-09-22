package wire

// MaxVarintBytes is the longest legal encoding of a uint64 varint.
const MaxVarintBytes = 10

// AppendVarint appends v to dst using the standard LEB128 encoding:
// 7 bits per byte, most significant bit as continuation flag,
// little-endian group order. The encoding is canonical (minimal).
func AppendVarint(dst []byte, v uint64) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

// VarintLen returns the number of bytes the canonical encoding of v uses.
func VarintLen(v uint64) int {
	n := 1
	for v >= 0x80 {
		v >>= 7
		n++
	}
	return n
}

// ReadVarint decodes a varint from the start of buf and returns the value
// and the number of bytes consumed. Non-canonical (zero-padded) encodings
// are accepted; callers that need byte-exact round-trips must preserve the
// raw bytes themselves.
//
// Errors: ErrTruncated if buf ends mid-varint, ErrVarintOverflow if the
// varint does not terminate within MaxVarintBytes or exceeds 64 bits.
func ReadVarint(buf []byte) (v uint64, n int, err error) {
	for i := 0; i < MaxVarintBytes; i++ {
		if i >= len(buf) {
			return 0, 0, ErrTruncated
		}
		b := buf[i]
		if i == MaxVarintBytes-1 {
			// The 10th byte may contribute at most 1 bit and must
			// terminate the varint.
			if b > 1 {
				return 0, 0, ErrVarintOverflow
			}
			return v | uint64(b)<<63, MaxVarintBytes, nil
		}
		v |= uint64(b&0x7f) << (7 * uint(i))
		if b < 0x80 {
			return v, i + 1, nil
		}
	}
	return 0, 0, ErrVarintOverflow
}
