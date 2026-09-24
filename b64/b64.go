// Package b64 implements strict single-group Base64 primitives and the
// shared error vocabulary for strict decoding.
package b64

import (
	"errors"
	"fmt"
)

// Alphabet is the RFC 4648 standard alphabet.
const Alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

var (
	ErrInvalidChar  = errors.New("invalid character")
	ErrNonCanonical = errors.New("non-canonical tail bits")
	ErrPadding      = errors.New("misplaced padding")
	ErrLength       = errors.New("length not a multiple of 4")
	ErrNewline      = errors.New("misplaced newline")
	ErrLimit        = errors.New("output limit exceeded")
	ErrClosed       = errors.New("stream already finished")
)

// Error reports a strict-mode failure at an input byte offset.
type Error struct {
	Kind error
	Off  int64
}

func (e *Error) Error() string { return fmt.Sprintf("%v at byte %d", e.Kind, e.Off) }
func (e *Error) Unwrap() error { return e.Kind }

var rev [256]int8

func init() {
	for i := range rev {
		rev[i] = -1
	}
	for i := 0; i < 64; i++ {
		rev[Alphabet[i]] = int8(i)
	}
}

// Value returns the 6-bit value of c, or -1 if c is not in the alphabet.
func Value(c byte) int { return int(rev[c]) }

// Kind classifies a group decoding failure.
type Kind int

const (
	OK Kind = iota
	BadChar
	BadPad
	NonCanonical
)

// EncodeGroup appends the 4-character encoding of b (1 to 3 bytes) to dst.
func EncodeGroup(dst []byte, b ...byte) []byte {
	var v [3]byte
	copy(v[:], b)
	dst = append(dst, Alphabet[v[0]>>2], Alphabet[(v[0]<<4|v[1]>>4)&0x3F])
	if len(b) == 1 {
		return append(dst, '=', '=')
	}
	dst = append(dst, Alphabet[(v[1]<<2|v[2]>>6)&0x3F])
	if len(b) == 2 {
		return append(dst, '=')
	}
	return append(dst, Alphabet[v[2]&0x3F])
}

// DecodeGroup decodes one 4-character group into out. It returns the
// number of decoded bytes, the index of the offending character on
// failure, and the failure kind. Unused tail bits must be zero.
func DecodeGroup(g [4]byte, out *[3]byte) (int, int, Kind) {
	pad := 0
	for i := 3; i >= 0 && g[i] == '='; i-- {
		pad++
	}
	if pad > 2 {
		return 0, 4 - pad, BadPad
	}
	var v [4]int
	for i := 0; i < 4-pad; i++ {
		if g[i] == '=' {
			return 0, i, BadPad
		}
		v[i] = Value(g[i])
		if v[i] < 0 {
			return 0, i, BadChar
		}
	}
	switch pad {
	case 2:
		if v[1]&0x0F != 0 {
			return 0, 1, NonCanonical
		}
	case 1:
		if v[2]&0x03 != 0 {
			return 0, 2, NonCanonical
		}
	}
	out[0] = byte(v[0]<<2 | v[1]>>4)
	if pad < 2 {
		out[1] = byte(v[1]<<4 | v[2]>>2)
	}
	if pad < 1 {
		out[2] = byte(v[2]<<6 | v[3])
	}
	return 3 - pad, 0, OK
}
