package pctenc

import "strings"

// unhex converts a hex digit (either case) to its value,
// or returns -1 for non-hex bytes.
func unhex(c byte) int {
	switch {
	case '0' <= c && c <= '9':
		return int(c - '0')
	case 'a' <= c && c <= 'f':
		return int(c-'a') + 10
	case 'A' <= c && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}

// Decode reverses percent-encoding in s.
//
// Both uppercase and lowercase hex digits are accepted. A '+' is
// always kept as a literal plus sign; form-encoding rules do not
// apply here. Any malformed escape ("%", "%A", "%GG", "%2G", ...)
// yields ("", ErrBadEscape) rather than a partial result. The mode
// is accepted for symmetry with Encode; decoding only validates
// escape syntax and performs no content whitelist checks.
func Decode(s string, m Mode) (string, error) {
	_ = m
	if strings.IndexByte(s, '%') < 0 {
		return s, nil
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '%' {
			b.WriteByte(c)
			continue
		}
		if i+2 >= len(s) {
			return "", ErrBadEscape
		}
		hi := unhex(s[i+1])
		lo := unhex(s[i+2])
		if hi < 0 || lo < 0 {
			return "", ErrBadEscape
		}
		b.WriteByte(byte(hi<<4 | lo))
		i += 2
	}
	return b.String(), nil
}
