package wire

// MaxVarintBytes is the longest legal varint encoding (64 bits need
// at most 10 groups of 7 bits).
const MaxVarintBytes = 10

// AppendVarint appends the canonical varint encoding of v to dst.
func AppendVarint(dst []byte, v uint64) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

// ReadVarint reads a varint starting at buf[off]. It returns the
// value and the offset just past the varint. Only canonical
// (shortest-form) encodings are accepted.
func ReadVarint(buf []byte, off int) (v uint64, next int, err error) {
	start := off
	for i := 0; i < MaxVarintBytes; i++ {
		if off >= len(buf) {
			return 0, off, &Error{Kind: KindTruncated, Offset: start}
		}
		b := buf[off]
		off++
		if i == MaxVarintBytes-1 {
			// The 10th byte may carry only a single value bit.
			if b > 1 {
				return 0, off, &Error{Kind: KindVarintOverflow, Offset: start}
			}
			v |= uint64(b) << 63
			if b == 0 {
				return 0, off, &Error{Kind: KindNonCanonicalVarint, Offset: start}
			}
			return v, off, nil
		}
		v |= uint64(b&0x7f) << (7 * uint(i))
		if b < 0x80 {
			if i > 0 && b == 0 {
				// A multi-byte varint must not end in a zero group.
				return 0, off, &Error{Kind: KindNonCanonicalVarint, Offset: start}
			}
			return v, off, nil
		}
	}
	return 0, off, &Error{Kind: KindVarintOverflow, Offset: start}
}
