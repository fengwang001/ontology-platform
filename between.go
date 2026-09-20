package ontology

import (
	"fmt"
	"strings"
)

// Generator creates sort keys between two neighbor keys. Implementations
// must be deterministic: the same (left, right) pair always yields the
// same key, and the result must satisfy left < key < right.
type Generator interface {
	// Between returns a key strictly between left and right. An empty
	// string means "no neighbor" on that side (front or back insert).
	// maxLen caps the result length; exceeding it must fail with
	// ErrNeedsRebalance.
	Between(left, right string, maxLen int) (string, error)
}

// LexGenerator is the default Generator. It walks the common prefix of
// the neighbors and picks the midpoint character at the first position
// where they differ, recursing one level when the gap is too tight.
// The result never ends with firstChar, so it never creates a dead end.
type LexGenerator struct{}

// NewLexGenerator returns the default deterministic key generator.
func NewLexGenerator() LexGenerator { return LexGenerator{} }

// Between implements Generator.
func (LexGenerator) Between(left, right string, maxLen int) (string, error) {
	if err := validateKey(left); err != nil {
		return "", err
	}
	if err := validateKey(right); err != nil {
		return "", err
	}
	if left != "" && right != "" && left >= right {
		return "", fmt.Errorf("%w: left %q, right %q", ErrInvalidOrder, left, right)
	}
	if right != "" && len(right) > len(left) && strings.HasPrefix(right, left) &&
		strings.Trim(right[len(left):], string(firstChar)) == "" {
		return "", fmt.Errorf("%w: left %q, right %q", ErrNoRoom, left, right)
	}
	if maxLen <= 0 {
		maxLen = DefaultMaxKeyLen
	}
	key := between(left, right)
	if len(key) > maxLen {
		return "", fmt.Errorf("%w: need %d bytes, max is %d",
			ErrNeedsRebalance, len(key), maxLen)
	}
	return key, nil
}

// between computes the key without validation or length checks.
// Its length is at most max(len(left), len(right)) + 1.
func between(left, right string) string {
	if left == "" && right == "" {
		return string(midChar)
	}
	i := 0
	for i < len(left) && i < len(right) && left[i] == right[i] {
		i++
	}
	lo := 0
	if i < len(left) {
		lo = charIndex(left[i])
	}
	hi := len(charset) - 1
	if i < len(right) {
		hi = charIndex(right[i])
	}
	if hi-lo > 1 {
		return left[:i] + string(charset[(lo+hi)/2])
	}
	return left[:i] + string(charset[lo]) + between(rest(left, i), rest(right, i))
}

// rest returns s[i+1:], tolerating i == len(s).
func rest(s string, i int) string {
	if i+1 >= len(s) {
		return ""
	}
	return s[i+1:]
}
