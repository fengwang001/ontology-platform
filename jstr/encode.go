package jstr

import "unicode/utf8"

// Encode writes minimal JSON escaping. Invalid UTF-8 causes *EncodeError.
func Encode(s string) ([]byte, error) {
	out := []byte{'"'}
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && n == 1 {
			return nil, &EncodeError{i}
		}
		switch r {
		case '"', '\\':
			out = append(out, '\\', byte(r))
		case '\b':
			out = append(out, '\\', 'b')
		case '\f':
			out = append(out, '\\', 'f')
		case '\n':
			out = append(out, '\\', 'n')
		case '\r':
			out = append(out, '\\', 'r')
		case '\t':
			out = append(out, '\\', 't')
		default:
			if r < 0x20 {
				const h = "0123456789abcdef"
				out = append(out, '\\', 'u', '0', '0', h[byte(r)>>4], h[byte(r)&15])
			} else {
				var b [4]byte
				m := utf8.EncodeRune(b[:], r)
				out = append(out, b[:m]...)
			}
		}
		i += n
	}
	return append(out, '"'), nil
}
