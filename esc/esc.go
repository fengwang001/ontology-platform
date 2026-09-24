package esc

import "errors"

var (
	ErrUnknownEscape = errors.New("esc: unknown escape sequence")
	ErrShortUnicode  = errors.New("esc: incomplete unicode escape")
	ErrLoneSurrogate = errors.New("esc: unpaired utf-16 surrogate")
)

const surrogateOffset = 0x10000

var simple = map[byte]rune{
	'"':  '"',
	'\\': '\\',
	'/':  '/',
	'b':  '\b',
	'f':  '\f',
	'n':  '\n',
	'r':  '\r',
	't':  '\t',
}

type EscapeError struct {
	Offset int
	Kind   error
}

func (e EscapeError) Error() string { return e.Kind.Error() }
func (e EscapeError) Unwrap() error { return e.Kind }

func IsSimple(c byte) bool {
	_, ok := simple[c]
	return ok
}

func SimpleValue(c byte) (rune, bool) {
	r, ok := simple[c]
	return r, ok
}

func HexDigit(c byte) (rune, bool) {
	switch {
	case '0' <= c && c <= '9':
		return rune(c - '0'), true
	case 'a' <= c && c <= 'f':
		return rune(c-'a') + 10, true
	case 'A' <= c && c <= 'F':
		return rune(c-'A') + 10, true
	default:
		return 0, false
	}
}

func DecodeUnicode(hex []byte) (rune, bool) {
	if len(hex) != 4 {
		return 0, false
	}
	var value rune
	for _, c := range hex {
		digit, ok := HexDigit(c)
		if !ok {
			return 0, false
		}
		value = value<<4 | digit
	}
	return value, true
}

func IsSurrogate(value rune) bool {
	return 0xD800 <= value && value <= 0xDFFF
}

func IsHigh(value rune) bool {
	return 0xD800 <= value && value <= 0xDBFF
}

func IsLow(value rune) bool {
	return 0xDC00 <= value && value <= 0xDFFF
}

func Pair(high, low rune) (rune, bool) {
	if !IsHigh(high) || !IsLow(low) {
		return 0, false
	}
	return surrogateOffset + (high-0xD800)<<10 + (low - 0xDC00), true
}
