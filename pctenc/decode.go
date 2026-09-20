package pctenc

// Decode reverses percent-escapes in s. Both uppercase and lowercase
// hex digits are accepted. A literal '+' is always preserved (this is
// not form encoding), and raw bytes other than '%' pass through
// untouched regardless of the mode.
//
// Every escape is validated before any output is produced, so a
// malformed escape anywhere in s yields ("", ErrBadEscape).
func Decode(s string, m Mode) (string, error) {
	for i := 0; i < len(s); i++ {
		if s[i] == '%' {
			return decodeEscaping(s, i)
		}
	}
	return s, nil
}

func decodeEscaping(s string, first int) (string, error) {
	for i := first; i < len(s); i++ {
		if s[i] != '%' {
			continue
		}
		if i+2 >= len(s) {
			return "", ErrBadEscape
		}
		_, okHi := hexValue(s[i+1])
		_, okLo := hexValue(s[i+2])
		if !okHi || !okLo {
			return "", ErrBadEscape
		}
		i += 2
	}

	out := make([]byte, 0, len(s))
	out = append(out, s[:first]...)
	for i := first; i < len(s); {
		if s[i] != '%' {
			out = append(out, s[i])
			i++
			continue
		}
		hi, _ := hexValue(s[i+1])
		lo, _ := hexValue(s[i+2])
		out = append(out, hi<<4|lo)
		i += 3
	}
	return string(out), nil
}

func hexValue(b byte) (byte, bool) {
	switch {
	case b >= '0' && b <= '9':
		return b - '0', true
	case b >= 'A' && b <= 'F':
		return b - 'A' + 10, true
	case b >= 'a' && b <= 'f':
		return b - 'a' + 10, true
	default:
		return 0, false
	}
}
