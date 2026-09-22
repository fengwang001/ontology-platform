package wire

// Type is the wire type of a field, stored as a single byte after the
// field number.
type Type byte

// The only three valid wire types.
const (
	Varint  Type = 0 // payload is one varint
	Bytes   Type = 1 // payload is length-prefixed raw bytes
	Message Type = 2 // payload is a length-prefixed nested message
)

// Valid reports whether t is one of the three known wire types.
func (t Type) Valid() bool {
	return t == Varint || t == Bytes || t == Message
}

// HasLength reports whether fields of this type carry a length prefix
// between the type byte and the payload.
func (t Type) HasLength() bool {
	return t == Bytes || t == Message
}

// MaxVarintBytes is the maximum length of a 64-bit varint.
const MaxVarintBytes = 10

// DecodeVarint reads a varint from the start of buf: 7 bits per byte,
// most significant bit as continuation flag, little-endian groups.
//
// It returns the value and the number of bytes consumed. The encoding
// must be canonical (shortest form); otherwise the re-encode of a
// parsed message could differ from the original bytes.
func DecodeVarint(buf []byte) (uint64, int, error) {
	var v uint64
	for i := 0; i < len(buf) && i < MaxVarintBytes; i++ {
		b := buf[i]
		if i == MaxVarintBytes-1 && b > 1 {
			// 10th byte may only contribute bit 63; anything
			// more (including a continuation flag) overflows.
			return 0, 0, &ParseError{Err: ErrVarintOverflow, Offset: 0}
		}
		v |= uint64(b&0x7f) << (7 * uint(i))
		if b < 0x80 {
			if i > 0 && b == 0 {
				return 0, 0, &ParseError{Err: ErrNonCanonicalVarint, Offset: 0}
			}
			return v, i + 1, nil
		}
	}
	if len(buf) >= MaxVarintBytes {
		return 0, 0, &ParseError{Err: ErrVarintOverflow, Offset: 0}
	}
	return 0, 0, &ParseError{Err: ErrVarintTruncated, Offset: 0}
}

// AppendVarint appends the canonical varint encoding of v to dst and
// returns the extended slice.
func AppendVarint(dst []byte, v uint64) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

// VarintLen returns the number of bytes the canonical encoding of v
// occupies.
func VarintLen(v uint64) int {
	n := 1
	for v >= 0x80 {
		v >>= 7
		n++
	}
	return n
}
