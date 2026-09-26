// Package enc implements the low-level protobuf wire primitives:
// unsigned LEB128 varints, zig-zag signed integers, little-endian
// fixed-width integers and field-key helpers. It depends on no other package.
package enc

import "errors"

// Sentinel errors. Every decode failure in the codec tree is reported with
// one of these (or a wire-package sentinel), so callers can branch on them.
var (
	// ErrTruncated means a value runs past the end of the input.
	ErrTruncated = errors.New("enc: truncated input")
	// ErrOverflow means a varint exceeds 10 bytes or the uint64 range.
	ErrOverflow = errors.New("enc: varint overflow")
)

// EncodeVarint appends nothing; it returns the unsigned LEB128 encoding of u:
// 7 payload bits per byte, least significant group first, bit 7 = continue.
// Non-minimal encodings are accepted on decode, so zero encodes as 00 here
// but 80 00 must also decode to zero.
func EncodeVarint(u uint64) []byte {
	var b [10]byte
	i := 0
	for u >= 0x80 {
		b[i] = byte(u) | 0x80
		u >>= 7
		i++
	}
	b[i] = byte(u)
	return append([]byte(nil), b[:i+1]...)
}

// DecodeVarint decodes one varint from b, returning the value and the number
// of bytes consumed. More than 10 continuation bytes, a 10th byte carrying
// more than bit 0, or a value outside uint64 => ErrOverflow. A stream that
// ends while the continuation bit is set => ErrTruncated.
func DecodeVarint(b []byte) (uint64, int, error) {
	var u uint64
	for i := 0; i < len(b) && i < 10; i++ {
		c := b[i]
		// The 10th byte may only carry value bit 63 (payload bit 0);
		// anything larger overflows uint64 or needs an 11th byte.
		if i == 9 && c > 1 {
			return 0, 0, ErrOverflow
		}
		u |= uint64(c&0x7f) << (7 * i)
		if c&0x80 == 0 {
			return u, i + 1, nil
		}
	}
	return 0, 0, ErrTruncated
}

// Zigzag32 maps a signed int32 onto an unsigned int32 without sign extension
// bloating small negatives: (n<<1) ^ (n>>31).
func Zigzag32(n int32) uint32 { return uint32((n << 1) ^ (n >> 31)) }

// Unzigzag32 is the inverse: (u>>1) ^ -(u&1); the negation folds the sign.
func Unzigzag32(u uint32) int32 { return int32(u>>1) ^ -int32(u&1) }

// Zigzag64 is the 64-bit zig-zag encoding: (n<<1) ^ (n>>63).
func Zigzag64(n int64) uint64 { return uint64((n << 1) ^ (n >> 63)) }

// Unzigzag64 is the 64-bit inverse: (u>>1) ^ -(u&1).
func Unzigzag64(u uint64) int64 { return int64(u>>1) ^ -int64(u&1) }

// PutFixed32 writes v as 4 little-endian bytes. len(b) must be >= 4.
func PutFixed32(b []byte, v uint32) {
	b[0] = byte(v)
	b[1] = byte(v >> 8)
	b[2] = byte(v >> 16)
	b[3] = byte(v >> 24)
}

// GetFixed32 reads 4 little-endian bytes. len(b) must be >= 4; callers in the
// wire package verify bounds before calling.
func GetFixed32(b []byte) uint32 {
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
}

// PutFixed64 writes v as 8 little-endian bytes. len(b) must be >= 8.
func PutFixed64(b []byte, v uint64) {
	b[0] = byte(v)
	b[1] = byte(v >> 8)
	b[2] = byte(v >> 16)
	b[3] = byte(v >> 24)
	b[4] = byte(v >> 32)
	b[5] = byte(v >> 40)
	b[6] = byte(v >> 48)
	b[7] = byte(v >> 56)
}

// GetFixed64 reads 8 little-endian bytes. len(b) must be >= 8.
func GetFixed64(b []byte) uint64 {
	return uint64(b[0]) | uint64(b[1])<<8 | uint64(b[2])<<16 |
		uint64(b[3])<<24 | uint64(b[4])<<32 | uint64(b[5])<<40 |
		uint64(b[6])<<48 | uint64(b[7])<<56
}

// KeyNum extracts the field number from a field key: key >> 3.
func KeyNum(key uint64) int { return int(key >> 3) }

// KeyWire extracts the wire type from a field key: key & 0x07.
func KeyWire(key uint64) int { return int(key & 0x07) }
