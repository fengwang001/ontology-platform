package host

const (
	rewriteCase = 1 << iota
	rewriteTrailingDot
)

// normalizeRegName lower-cases ASCII letters and upper-cases the hex digits of
// percent escapes in a registered name, and removes exactly one terminal root
// dot. Multiple trailing dots are kept (they are part of an unusual name and
// are not a root indicator).
func normalizeRegName(name string) (string, int) {
	var flags int
	b := []byte(name)
	for i := 0; i < len(b); i++ {
		c := b[i]
		if c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
			flags |= rewriteCase
		}
		if c == '%' && i+2 < len(b) {
			if _, ok1 := hexVal(b[i+1]); ok1 {
				if _, ok2 := hexVal(b[i+2]); ok2 {
					if u := toUpperHex(b[i+1]); u != b[i+1] {
						b[i+1] = u
						flags |= rewriteCase
					}
					if u := toUpperHex(b[i+2]); u != b[i+2] {
						b[i+2] = u
						flags |= rewriteCase
					}
					i += 2
				}
			}
		}
	}
	if len(b) > 0 && b[len(b)-1] == '.' {
		b = b[:len(b)-1]
		flags |= rewriteTrailingDot
	}
	return string(b), flags
}

func hexVal(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

func toUpperHex(c byte) byte {
	if c >= 'a' && c <= 'f' {
		return c - ('a' - 'A')
	}
	return c
}
