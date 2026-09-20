package pctenc

import "errors"

// ErrBadEscape reports an invalid percent-escape sequence such as a
// trailing '%' or non-hex digits after '%'.
var ErrBadEscape = errors.New("pctenc: invalid percent-escape")

// Decode reverses percent-encoding in s. A literal '+' is kept as '+'
// (form encoding is out of scope). On invalid escapes it returns
// ("", ErrBadEscape) rather than a partial result.
func Decode(s string, m Mode) (string, error) {
	escapes := 0
	for i := 0; i < len(s); i++ {
		if s[i] != '%' {
			continue
		}
		if i+2 >= len(s) || !isHex(s[i+1]) || !isHex(s[i+2]) {
			return "", ErrBadEscape
		}
		escapes++
		i += 2
	}
	if escapes == 0 {
		return s, nil
	}
	buf := make([]byte, 0, len(s)-2*escapes)
	for i := 0; i < len(s); i++ {
		if s[i] == '%' {
			buf = append(buf, hexVal(s[i+1])<<4|hexVal(s[i+2]))
			i += 2
		} else {
			buf = append(buf, s[i])
		}
	}
	return string(buf), nil
}

func isHex(b byte) bool {
	return '0' <= b && b <= '9' || 'a' <= b && b <= 'f' || 'A' <= b && b <= 'F'
}

func hexVal(b byte) byte {
	switch {
	case '0' <= b && b <= '9':
		return b - '0'
	case 'a' <= b && b <= 'f':
		return b - 'a' + 10
	default:
		return b - 'A' + 10
	}
}
