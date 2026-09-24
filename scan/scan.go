// Package scan classifies the characters of a parenthesis string.
package scan

// Kind is the classification of one character.
type Kind int

const (
	Left    Kind = iota // '('
	Right               // ')'
	Invalid             // anything else
)

// Classify reports the Kind of a single byte.
func Classify(b byte) Kind {
	switch b {
	case '(':
		return Left
	case ')':
		return Right
	}
	return Invalid
}

// FirstInvalid returns the index of the first character that is
// neither '(' nor ')', or -1 when every character is a parenthesis.
func FirstInvalid(s string) int {
	for i := 0; i < len(s); i++ {
		if Classify(s[i]) == Invalid {
			return i
		}
	}
	return -1
}
