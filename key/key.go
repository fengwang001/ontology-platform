// Package key defines the nullable-key matching rule for the SEMI JOIN.
// It depends on no other package in this module.
package key

// Match reports whether two nullable keys match.
// A match requires both keys non-nil and pointing at equal strings;
// NULL (nil) never matches anything, including another NULL.
func Match(a, b *string) bool {
	return a != nil && b != nil && *a == *b
}

// Value returns the normalized (string, present) form of a nullable key:
// present is false for NULL. Callers use it instead of comparing *string
// directly, so NULL is never confused with the empty string "".
func Value(k *string) (v string, present bool) {
	if k == nil {
		return "", false
	}
	return *k, true
}
