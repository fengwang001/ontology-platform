package jstr

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"unicode/utf8"

	"ontology/esc"
)

var (
	ErrControl       = errors.New("jstr: unescaped control character")
	ErrUnknownEscape = errors.New("jstr: unknown escape")
	ErrShortUnicode  = errors.New("jstr: incomplete unicode escape")
	ErrMissingQuote  = errors.New("jstr: missing closing quote")
	ErrTrailingBytes = errors.New("jstr: bytes after closing quote")
	ErrSurrogate     = esc.ErrLoneSurrogate
	ErrInvalidUTF8   = errors.New("jstr: invalid utf-8")
)

type SyntaxError struct {
	Offset int
	Kind   error
}

func (e SyntaxError) Error() string { return e.Kind.Error() }
func (e SyntaxError) Unwrap() error { return e.Kind }

type Decoder struct {
	out     io.Writer
	state   byte
	need    byte
	cont    byte
	start   int
	hex     [4]byte
	pos     int
	high    rune
	pending [4]byte
	checked int
	err     error
}

func Decode(lit []byte) (string, error) {
	var out bytes.Buffer
	d := NewDecoder(&out)
	if _, err := d.Write(lit); err != nil {
		return "", err
	}
	return out.String(), d.Close()
}

func Encode(s string) ([]byte, error) {
	if !utf8.ValidString(s) {
		return nil, ErrInvalidUTF8
	}
	out := make([]byte, 0, len(s)+2)
	out = append(out, '"')
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == '"':
			out = append(out, '\\', '"')
		case r == '\\':
			out = append(out, '\\', '\\')
		case r == '\b', r == '\f', r == '\n', r == '\r', r == '\t':
			out = append(out, '\\', "bfnrt"[strings.IndexByte("\b\f\n\r\t", byte(r))])
		case r < 0x20:
			out = append(out, '\\', 'u', '0', '0', hexd(r>>4), hexd(r))
		default:
			out = append(out, s[i:i+n]...)
		}
		i += n
	}
	return append(out, '"'), nil
}

func NewDecoder(out io.Writer) *Decoder { return &Decoder{out: out} }

func (d *Decoder) Write(p []byte) (int, error) {
	for i := range p {
		d.checked++
		if err := d.byte(p[i]); err != nil {
			d.err = err
			d.state = 7
			return i, err
		}
	}
	return len(p), nil
}

func (d *Decoder) Close() error {
	if d.err != nil {
		return d.err
	}
	if d.state != 6 {
		return SyntaxError{Offset: d.checked, Kind: ErrMissingQuote}
	}
	return nil
}

func (d *Decoder) CheckedCount() int { return d.checked }

func (d *Decoder) byte(c byte) error {
	off := d.checked - 1
	switch d.state {
	case 0:
		if c != '"' {
			return SyntaxError{Offset: off, Kind: ErrMissingQuote}
		}
		d.state = 1
	case 1:
		return d.content(c, off)
	case 2:
		return d.escaped(c, off)
	case 3, 4:
		return d.unicode(c, off)
	case 5:
		return d.utf8Cont(c, off)
	default:
		return SyntaxError{Offset: off, Kind: ErrTrailingBytes}
	}
	return nil
}

func (d *Decoder) content(c byte, off int) error {
	switch {
	case c == '"':
		d.state = 6
	case c == '\\':
		d.state = 2
	case c < 0x20:
		return SyntaxError{Offset: off, Kind: ErrControl}
	case c < 0x80:
		_, err := d.out.Write([]byte{c})
		return err
	default:
		d.start = off
		d.pending[0] = c
		d.need, d.cont = utf8Info(c)
		if d.need == 0 {
			return SyntaxError{Offset: off, Kind: ErrInvalidUTF8}
		}
		d.state = 5
	}
	return nil
}

func (d *Decoder) escaped(c byte, off int) error {
	if r, ok := esc.SimpleValue(c); ok {
		d.state = 1
		return d.emit(r)
	}
	if c != 'u' {
		return SyntaxError{Offset: off - 1, Kind: ErrUnknownEscape}
	}
	d.pos, d.state = 0, 3
	return nil
}

func (d *Decoder) unicode(c byte, off int) error {
	v, ok := esc.HexDigit(c)
	if !ok {
		kind := ErrShortUnicode
		offset := off - d.pos - 1
		if d.state == 4 {
			kind, offset = ErrSurrogate, d.start
		}
		return SyntaxError{Offset: offset, Kind: kind}
	}
	d.hex[d.pos] = c
	d.pos++
	if d.pos < 4 {
		return nil
	}
	v, _ = esc.DecodeUnicode(d.hex[:])
	d.state = 1
	switch {
	case d.high != 0:
		high := d.high
		d.high = 0
		r, ok := esc.Pair(high, v)
		if !ok {
			return SyntaxError{Offset: d.start, Kind: ErrSurrogate}
		}
		return d.emit(r)
	case esc.IsLow(v):
		return SyntaxError{Offset: off - 5, Kind: ErrSurrogate}
	case esc.IsHigh(v):
		d.high, d.start, d.state = v, off-5, 4
	}
	return d.emit(v)
}

func (d *Decoder) utf8Cont(c byte, off int) error {
	if c&0xC0 != 0x80 {
		return SyntaxError{Offset: d.start, Kind: ErrInvalidUTF8}
	}
	d.pending[d.cont] = c
	d.cont++
	d.need--
	if d.need > 0 {
		return nil
	}
	seq := d.pending[:d.cont]
	r, _ := utf8.DecodeRune(seq)
	if r == utf8.RuneError || int(d.cont) != utf8.RuneLen(r) {
		return SyntaxError{Offset: d.start, Kind: ErrInvalidUTF8}
	}
	d.state = 1
	_, err := d.out.Write(seq)
	return err
}

func (d *Decoder) emit(r rune) error {
	if d.high != 0 {
		return nil
	}
	var b [4]byte
	n := utf8.EncodeRune(b[:], r)
	_, err := d.out.Write(b[:n])
	return err
}

func utf8Info(first byte) (byte, byte) {
	switch {
	case first&0xE0 == 0xC0:
		return 1, 1
	case first&0xF0 == 0xE0:
		return 2, 1
	case first&0xF8 == 0xF0:
		return 3, 1
	default:
		return 0, 0
	}
}

func hexd(r rune) byte {
	return "0123456789abcdef"[byte(r)]
}
