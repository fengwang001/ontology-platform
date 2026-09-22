package ontology

import (
	"fmt"
	"strings"
)

// charset is the fixed, ordered alphabet every sort key is built from.
// Keys compare by plain byte-wise (dictionary) order, which matches the
// index order of these bytes.
//
// The first character ('0') is reserved as an internal "floor": generated
// keys never end with it, so between any two generated keys there is
// always room for another key (no dead ends).
const charset = "0123456789abcdefghijklmnopqrstuvwxyz"

// Derived boundary bytes of charset.
var (
	firstChar = charset[0]              // smallest allowed byte
	lastChar  = charset[len(charset)-1] // largest allowed byte
	midChar   = charset[len(charset)/2] // used for unconstrained gaps
)

// DefaultMaxKeyLen is the key length limit used when none is specified.
const DefaultMaxKeyLen = 16

// charIndex returns the rank of b within charset, or -1 if not allowed.
func charIndex(b byte) int {
	return strings.IndexByte(charset, b)
}

// validateKey rejects keys containing bytes outside charset.
func validateKey(key string) error {
	for i := 0; i < len(key); i++ {
		if charIndex(key[i]) < 0 {
			return fmt.Errorf("%w: %q at offset %d in key %q",
				ErrInvalidChar, string(key[i]), i, key)
		}
	}
	return nil
}
