package pretty

// runeWidth returns the display width of one code point: 2 for code points
// >= U+2E80 (East Asian wide ranges and beyond), 1 otherwise.
func runeWidth(r rune) int {
	if r >= 0x2E80 {
		return 2
	}
	return 1
}

// Width returns the display width of s measured in code points.
func Width(s string) int {
	w := 0
	for _, r := range s {
		w += runeWidth(r)
	}
	return w
}
