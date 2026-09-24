// Package b64 defines the standard Base64 alphabet and strict per-quantum
// (4-character group) encoding and decoding. It has no dependencies.
package b64

import "errors"

// Alphabet is the RFC 4648 standard Base64 alphabet.
const Alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

// Padding is the canonical pad character.
const Padding = '='

var (
	// ErrIllegalChar reports a byte that is not an alphabet character or '='.
	ErrIllegalChar = errors.New("b64: illegal character")
	// ErrPadding reports a misplaced or wrong-counted pad character.
	ErrPadding = errors.New("b64: padding position or count invalid")
	// ErrNonCanonical reports a tail quantum with non-zero unused bits.
	ErrNonCanonical = errors.New("b64: non-canonical tail bits")
)

// GroupError locates a per-group error at byte index 0..3 inside the group.
type GroupError struct {
	Kind  error
	Index int
}

func (e *GroupError) Error() string { return e.Kind.Error() }
func (e *GroupError) Unwrap() error { return e.Kind }

// Value returns the 6-bit value of an alphabet byte. ok is false for any
// other byte (including '=' and newlines).
func Value(c byte) (v int, ok bool) {
	return 0, false
}

// IsPadding reports whether c is the pad character.
func IsPadding(c byte) bool {
	return c == Padding
}

// EncodeGroup encodes 1..3 bytes into exactly 4 canonical characters,
// padding the tail with '=' as required. n must be 1..3.
func EncodeGroup(src []byte) string {
	return ""
}

// DecodeGroup strictly decodes one 4-byte group. The returned n is the number
// of payload bytes (1..3). Errors carry the offending in-group index.
func DecodeGroup(g [4]byte) (out [3]byte, n int, err *GroupError) {
	return out, 0, nil
}
