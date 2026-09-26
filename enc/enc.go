// Package enc implements canonical LEB128 variable-length integer coding:
// unsigned and signed 64-bit values, 7 bits per byte, least significant
// group first, bit 7 as continuation flag. Decoding rejects non-canonical
// (non-minimal) encodings and overflow.
package enc

import "errors"

// Sentinel errors; each rejection is decidable via errors.Is.
var (
	ErrEmptyInput   = errors.New("enc: empty input")
	ErrOverflow     = errors.New("enc: varint overflows 64-bit value")
	ErrNonCanonical = errors.New("enc: non-canonical (non-minimal) encoding")
)

const maxBytes = 10 // 64 bits need at most ceil(64/7) = 10 bytes

// EncodeUint encodes v as unsigned VLQ. Zero encodes as a single 0x00.
func EncodeUint(v uint64) []byte {
	var out [maxBytes]byte
	i := 0
	for {
		b := byte(v & 0x7f)
		v >>= 7
		if v != 0 {
			b |= 0x80
		}
		out[i] = b
		i++
		if v == 0 {
			return out[:i]
		}
	}
}

// EncodeInt encodes v as signed VLQ using arithmetic right shift; a byte's
// sign bit is its bit 6. Stops when the remainder is 0 and bit6 is clear
// (non-negative end) or the remainder is -1 and bit6 is set (negative end).
func EncodeInt(v int64) []byte {
	var out [maxBytes]byte
	i := 0
	for {
		b := byte(v & 0x7f)
		v >>= 7
		done := (v == 0 && b&0x40 == 0) || (v == -1 && b&0x40 != 0)
		if !done {
			b |= 0x80
		}
		out[i] = b
		i++
		if done {
			return out[:i]
		}
	}
}

// DecodeUint decodes an unsigned VLQ from the front of b, returning the
// value and the number of bytes consumed.
func DecodeUint(b []byte) (uint64, int, error) {
	var v uint64
	for i := 0; i < maxBytes; i++ {
		if i >= len(b) {
			return 0, 0, ErrEmptyInput
		}
		c := b[i]
		if i == maxBytes-1 {
			if c&0x80 != 0 || c > 1 {
				return 0, 0, ErrOverflow
			}
		}
		v |= uint64(c&0x7f) << (7 * i)
		if c&0x80 == 0 {
			if i > 0 && c == 0x00 {
				return 0, 0, ErrNonCanonical
			}
			return v, i + 1, nil
		}
	}
	panic("unreachable")
}

// DecodeInt decodes a signed VLQ from the front of b, returning the value
// and the number of bytes consumed.
func DecodeInt(b []byte) (int64, int, error) {
	var v int64
	for i := 0; i < maxBytes; i++ {
		if i >= len(b) {
			return 0, 0, ErrEmptyInput
		}
		c := b[i]
		if i == maxBytes-1 {
			if c&0x80 != 0 {
				return 0, 0, ErrOverflow
			}
			// The 10th byte's bit 6 must equal the final sign: only
			// 0x00 (non-negative) or 0x7F (negative) fit a sign
			// extension of a 64-bit value.
			if c != 0x00 && c != 0x7f {
				return 0, 0, ErrOverflow
			}
		}
		v |= int64(c&0x7f) << (7 * i)
		if c&0x80 == 0 {
			if shift := 7 * (i + 1); shift < 64 && c&0x40 != 0 {
				v |= -1 << shift // sign extension
			}
			if i > 0 {
				prevNeg := b[i-1]&0x40 != 0
				if (c == 0x00 && !prevNeg) || (c == 0x7f && prevNeg) {
					return 0, 0, ErrNonCanonical
				}
			}
			return v, i + 1, nil
		}
	}
	panic("unreachable")
}
