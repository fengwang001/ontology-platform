package esc

type HexError struct{}

func Simple(c byte) (rune, bool) {
	switch c {
	case '"', '/', '\\':
		return rune(c), true
	case 'b':
		return '\b', true
	case 'f':
		return '\f', true
	case 'n':
		return '\n', true
	case 'r':
		return '\r', true
	case 't':
		return '\t', true
	default:
		return 0, false
	}
}

func Hex(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	default:
		return 0, false
	}
}

func ReadHex(p []byte) (uint16, error) {
	if len(p) < 4 {
		return 0, HexError{}
	}
	var v uint16
	for _, c := range p[:4] {
		d, ok := Hex(c)
		if !ok {
			return 0, HexError{}
		}
		v = v<<4 | uint16(d)
	}
	return v, nil
}

func IsHigh(v uint16) bool {
	return v >= 0xD800 && v <= 0xDBFF
}

func IsLow(v uint16) bool {
	return v >= 0xDC00 && v <= 0xDFFF
}

func Pair(hi, lo uint16) rune {
	return rune(0x10000 + int(hi-0xD800)<<10 + int(lo-0xDC00))
}

func (HexError) Error() string {
	return "esc: invalid unicode hexadecimal escape"
}
