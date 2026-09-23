// Package chunk splits a string into alternating digit and non-digit
// chunks. Only ASCII bytes '0'-'9' count as digits; every other byte,
// including the bytes of multi-byte UTF-8 characters, is non-digit.
package chunk

// IsDigit reports whether b is an ASCII digit.
func IsDigit(b byte) bool { return '0' <= b && b <= '9' }

// Next splits s into its first chunk and the remainder, reporting
// whether the chunk consists of ASCII digits. s must be non-empty.
// The returned strings share storage with s; nothing is allocated.
func Next(s string) (head, tail string, digit bool) {
	digit = IsDigit(s[0])
	i := 1
	for i < len(s) && IsDigit(s[i]) == digit {
		i++
	}
	return s[:i], s[i:], digit
}
