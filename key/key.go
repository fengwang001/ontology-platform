// Package key defines nullable join keys and their match rule.
package key

// Match reports whether two nullable keys match: both non-nil and equal.
// NULL never matches anything, including NULL itself.
func Match(a, b *string) bool {
	return a != nil && b != nil && *a == *b
}

// Equal is the canonical comparison for nullable keys: nil equals nil,
// two non-nil keys are equal iff their strings are equal.
func Equal(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// Norm canonicalizes a nullable key for indexing: ok is false for NULL,
// which is never indexed and never produces a match.
func Norm(k *string) (v string, ok bool) {
	if k == nil {
		return "", false
	}
	return *k, true
}
