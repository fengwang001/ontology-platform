// Package b64 implements the RFC 4648 standard alphabet and strict
// single-group (4-character) encode/decode with canonical-form checks.
package b64

import "fmt"

// Alphabet is the standard RFC 4648 Base64 alphabet.
const Alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

// Kind classifies a group decoding failure.
type Kind int

const (
	KindChar  Kind = iota // character outside the alphabet
	KindPad               // '=' in a position that breaks the padding shape
	KindCanon             // non-zero trailing (unused) bits: non-canonical form
)

// Error describes a group decoding failure; Pos is the index (0..3) of the
// offending character inside the group.
type Error struct {
	Kind Kind
	Pos  int
}

func (e *Error) Error() string {
	name := [...]string{"invalid character", "bad padding", "non-canonical trailing bits"}[e.Kind]
	return fmt.Sprintf("b64: %s at group position %d", name, e.Pos)
}

var rev [256]int8

func init() {
	for i := range rev {
		rev[i] = -1
	}
	for i := 0; i < 64; i++ {
		rev[Alphabet[i]] = int8(i)
	}
}

// Value maps an alphabet character to its 6-bit value.
func Value(c byte) (int, bool) {
	v := rev[c]
	return int(v), v >= 0
}

// Char maps a 6-bit value to its alphabet character.
func Char(v byte) byte { return Alphabet[v&0x3f] }

// AppendEncode appends the 4-character encoding of src (1..3 bytes) to dst,
// padding with '=' as needed.
func AppendEncode(dst, src []byte) []byte {
	var b [3]byte
	copy(b[:], src)
	dst = append(dst, Char(b[0]>>2), Char(b[0]<<4|b[1]>>4), Char(b[1]<<2|b[2]>>6), Char(b[2]))
	switch len(src) {
	case 1:
		dst[len(dst)-2], dst[len(dst)-1] = '=', '='
	case 2:
		dst[len(dst)-1] = '='
	}
	return dst
}

// DecodeGroup strictly decodes one 4-character group, returning 1..3 bytes.
// Padding is only valid as "XX==" or "XXX=", and the unused trailing bits of
// the last data character must be zero (canonical form).
func DecodeGroup(g [4]byte) ([]byte, error) {
	var v [4]int
	for i := 0; i < 2; i++ {
		val, ok := Value(g[i])
		if !ok {
			if g[i] == '=' {
				return nil, &Error{KindPad, i}
			}
			return nil, &Error{KindChar, i}
		}
		v[i] = val
	}
	n := 3
	if g[2] == '=' {
		if g[3] != '=' {
			return nil, &Error{KindPad, 3}
		}
		if v[1]&0x0f != 0 {
			return nil, &Error{KindCanon, 1}
		}
		n = 1
	} else {
		val, ok := Value(g[2])
		if !ok {
			return nil, &Error{KindChar, 2}
		}
		v[2] = val
		if g[3] == '=' {
			if v[2]&0x03 != 0 {
				return nil, &Error{KindCanon, 2}
			}
			n = 2
		} else {
			val, ok := Value(g[3])
			if !ok {
				return nil, &Error{KindChar, 3}
			}
			v[3] = val
		}
	}
	out := []byte{byte(v[0]<<2 | v[1]>>4), byte(v[1]<<4 | v[2]>>2), byte(v[2]<<6 | v[3])}
	return out[:n], nil
}
