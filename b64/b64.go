// Package b64 implements RFC 4648 standard-alphabet single-group base64
// encoding, decoding and strict canonical validation. It has no dependencies.
package b64

import "errors"

// Alphabet is the RFC 4648 standard base64 alphabet.
const Alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

// Padding is the canonical padding character.
const Padding = '='

var (
	// ErrIllegalChar means a character is not in the alphabet nor legal padding.
	ErrIllegalChar = errors.New("b64: illegal base64 character")
	// ErrPadding means padding occurs in an illegal shape or position.
	ErrPadding = errors.New("b64: illegal padding")
	// ErrNonCanonical means the residual bits behind padding are not all zero.
	ErrNonCanonical = errors.New("b64: non-canonical tail group")
)

var decodeTable [256]byte

func init() {
	for i := range decodeTable {
		decodeTable[i] = 0xff
	}
	for i := 0; i < len(Alphabet); i++ {
		decodeTable[Alphabet[i]] = byte(i)
	}
}

// Value returns the 6-bit value of c and true, or 0,false for non-alphabet bytes.
func Value(c byte) (byte, bool) {
	v := decodeTable[c]
	return v, v != 0xff
}

// EncodeGroup encodes 1-3 input bytes into exactly 4 canonical base64 bytes,
// appending '=' padding as required.
func EncodeGroup(src []byte) []byte {
	if len(src) < 1 || len(src) > 3 {
		panic("b64: EncodeGroup requires 1 to 3 bytes")
	}
	out := []byte{'=', '=', '=', '='}
	out[0] = Alphabet[src[0]>>2]
	switch len(src) {
	case 1:
		out[1] = Alphabet[(src[0]&0x0f)<<4]
	case 2:
		out[1] = Alphabet[((src[0]&0x0f)<<4)|(src[1]>>4)]
		out[2] = Alphabet[(src[1]&0x0f)<<2]
	default:
		out[1] = Alphabet[((src[0]&0x0f)<<4)|(src[1]>>4)]
		out[2] = Alphabet[((src[1]&0x0f)<<2)|(src[2]>>6)]
		out[3] = Alphabet[src[2]&0x3f]
	}
	return out
}

// DecodeGroup decodes exactly 4 characters, which must be one of:
// "XXXX" (3 bytes), "XXX=" (2 bytes) or "XX==" (1 byte).
// Alphabet errors take priority; only valid padding shapes reach the
// canonical residual-bit check.
func DecodeGroup(g [4]byte) ([]byte, error) {
	v := [4]byte{}
	for i := 0; i < 4; i++ {
		val, ok := Value(g[i])
		if !ok && g[i] != Padding {
			return nil, ErrIllegalChar
		}
		v[i] = val
	}
	if g[0] == Padding || g[1] == Padding {
		return nil, ErrPadding
	}
	switch {
	case g[2] != Padding && g[3] != Padding:
		return []byte{
			v[0]<<2 | v[1]>>4,
			v[1]<<4 | v[2]>>2,
			v[2]<<6 | v[3],
		}, nil
	case g[2] == Padding && g[3] == Padding:
		if g[0] == Padding || g[1] == Padding {
			return nil, ErrPadding
		}
		if v[1]&0x0f != 0 {
			return nil, ErrNonCanonical
		}
		return []byte{v[0]<<2 | v[1]>>4}, nil
	case g[2] != Padding && g[3] == Padding:
		if v[2]&0x03 != 0 {
			return nil, ErrNonCanonical
		}
		return []byte{v[0]<<2 | v[1]>>4, v[1]<<4 | v[2]>>2}, nil
	default:
		// g[2]=='=' and g[3]!='=': padding may only occupy the final slot.
		return nil, ErrPadding
	}
}
