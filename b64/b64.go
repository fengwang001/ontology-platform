// Package b64 implements the RFC 4648 standard Base64 alphabet and the
// encoding/decoding of a single 4-character group in strict canonical form.
package b64

import "errors"

// Alphabet is the RFC 4648 standard Base64 alphabet.
const Alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

// Padding is the canonical padding byte.
const Padding = '='

var (
	// ErrInvalidChar reports a byte that is not an alphabet character.
	ErrInvalidChar = errors.New("b64: invalid base64 character")
	// ErrPadding reports an '=' in an illegal position or shape.
	ErrPadding = errors.New("b64: illegal padding placement")
	// ErrNonCanonical reports a valid-looking tail whose dropped bits are nonzero.
	ErrNonCanonical = errors.New("b64: non-canonical tail group")
)

var decodeTable [256]int8

func init() {
	for i := range decodeTable {
		decodeTable[i] = -1
	}
	for i := 0; i < len(Alphabet); i++ {
		decodeTable[Alphabet[i]] = int8(i)
	}
}

// Value returns the 6-bit value of an alphabet character.
func Value(c byte) (int, bool) {
	v := decodeTable[c]
	if v < 0 {
		return 0, false
	}
	return int(v), true
}

// EncodeGroup encodes 1 to 3 input bytes into one padded 4-character group.
func EncodeGroup(src []byte) [4]byte {
	var g [4]byte
	switch len(src) {
	case 1:
		g[0] = Alphabet[src[0]>>2]
		g[1] = Alphabet[(src[0]&0x03)<<4]
		g[2], g[3] = Padding, Padding
	case 2:
		g[0] = Alphabet[src[0]>>2]
		g[1] = Alphabet[((src[0]&0x0f)<<4)|(src[1]>>4)]
		g[2] = Alphabet[(src[1]&0x0f)<<2]
		g[3] = Padding
	default: // 3 bytes
		g[0] = Alphabet[src[0]>>2]
		g[1] = Alphabet[((src[0]&0x0f)<<4)|(src[1]>>4)]
		g[2] = Alphabet[((src[1]&0x03)<<6)|(src[2]>>6)]
		g[3] = Alphabet[src[2]&0x3f]
	}
	return g
}

// DecodeGroup decodes one 4-character group in strict mode. It returns the
// decoded bytes and their count (1, 2 or 3). Padding shape is validated, and
// the bits dropped by a padded tail must all be zero (canonical form).
func DecodeGroup(g [4]byte) ([3]byte, int, error) {
	var v [4]int
	outLen := 3
	switch {
	case g[2] == Padding && g[3] == Padding:
		outLen = 1
	case g[3] == Padding:
		outLen = 2
	case g[0] == Padding || g[1] == Padding || g[2] == Padding:
		return [3]byte{}, 0, ErrPadding
	}
	for i := 0; i < outLen+1; i++ {
		if g[i] == Padding {
			return [3]byte{}, 0, ErrPadding
		}
		n, ok := Value(g[i])
		if !ok {
			return [3]byte{}, 0, ErrInvalidChar
		}
		v[i] = n
	}

	var out [3]byte
	out[0] = byte(v[0]<<2) | byte(v[1]>>4)
	switch outLen {
	case 1:
		if v[1]&0x0f != 0 { // low 4 bits of char 2 are dropped
			return [3]byte{}, 0, ErrNonCanonical
		}
	case 2:
		if v[2]&0x03 != 0 { // low 2 bits of char 3 are dropped
			return [3]byte{}, 0, ErrNonCanonical
		}
		out[1] = byte(v[1]<<4) | byte(v[2]>>2)
	default:
		out[1] = byte(v[1]<<4) | byte(v[2]>>2)
		out[2] = byte(v[2]<<6) | byte(v[3])
	}
	return out, outLen, nil
}
