// Package wire defines the self-describing byte format of the LZ77 stream.
// It has no dependencies on the other packages of this module.
package wire

const (
	Magic   = "LZL1"
	Version = byte(1)

	TagLiteral = 0
	TagMatch   = 1
	TagFlush   = 2
	TagEnd     = 3

	MinMatch = 3
	MaxVarint = 10
)

// AppendUvarint appends an unsigned LEB128 integer.
func AppendUvarint(dst []byte, v uint64) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

// ReadUvarint consumes one unsigned LEB128 integer from the front of src.
func ReadUvarint(src []byte) (v uint64, rest []byte, ok, overflow bool) {
	var x uint64
	for i := 0; i < MaxVarint; i++ {
		if i >= len(src) {
			return 0, src, false, false
		}
		b := src[i]
		if i == MaxVarint-1 && b > 1 {
			return 0, src[MaxVarint:], false, true
		}
		x |= uint64(b&0x7f) << (7 * i)
		if b < 0x80 {
			return x, src[i+1:], true, false
		}
}
	return 0, src[MaxVarint:], false, true
}

// AppendHeader writes the stream header.
func AppendHeader(dst []byte, windowSize, maxChain int) []byte {
	dst = append(dst, Magic...)
	dst = append(dst, Version)
	dst = AppendUvarint(dst, uint64(windowSize))
	dst = AppendUvarint(dst, uint64(maxChain))
	return dst
}
