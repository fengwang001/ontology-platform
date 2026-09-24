package jstr

import (
	"errors"
	"fmt"
	"unicode/utf8"
)

var (
	ErrInvalidUTF8 = errors.New("invalid UTF-8 in string literal")
	ErrControl     = errors.New("unescaped control character")
	ErrEscape      = errors.New("unknown escape sequence")
	ErrUnicode     = errors.New("invalid unicode escape")
	ErrSurrogate   = errors.New("invalid UTF-16 surrogate pair")
	ErrMissing     = errors.New("missing closing quote")
	ErrTrailing    = errors.New("bytes after closing quote")
)

type OffsetError struct {
	Op     error
	Offset int
}
type EncodeError struct{ Offset int }

func (e *OffsetError) Error() string { return fmt.Sprintf("%s at byte %d", e.Op, e.Offset) }
func (e *OffsetError) Unwrap() error { return e.Op }
func (e *EncodeError) Error() string { return fmt.Sprintf("%s at byte %d", ErrInvalidUTF8, e.Offset) }
func (e *EncodeError) Unwrap() error { return ErrInvalidUTF8 }

type Decoder struct {
	out                 []byte
	state               uint8
	pos, start, checked int
	hexN                int
	hexV                rune
	high, highAt        rune
	highSet             bool
	rawN                int
	raw                 [4]byte
	err                 error
}

// Decode decodes one complete JSON string literal, including both quote bytes.
func Decode(lit []byte) (string, error) {
	var d Decoder
	if _, err := d.Write(lit); err != nil {
		return "", err
	}
	if err := d.Close(); err != nil {
		return "", err
	}
	return d.Result(), nil
}

func (d *Decoder) Result() string { return string(d.out) }
func (d *Decoder) Checks() int    { return d.checked }

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
