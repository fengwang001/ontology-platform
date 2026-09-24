package esc

import "errors"

var (
	ErrUnknown              = errors.New("esc: unknown escape sequence")
	ErrIncompleteHex        = errors.New("esc: incomplete unicode escape")
	ErrLoneSurrogate        = errors.New("esc: lone utf-16 surrogate")
	ErrInvalidSurrogatePair = errors.New("esc: invalid utf-16 surrogate pair")
)

type CodeUnit rune

func DecodeUnit(lit []byte, at int) (CodeUnit, int, error) {
	if at+1 >= len(lit) || lit[at] != '\\' {
		return 0, at, ErrUnknown
	}
	switch lit[at+1] {
	case '"', '\\', '/':
		return CodeUnit(lit[at+1]), at + 2, nil
	case 'b':
		return '\b', at + 2, nil
	case 'f':
		return '\f', at + 2, nil
	case 'n':
		return '\n', at + 2, nil
	case 'r':
		return '\r', at + 2, nil
	case 't':
		return '\t', at + 2, nil
	case 'u':
		if at+6 > len(lit) {
			return 0, at, ErrIncompleteHex
		}
		value, ok := parseHex4(lit[at+2 : at+6])
		if !ok {
			return 0, at, ErrIncompleteHex
		}
		return CodeUnit(value), at + 6, nil
	default:
		return 0, at, ErrUnknown
	}
}

func AppendRune(dst []byte, first, second CodeUnit) ([]byte, error) {
	value := rune(first)
	switch {
	case first >= 0xD800 && first <= 0xDBFF:
		if second < 0xDC00 || second > 0xDFFF {
			return dst, ErrLoneSurrogate
		}
		value = 0x10000 + (rune(first)-0xD800)<<10 + (rune(second) - 0xDC00)
	case first >= 0xDC00 && first <= 0xDFFF:
		return dst, ErrLoneSurrogate
	default:
		return appendUTF8(dst, value), nil
	}
	return appendUTF8(dst, value), nil
}

func parseHex4(src []byte) (rune, bool) {
	var value rune
	for _, b := range src {
		var digit rune
		switch {
		case b >= '0' && b <= '9':
			digit = rune(b - '0')
		case b >= 'a' && b <= 'f':
			digit = rune(b - 'a') + 10
		case b >= 'A' && b <= 'F':
			digit = rune(b - 'A') + 10
	default:
			return 0, false
		}
		value = value<<4 + digit
	}
	return value, true
}

func appendUTF8(dst []byte, value rune) []byte {
	switch {
	case value < 0x80:
		return append(dst, byte(value))
	case value < 0x800:
		return append(dst, 0xC0|byte(value>>6), 0x80|byte(value&0x3F))
	case value < 0x10000:
		return append(dst, 0xE0|byte(value>>12), 0x80|byte((value>>6)&0x3F), 0x80|byte(value&0x3F))
	default:
		return append(dst, 0xF0|byte(value>>18), 0x80|byte((value>>12)&0x3F), 0x80|byte((value>>6)&0x3F), 0x80|byte(value&0x3F))
	}
}
