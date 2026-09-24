// Package b64 implements the RFC 4648 standard alphabet and the
// coding of a single 4-character quantum with canonical-form checks.
package b64

import "errors"

const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

// rev maps a byte to its sextet value: >=0 data, -1 invalid, -2 '='.
var rev [256]int8

func init() {
	for i := range rev {
		rev[i] = -1
	}
	for i := 0; i < 64; i++ {
		rev[alphabet[i]] = int8(i)
	}
	rev['='] = -2
}

var (
	ErrChar = errors.New("b64: invalid character")
	ErrPad  = errors.New("b64: misplaced padding")
	ErrBits = errors.New("b64: non-canonical trailing bits")
)

// Valid reports whether c is a standard-alphabet character.
func Valid(c byte) bool { return rev[c] >= 0 }

// AppendEncode appends the 4-char encoding of src (1-3 bytes) to dst.
func AppendEncode(dst, src []byte) []byte {
	n := len(src)
	var v uint32
	for i := 0; i < 3; i++ {
		v <<= 8
		if i < n {
			v |= uint32(src[i])
		}
	}
	for p := 0; p < 4; p++ {
		if p > n {
			dst = append(dst, '=')
		} else {
			dst = append(dst, alphabet[v>>(18-6*uint(p))&63])
		}
	}
	return dst
}

// DecodeGroup decodes one 4-char quantum into 1-3 bytes, rejecting
// misplaced padding and non-zero trailing bits (non-canonical form).
func DecodeGroup(g [4]byte) ([]byte, error) {
	var v [4]int
	pad := 0
	for i := 0; i < 4; i++ {
		switch r := rev[g[i]]; {
		case r >= 0:
			if pad > 0 {
				return nil, ErrPad
			}
			v[i] = int(r)
		case r == -2:
			if i < 2 {
				return nil, ErrPad
			}
			pad++
		default:
			return nil, ErrChar
		}
	}
	switch pad {
	case 2:
		if v[1]&15 != 0 {
			return nil, ErrBits
		}
	case 1:
		if v[2]&3 != 0 {
			return nil, ErrBits
		}
	}
	b := uint32(v[0])<<18 | uint32(v[1])<<12 | uint32(v[2])<<6 | uint32(v[3])
	return []byte{byte(b >> 16), byte(b >> 8), byte(b)}[:3-pad], nil
}
