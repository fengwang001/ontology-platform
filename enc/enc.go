// Package enc implements variable-length quantity (LEB128) encoding for
// unsigned and signed 64-bit integers. Decoders reject non-canonical
// (non-minimal) encodings and overflows.
package enc

import "errors"

// Sentinel errors. All decode failures map to one of these so callers can
// distinguish empty input, overflow, and non-canonical input.
var (
	// ErrEmpty is returned when no byte is available at the decode cursor
	// (either the buffer was empty from the start, or it ended in the
	// middle of a varint).
	ErrEmpty = errors.New("enc: empty input")
	// ErrOverflow is returned when a value needs more than 10 bytes, the
	// 10th byte still carries a continuation bit, or a signed 10th byte
	// disagrees with the final sign.
	ErrOverflow = errors.New("enc: varint overflow")
	// ErrNonCanonical is returned for a non-minimal byte sequence: a
	// redundant trailing 0x00 group (unsigned), or a redundant trailing
	// 0x00/0x7F group inconsistent with the previous group's sign bit.
	ErrNonCanonical = errors.New("enc: non-canonical encoding")
)

// EncodeUint encodes v as unsigned LEB128: least-significant 7-bit group
// first, continuation bit 0x80 set on every byte except the last.
func EncodeUint(v uint64) []byte {
	out := make([]byte, 0, 10)
	for {
		b := byte(v & 0x7f)
		v >>= 7
		if v != 0 {
			out = append(out, b|0x80)
		} else {
			out = append(out, b)
			return out
		}
	}
}

// EncodeInt encodes v as signed LEB128. Groups are produced with an
// arithmetic (sign-extending) shift; a group terminates the sequence when
// either the remainder is 0 and its sign bit (0x40) is clear, or the
// remainder is -1 and its sign bit is set.
func EncodeInt(v int64) []byte {
	out := make([]byte, 0, 10)
	for {
		b := byte(v & 0x7f)
		v >>= 7 // arithmetic shift on a signed integer
		if (v == 0 && b&0x40 == 0) || (v == -1 && b&0x40 != 0) {
			out = append(out, b)
			return out
		}
		out = append(out, b|0x80)
	}
}

// DecodeUint decodes one unsigned varint from b. It returns the value, the
// number of bytes consumed, and a sentinel error. On error the consumed
// count is 0 so a cursor-based caller can leave its position untouched.
func DecodeUint(b []byte) (uint64, int, error) {
	var v uint64
	for i := 0; i < 10; i++ {
		if i >= len(b) {
			return 0, 0, ErrEmpty
		}
		c := b[i]
		if i == 9 {
			// The 10th byte carries only one value bit (bit 63): a
			// continuation bit, or any of payload bits 1..6, overflows.
			if c&0x80 != 0 || c&0x7e != 0 {
				return 0, 0, ErrOverflow
			}
		}
		v |= uint64(c&0x7f) << (uint(i) * 7)
		if c&0x80 == 0 {
			// Canonical form: in a multi-byte sequence the final group
			// must carry a nonzero 7-bit group.
			if i > 0 && c&0x7f == 0 {
				return 0, 0, ErrNonCanonical
			}
			return v, i + 1, nil
		}
	}
	return 0, 0, ErrOverflow
}

// DecodeInt decodes one signed varint from b. It returns the value, the
// number of bytes consumed, and a sentinel error; consumed is 0 on error.
func DecodeInt(b []byte) (int64, int, error) {
	var v uint64
	for i := 0; i < 10; i++ {
		if i >= len(b) {
			return 0, 0, ErrEmpty
		}
		c := b[i]
		if i == 9 {
			// Only the sign bit remains after 63 bits: the last group
			// must be 0x00 (remainder 0) or 0x7F (remainder -1).
			if p := c & 0x7f; c&0x80 != 0 || (p != 0x00 && p != 0x7f) {
				return 0, 0, ErrOverflow
			}
		}
		v |= uint64(c&0x7f) << (uint(i) * 7)
		if c&0x80 == 0 {
			if i > 0 {
				prev := b[i-1]
				if c == 0x00 && prev&0x40 == 0 {
					return 0, 0, ErrNonCanonical // redundant non-negative zero group
				}
				if c == 0x7f && prev&0x40 != 0 {
					return 0, 0, ErrNonCanonical // redundant negative sign group
				}
			}
			if c&0x40 != 0 && 7*(i+1) < 64 {
				v |= ^uint64(0) << (uint(i+1) * 7) // sign-extend
			}
			return int64(v), i + 1, nil
		}
	}
	return 0, 0, ErrOverflow
}
