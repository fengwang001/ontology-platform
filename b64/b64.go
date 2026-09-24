// Package b64 implements RFC 4648 standard Base64 alphabet and strict
// encode/decode of a single 4-character group. It has no dependencies.
package b64

import "errors"

// Alphabet is the RFC 4648 standard Base64 alphabet.
const Alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

// Pad is the canonical padding character.
const Pad = '='

var (
	// ErrInvalidChar means a byte is not in the alphabet and is not '='.
	ErrInvalidChar = errors.New("b64: invalid base64 character")
	// ErrPadding means padding is missing/misplaced inside a group.
	ErrPadding = errors.New("b64: padding position error")
	// ErrNonCanonical means the unused tail bits of the last group are nonzero.
	ErrNonCanonical = errors.New("b64: non-canonical tail")
)

var decodeTable [256]int8

func init() {
	for i := range decodeTable {
		decodeTable[i] = -1
	}
	for i := 0; i < len(Alphabet); i++ {
		decodeTable[Alphabet[i]] = int8(i)
	}
	decodeTable[Pad] = -2
}

// Value returns the 6-bit value of an alphabet byte and ok==true.
// A padding byte reports ok==false with pad==true; any other byte is invalid.
func Value(c byte) (v int, ok, pad bool) {
	x := decodeTable[c]
	switch {
	case x >= 0:
		return int(x), true, false
	case x == -2:
		return 0, false, true
	default:
		return 0, false, false
	}
}

// EncodeGroup encodes 1..3 bytes into exactly 4 RFC 4648 characters.
// n must be 1, 2 or 3.
func EncodeGroup(src []byte, n int) [4]byte {
	var g [4]byte
	v0 := int(src[0]) << 16
	v1, v2 := 0, 0
	if n >= 2 {
		v1 = int(src[1]) << 8
	}
	if n >= 3 {
		v2 = int(src[2])
	}
	v := v0 | v1 | v2
	g[0] = Alphabet[v>>18&0x3F]
	g[1] = Alphabet[v>>12&0x3F]
	g[2], g[3] = Pad, Pad
	if n >= 2 {
		g[2] = Alphabet[v>>6&0x3F]
	}
	if n >= 3 {
		g[3] = Alphabet[v&0x3F]
	}
	return g
}

// GroupError is a single-group decode failure. Index is the byte offset
// inside the 4-character group that should be reported.
type GroupError struct {
	Err   error
	Index int
}

func (e *GroupError) Error() string { return e.Err.Error() }
func (e *GroupError) Unwrap() error { return e.Err }

// DecodeGroup strictly decodes one 4-character group. It returns 1..3 decoded
// bytes (3 for an unpadded group, 2 for "XXX=", 1 for "XX==") and rejects
// invalid characters, bad padding shapes, and non-canonical tail bits.
func DecodeGroup(g [4]byte) ([]byte, error) {
	val := [4]int{}
	for i, c := range g {
		v, ok, pad := Value(c)
		if !ok && !pad {
			return nil, &GroupError{Err: ErrInvalidChar, Index: i}
		}
		if pad {
			val[i] = -1
		} else {
			val[i] = v
		}
	}
	n := 3
	switch {
	case val[2] == -1 && val[3] == -1:
		if val[0] == -1 || val[1] == -1 {
			return nil, &GroupError{Err: ErrPadding, Index: firstPad(g)}
		}
		n = 1
	case val[3] == -1:
		if val[0] == -1 || val[1] == -1 || val[2] == -1 {
			return nil, &GroupError{Err: ErrPadding, Index: firstPad(g)}
		}
		n = 2
	case val[0] == -1 || val[1] == -1 || val[2] == -1:
		return nil, &GroupError{Err: ErrPadding, Index: firstPad(g)}
	}
	v := val[0]<<18 | val[1]<<12
	if n >= 2 {
		v |= val[2] << 6
	}
	if n >= 3 {
		v |= val[3]
	}
	b0 := byte(v >> 16)
	switch n {
	case 1:
		if val[1]&0x0F != 0 {
			return nil, &GroupError{Err: ErrNonCanonical, Index: 1}
		}
		return []byte{b0}, nil
	case 2:
		if val[2]&0x03 != 0 {
			return nil, &GroupError{Err: ErrNonCanonical, Index: 2}
		}
		return []byte{b0, byte(v >> 8)}, nil
	default:
		return []byte{b0, byte(v >> 8), byte(v)}, nil
	}
}

func firstPad(g [4]byte) int {
	for i, c := range g {
		if c == Pad {
			return i
		}
	}
	return 0
}
