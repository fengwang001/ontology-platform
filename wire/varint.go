package wire

// MaxVarintLen is the longest legal encoding of a uint64 varint.
const MaxVarintLen = 10

// AppendVarint appends v to dst using the standard varint encoding:
// 7 bits per byte, little-endian groups, high bit as continuation.
func AppendVarint(dst []byte, v uint64) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

// VarintLen returns the encoded length of v in bytes.
func VarintLen(v uint64) int {
	n := 1
	for v >= 0x80 {
		v >>= 7
		n++
	}
	return n
}

// ReadVarint decodes a varint starting at buf[off]. It returns the
// value and the offset just past the varint. Errors:
//   - KindTruncated if the buffer ends before the varint terminates;
//   - KindVarintOverflow if 10 bytes pass without termination or the
//     10th byte would overflow a uint64.
func ReadVarint(buf []byte, off int) (uint64, int, error) {
	var v uint64
	for i := 0; i < MaxVarintLen; i++ {
		if off+i >= len(buf) {
			return 0, 0, &Error{Kind: KindTruncated, Offset: off + i}
		}
		b := buf[off+i]
		if i == MaxVarintLen-1 && b > 1 {
			// The 10th byte may only be 0 or 1; anything larger
			// either overflows uint64 or continues past 10 bytes.
			return 0, 0, &Error{Kind: KindVarintOverflow, Offset: off}
		}
		v |= uint64(b&0x7f) << (7 * i)
		if b < 0x80 {
			return v, off + i + 1, nil
		}
	}
	return 0, 0, &Error{Kind: KindVarintOverflow, Offset: off}
}
