// Package b64 provides the RFC 4648 Base64 alphabet and single-group codecs.
package b64

import "errors"

var (
	ErrInvalidChar  = errors.New("b64: invalid character")
	ErrNonCanonical = errors.New("b64: non-canonical tail bits")
	ErrPadding      = errors.New("b64: misplaced padding")
	ErrLength       = errors.New("b64: length not a multiple of 4")
	ErrNewline      = errors.New("b64: misplaced newline")
	ErrLimit        = errors.New("b64: output limit exceeded")
)

const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

var rev [256]int8

func init() {
	for i := range rev {
		rev[i] = -1
	}
	for i := 0; i < len(alphabet); i++ {
		rev[alphabet[i]] = int8(i)
	}
}

// Valid reports whether c is an alphabet character.
func Valid(c byte) bool { return rev[c] >= 0 }

// EncodeGroup appends the 4-char encoding of src (1 to 3 bytes) to dst.
func EncodeGroup(dst, src []byte) []byte {
	var bits uint32
	for i := 0; i < 3; i++ {
		bits <<= 8
		if i < len(src) {
			bits |= uint32(src[i])
		}
	}
	dst = append(dst, alphabet[bits>>18&63], alphabet[bits>>12&63])
	if len(src) > 1 {
		dst = append(dst, alphabet[bits>>6&63])
	} else {
		dst = append(dst, '=')
	}
	if len(src) > 2 {
		dst = append(dst, alphabet[bits&63])
	} else {
		dst = append(dst, '=')
	}
	return dst
}

// DecodeGroup decodes one 4-char group. It returns the decoded bytes, their
// count, the index of the offending character (-1 on success) and an error.
func DecodeGroup(g [4]byte) (out [3]byte, n, bad int, err error) {
	for i := 0; i < 4; i++ {
		if g[i] != '=' && !Valid(g[i]) {
			return out, 0, i, ErrInvalidChar
		}
	}
	pad := 0
	for i := 3; i >= 0 && g[i] == '='; i-- {
		pad++
	}
	if pad > 2 {
		return out, 0, 4 - pad, ErrPadding
	}
	for i := 0; i < 4-pad; i++ {
		if g[i] == '=' {
			return out, 0, i, ErrPadding
		}
	}
	v0, v1 := int(rev[g[0]]), int(rev[g[1]])
	out[0] = byte(v0<<2 | v1>>4)
	switch pad {
	case 2:
		if v1&0x0f != 0 {
			return out, 0, 1, ErrNonCanonical
		}
		return out, 1, -1, nil
	case 1:
		v2 := int(rev[g[2]])
		out[1] = byte(v1<<4 | v2>>2)
		if v2&0x03 != 0 {
			return out, 0, 2, ErrNonCanonical
		}
		return out, 2, -1, nil
	}
	v2, v3 := int(rev[g[2]]), int(rev[g[3]])
	out[1] = byte(v1<<4 | v2>>2)
	out[2] = byte(v2<<6 | v3)
	return out, 3, -1, nil
}
