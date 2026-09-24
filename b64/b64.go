// Package b64 implements the RFC 4648 standard Base64 alphabet and the
// encoding/decoding of a single 4-character quantum, including strict
// canonical-tail validation. It has no dependencies beyond the standard
// library and never imports encoding/base64.
package b64

import "errors"

// Alphabet is the RFC 4648 standard Base64 alphabet.
const Alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

// Padding is the canonical padding character.
const Padding byte = '='

var (
	// ErrIllegalChar reports a byte outside the alphabet and not '='.
	ErrIllegalChar = errors.New("b64: illegal base64 character")
	// ErrBadPadding reports an '=' in a structurally forbidden position.
	ErrBadPadding = errors.New("b64: padding character in wrong position")
	// ErrNonCanonical reports unused tail bits that are not zero.
	ErrNonCanonical = errors.New("b64: non-canonical tail bits")
)

var decode [256]int8

func init() {
	for i := range decode {
		decode[i] = -1
	}
	for i := 0; i < len(Alphabet); i++ {
		decode[Alphabet[i]] = int8(i)
	}
	decode[Padding] = -2
}

// Value reports the 6-bit value of an alphabet character. It returns -1 for
// any other byte (including '=' and whitespace).
func Value(c byte) int {
	v := decode[c]
	if v < 0 {
		return -1
	}
	return int(v)
}

// IsPad reports whether c is the padding character.
func IsPad(c byte) bool { return c == Padding }

// EncodeGroup encodes 1 to 3 input bytes into exactly 4 output characters,
// padding with '=' when len(src) < 3.
func EncodeGroup(dst []byte, src []byte) {
	v0 := int(src[0])
	dst[0] = Alphabet[v0>>2]
	switch len(src) {
	case 1:
		dst[1] = Alphabet[(v0<<4)&63]
		dst[2] = Padding
		dst[3] = Padding
	default: // 2 or 3
		v1 := int(src[1])
		dst[1] = Alphabet[(v0<<4)|(v1>>2)]
		if len(src) == 2 {
			dst[2] = Alphabet[(v1<<4)&63]
			dst[3] = Padding
		} else {
			v2 := int(src[2])
			dst[2] = Alphabet[(v1<<4)|(v2>>6)]
			dst[3] = Alphabet[v2&63]
		}
	}
}

// DecodeGroup decodes exactly 4 characters into 1, 2 or 3 bytes. The input
// must contain only alphabet characters and '='; the padding shape and the
// unused tail bits are validated per the strict canonical rules.
func DecodeGroup(g [4]byte) ([]byte, error) {
	var s [3]int
	for i := 0; i < 4; i++ {
		if decode[g[i]] == -1 {
			return nil, ErrIllegalChar
		}
	}
	switch {
	case !IsPad(g[2]) && !IsPad(g[3]):
		s[0], s[1], s[2] = Value(g[1]), Value(g[2]), 0
	case IsPad(g[2]) && IsPad(g[3]):
		if IsPad(g[0]) || IsPad(g[1]) {
			return nil, ErrBadPadding
		}
		s[0] = Value(g[1])
		if s[0]&0b1111 != 0 {
			return nil, ErrNonCanonical
		}
	case !IsPad(g[2]) && IsPad(g[3]):
		if IsPad(g[0]) || IsPad(g[1]) {
			return nil, ErrBadPadding
		}
		s[0], s[1] = Value(g[1]), Value(g[2])
		if s[1]&0b11 != 0 {
			return nil, ErrNonCanonical
		}
	default:
		return nil, ErrBadPadding
	}
	v0 := Value(g[0])
	if IsPad(g[3]) && IsPad(g[2]) {
		return []byte{byte(v0<<2 | s[0]>>4)}, nil
	}
	if IsPad(g[3]) {
		return []byte{byte(v0<<2 | s[0]>>4), byte(s[0]<<4 | s[1]>>2)}, nil
	}
	s[2] = Value(g[3])
	return []byte{
		byte(v0<<2 | s[0]>>4),
		byte(s[0]<<4 | s[1]>>2),
		byte(s[1]<<6 | s[2]),
	}, nil
}
