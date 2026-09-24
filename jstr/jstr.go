// Package jstr encodes and decodes one strict RFC 8259 JSON string literal.
package jstr

import (
	"errors"
	"unicode/utf8"
)

var (
	ErrNotString     = errors.New("jstr: input does not begin with a quote")
	ErrUnterminated  = errors.New("jstr: missing closing quote")
	ErrTrailingBytes = errors.New("jstr: bytes after closing quote")
	ErrControl       = errors.New("jstr: unescaped control character")
	ErrUnknownEscape = errors.New("jstr: unknown escape sequence")
	ErrBadHex        = errors.New("jstr: \\u requires 4 hexadecimal digits")
	ErrSurrogate     = errors.New("jstr: invalid UTF-16 surrogate pair")
	ErrInvalidUTF8   = errors.New("jstr: invalid UTF-8")
)

// DecodeError carries the error kind and its absolute byte offset.
type DecodeError struct {
	Kind   error
	Offset int
}

func (e *DecodeError) Error() string { return e.Kind.Error() }
func (e *DecodeError) Unwrap() error { return e.Kind }

// EncodeError marks non-UTF-8 source input; nothing is replaced by U+FFFD.
type EncodeError struct{ Offset int }

func (e *EncodeError) Error() string { return "jstr: source string is not valid UTF-8" }
func (e *EncodeError) Unwrap() error { return ErrInvalidUTF8 }

// Decoder incrementally decodes one literal. Each input byte reaches step
// once, so Examined never exceeds the physical input length.
type Decoder struct {
	state                     int
	need, got, hpos           int
	lead, hval, high          rune
	escAt, utfAt, highAt, off int
	out, pbuf                 []byte
	err                       error
	examined                  int
}

func NewDecoder() *Decoder       { return &Decoder{} }
func (d *Decoder) Examined() int { return d.examined }
func (d *Decoder) fail(k error, o int) error {
	if d.err == nil {
		d.err = &DecodeError{Kind: k, Offset: o}
	}
	return d.err
}

func (d *Decoder) Write(p []byte) (int, error) {
	i := 0
	for i < len(p) && d.err == nil && d.state != sEnd {
		d.examined++
		if err := d.step(p[i]); err != nil {
			return i, err
		}
		i++
	}
	if d.err == nil && d.state == sEnd && i < len(p) {
		d.examined += len(p) - i
		return i, d.fail(ErrTrailingBytes, d.off)
	}
	return i, d.err
}

func (d *Decoder) emit(r rune) {
	var t [4]byte
	d.out = append(d.out, t[:utf8.EncodeRune(t[:], r)]...)
}

func (d *Decoder) Close() error {
	if d.err != nil {
		return d.err
	}
	switch d.state {
	case sEnd:
		return nil
	case sStart:
		return d.fail(ErrNotString, d.off)
	case sCont:
		return d.fail(ErrInvalidUTF8, d.utfAt)
	case sEsc:
		return d.fail(ErrUnknownEscape, d.escAt)
	case sHex:
		if d.high != 0 {
			return d.fail(ErrSurrogate, d.highAt)
		}
		return d.fail(ErrBadHex, d.escAt)
	default:
		return d.fail(ErrUnterminated, d.off)
	}
}

func (d *Decoder) Result() (string, error) {
	if err := d.Close(); err != nil {
		return "", err
	}
	return string(d.out), nil
}

func Decode(lit []byte) (string, error) {
	d := NewDecoder()
	if _, err := d.Write(lit); err != nil {
		return "", err
	}
	return d.Result()
}

var shortEsc = map[rune]string{8: `\b`, 9: `\t`, 10: `\n`, 12: `\f`, 13: `\r`}

// Encode emits a minimal-escape literal: only '"', '\\' and U+0000–U+001F
// are escaped; '/', DEL and non-ASCII pass through verbatim. It returns
// *EncodeError (wrapping ErrInvalidUTF8) for invalid UTF-8 input.
func Encode(s string) ([]byte, error) {
	if !utf8.ValidString(s) {
		for i := range s {
			if r, n := utf8.DecodeRuneInString(s[i:]); r == utf8.RuneError && n == 1 {
				return nil, &EncodeError{Offset: i}
			}
		}
	}
	b := append(make([]byte, 0, len(s)+2), '"')
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == '"':
			b = append(b, '\\', '"')
		case r == '\\':
			b = append(b, '\\', '\\')
		case r < 0x20:
			if e := shortEsc[r]; e != "" {
				b = append(b, e...)
			} else {
				const h = "0123456789abcdef"
				b = append(b, '\\', 'u', '0', '0', h[r>>4], h[r&0xF])
			}
		default:
			b = append(b, s[i:i+n]...)
		}
		i += n
	}
	return append(b, '"'), nil
}
