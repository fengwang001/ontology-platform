// Package b64 implements the RFC 4648 standard alphabet and strict
// encode/decode of a single 4-character group.
package b64

import "errors"

const Alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

var (
	ErrInvalidChar  = errors.New("b64: invalid character")
	ErrPadding      = errors.New("b64: misplaced padding")
	ErrNonCanonical = errors.New("b64: non-canonical trailing bits")
)

var rev [256]int16

func init() {
	for i := range rev {
		rev[i] = -1
	}
	for i := 0; i < len(Alphabet); i++ {
		rev[Alphabet[i]] = int16(i)
	}
	rev['='] = -2
}

// Valid reports whether c is an alphabet character (not '=').
func Valid(c byte) bool { return rev[c] >= 0 }

// GroupError locates a decoding failure inside a 4-character group.
type GroupError struct {
	Kind error
	Idx  int
}

func (e *GroupError) Error() string { return e.Kind.Error() }
func (e *GroupError) Unwrap() error { return e.Kind }

// EncodeGroup encodes 1 to 3 bytes into one 4-character group,
// padding with '=' as needed.
func EncodeGroup(b []byte) (g [4]byte) {
	var v [3]byte
	copy(v[:], b)
	g[0] = Alphabet[v[0]>>2]
	g[1] = Alphabet[v[0]<<4&0x30|v[1]>>4]
	g[2] = Alphabet[v[1]<<2&0x3c|v[2]>>6]
	g[3] = Alphabet[v[2]&0x3f]
	switch len(b) {
	case 1:
		g[2], g[3] = '=', '='
	case 2:
		g[3] = '='
	}
	return g
}

// DecodeGroup decodes one 4-character group. It returns the decoded
// bytes and their count n (1..3). A group with padding yields n < 3
// and is only canonical when the bits that reach no output byte are
// all zero: the low 4 bits of char 1 for "xx==", the low 2 bits of
// char 2 for "xxx=".
func DecodeGroup(g [4]byte) (out [3]byte, n int, err *GroupError) {
	var v [4]int
	npad := 0
	for i := 0; i < 4; i++ {
		r := rev[g[i]]
		switch {
		case r >= 0:
			if npad > 0 {
				return out, 0, &GroupError{ErrPadding, i}
			}
			v[i] = int(r)
		case r == -2:
			if i < 2 {
				return out, 0, &GroupError{ErrPadding, i}
			}
			npad++
		default:
			return out, 0, &GroupError{ErrInvalidChar, i}
		}
	}
	switch npad {
	case 1:
		if v[2]&3 != 0 {
			return out, 0, &GroupError{ErrNonCanonical, 2}
		}
	case 2:
		if v[1]&15 != 0 {
			return out, 0, &GroupError{ErrNonCanonical, 1}
		}
	}
	n = 3 - npad
	out[0] = byte(v[0]<<2 | v[1]>>4)
	out[1] = byte(v[1]<<4 | v[2]>>2)
	out[2] = byte(v[2]<<6 | v[3])
	return out, n, nil
}
