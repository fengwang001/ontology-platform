// Package b64 implements RFC 4648 standard-alphabet Base64 one-quantum
// (4-character group) encoding and strict, canonical decoding.
package b64

import "errors"

// Alphabet is the RFC 4648 standard Base64 alphabet.
const Alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

// Errors for a single quantum. They are the root causes that the stream
// package wraps with a byte offset.
var (
	// ErrIllegalChar: a byte that is not an alphabet char, '=' or newline.
	ErrIllegalChar = errors.New("b64: illegal base64 character")
	// ErrPadding: an '=' appears where the canonical layout forbids it.
	ErrPadding = errors.New("b64: padding in illegal position")
	// ErrNonCanonical: valid layout, but trailing unused bits are nonzero.
	ErrNonCanonical = errors.New("b64: non-canonical tail (unused bits nonzero)")
)

var decodeTab [256]byte

func init() {
	for i := range decodeTab {
		decodeTab[i] = 0xFF
	}
	for i := 0; i < len(Alphabet); i++ {
		decodeTab[Alphabet[i]] = byte(i)
	}
}

// Value returns the 6-bit value of an alphabet character.
func Value(c byte) (byte, bool) {
	v := decodeTab[c]
	return v, v != 0xFF
}

// EncodeGroup encodes 1-3 bytes into a 4-character quantum with canonical
// '=' padding. n is len(src) and must be 1..3.
func EncodeGroup(src []byte) [4]byte {
	var out [4]byte
	var v [3]byte
	n := len(src)
	copy(v[:], src)
	out[0] = Alphabet[v[0]>>2]
	out[1] = Alphabet[(v[0]<<4)&0x30|v[1]>>4]
	out[2] = '='
	out[3] = '='
	if n >= 2 {
		out[2] = Alphabet[(v[1]<<2)&0x3C|v[2]>>6]
	}
	if n >= 3 {
		out[3] = Alphabet[v[2]&0x3F]
	}
	return out
}

// DecodeGroup decodes one fully collected 4-byte quantum, returning the 1-3
// data bytes it carries. It enforces canonical padding placement and zero
// trailing unused bits.
func DecodeGroup(g [4]byte) ([]byte, error) {
	var v [4]byte
	for i := 0; i < 4; i++ {
		if g[i] == '=' {
			v[i] = 0 // padding handled structurally below
			continue
		}
		val, ok := Value(g[i])
		if !ok {
			return nil, ErrIllegalChar
		}
		v[i] = val
	}

	switch {
	case g[2] == '=' && g[3] == '=': // 1 data byte
		if g[0] == '=' || g[1] == '=' {
			return nil, ErrPadding
		}
		if v[1]&0x0F != 0 { // low 4 bits of v1 are unused
			return nil, ErrNonCanonical
		}
		return []byte{v[0]<<2 | v[1]>>4}, nil
	case g[3] == '=': // 2 data bytes; g[2] must be a value char
		if g[0] == '=' || g[1] == '=' || g[2] == '=' {
			return nil, ErrPadding
		}
		if v[2]&0x03 != 0 { // low 2 bits of v2 are unused
			return nil, ErrNonCanonical
		}
		return []byte{v[0]<<2 | v[1]>>4, v[1]<<4 | v[2]>>2}, nil
	case g[0] == '=' || g[1] == '=' || g[2] == '=':
		// '=' in slot 0/1, or slot 2 without slot 3: never valid.
		return nil, ErrPadding
	default: // 3 data bytes, no padding
		return []byte{
			v[0]<<2 | v[1]>>4,
			v[1]<<4 | v[2]>>2,
			v[2]<<6 | v[3],
		}, nil
	}
}
