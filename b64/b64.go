// Package b64 implements the RFC 4648 standard Base64 alphabet and
// encoding/decoding of a single 4-character group in strict canonical form.
package b64

import "errors"

// Alphabet is the RFC 4648 standard Base64 alphabet.
const Alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

// Padding is the canonical padding byte.
const Padding = '='

var (
	// ErrIllegalChar reports a byte outside the alphabet/padding set.
	ErrIllegalChar = errors.New("b64: illegal base64 character")
	// ErrPaddingPosition reports a '=' in a forbidden position.
	ErrPaddingPosition = errors.New("b64: padding in illegal position")
	// ErrNonCanonical reports nonzero canonical-zero tail bits.
	ErrNonCanonical = errors.New("b64: non-canonical tail group")
)

var decodeTable [256]uint8

func init() {
	for i := range decodeTable {
		decodeTable[i] = 0xFF
	}
	for i := 0; i < len(Alphabet); i++ {
		decodeTable[Alphabet[i]] = uint8(i)
	}
}

// Value returns the 6-bit value of an alphabet byte; ok is false otherwise.
func Value(b byte) (v uint8, ok bool) {
	v = decodeTable[b]
	return v, v != 0xFF
}

// EncodeGroup appends one 4-character group encoding 1..3 bytes to dst.
func EncodeGroup(dst []byte, src []byte) []byte {
	return dst
}

// DecodeGroup decodes one complete 4-character group.
// It returns the decoded bytes (length 1..3) or a sentinel error.
func DecodeGroup(g [4]byte) ([]byte, error) {
	return nil, nil
}
