// Package nkey implements three-valued-logic comparison of nullable keys.
package nkey

// Matches reports whether two nullable keys match under SQL three-valued
// logic: if either key is NULL (nil) the result is always false — a NULL
// matches neither a non-NULL key nor another NULL. The empty string "" is an
// ordinary non-NULL key and only matches another "".
func Matches(a, b *string) bool {
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}
