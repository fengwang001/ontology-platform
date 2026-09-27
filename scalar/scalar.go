// Package scalar validates individual Unicode scalar values.
package scalar

// Valid reports whether r is a Unicode scalar value:
// 0..U+D7FF or U+E000..U+10FFFF.
func Valid(r rune) bool {
	return uint32(r) <= 0xD7FF || (uint32(r) >= 0xE000 && uint32(r) <= 0x10FFFF)
}

// Surrogate reports whether r lies in the surrogate range U+D800..U+DFFF.
func Surrogate(r rune) bool {
	return uint32(r)-0xD800 < 0x800
}
