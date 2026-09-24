// Package alpha maps 5-bit values to base32 characters and back.
// It has no dependencies on the other packages of this module.
package alpha

// Padding is the character used to fill a group up to 8 characters.
const Padding = '='

// alphabet holds the 32 characters indexed by 5-bit value.
const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"

// rev is the reverse lookup table; -1 marks characters outside the alphabet.
var rev [256]int8

func init() {
	for i := range rev {
		rev[i] = -1
	}
	for i := 0; i < len(alphabet); i++ {
		rev[alphabet[i]] = int8(i)
	}
}

// Char returns the character encoding the 5-bit value v (v must be < 32).
func Char(v byte) byte {
	return alphabet[v&31]
}

// Value returns the 5-bit value encoded by c. ok is false when c is
// not part of the alphabet (including the padding character).
func Value(c byte) (v byte, ok bool) {
	if rev[c] < 0 {
		return 0, false
	}
	return byte(rev[c]), true
}
