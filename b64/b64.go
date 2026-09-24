// Package b64 implements single RFC 4648 base64 group (4 alphabet
// characters) encoding, decoding and strict canonical validation.
package b64

import "errors"

// Alphabet is the standard RFC 4648 base64 alphabet.
const Alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

// Padding is the canonical padding character.
const Padding = '='

var (
	// ErrIllegalChar reports a character outside the alphabet.
	ErrIllegalChar = errors.New("b64: illegal character")
	// ErrPadding reports a padding character in an illegal position or
	// a wrong number of padding characters.
	ErrPadding = errors.New("b64: padding position error")
)

var decode [256]byte

func init() {
	for i := range decode {
		decode[i] = 0xFF
	}
	for i := 0; i < len(Alphabet); i++ {
		decode[Alphabet[i]] = byte(i)
	}
}

// IsAlphabet reports whether c is a non-padding alphabet character.
func IsAlphabet(c byte) bool { return decode[c] != 0xFF }

// Value returns the 6-bit value of an alphabet character.
func Value(c byte) (byte, bool) {
	v := decode[c]
	return v, v != 0xFF
}

// EncodeGroup encodes 1 to 3 bytes into exactly 4 characters with
// canonical '=' padding.
func EncodeGroup(src []byte) [4]byte {
	var out [4]byte
	v0 := src[0]
	v1 := byte(0)
	if len(src) > 1 {
		v1 = src[1]
	}
	v2 := byte(0)
	if len(src) > 2 {
		v2 = src[2]
	}
	out[0] = Alphabet[(v0>>2)&0x3F]
	out[1] = Alphabet[((v0<<4)|(v1>>4))&0x3F]
	out[2] = Padding
	out[3] = Padding
	if len(src) >= 2 {
		out[2] = Alphabet[((v1<<2)|(v2>>6))&0x3F]
	}
	if len(src) >= 3 {
		out[3] = Alphabet[v2&0x3F]
	}
	return out
}

// DecodeGroup strictly decodes one canonical group of exactly 4 bytes.
// Non-canonical padding tails are reported as ErrPadding.
func DecodeGroup(group [4]byte) ([]byte, error) {
	v := [4]byte{}
	npad := 0
	for i, c := range group {
		if c == Padding {
			npad++
			continue
		}
		if !IsAlphabet(c) {
			return nil, ErrIllegalChar
		}
		v[i] = decode[c]
	}
	switch {
	case npad == 0:
		return []byte{
			(v[0] << 2) | (v[1] >> 4),
			(v[1] << 4) | (v[2] >> 2),
			(v[2] << 6) | v[3],
		}, nil
	case npad == 1 && group[3] == Padding:
		if v[2]&0x03 != 0 {
			return nil, ErrPadding
		}
		return []byte{
			(v[0] << 2) | (v[1] >> 4),
			(v[1] << 4) | (v[2] >> 2),
		}, nil
	case npad == 2 && group[2] == Padding && group[3] == Padding:
		if v[1]&0x0F != 0 {
			return nil, ErrPadding
		}
		return []byte{(v[0] << 2) | (v[1] >> 4)}, nil
	default:
		return nil, ErrPadding
	}
}

// IsValidGroup reports whether a 4-byte group is a canonical base64
// group (legal alphabet characters and a canonical padding tail).
func IsValidGroup(group [4]byte) bool {
	_, err := DecodeGroup(group)
	return err == nil
}
