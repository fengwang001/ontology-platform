// Package jstr encodes and decodes a single strict RFC 8259 JSON string.
package jstr

import (
	"errors"
	"strings"
	"unicode/utf8"
)

const (
	KindControl = iota
	KindEscape
	KindUnicode
	KindUnterminated
	KindTrailing
	KindUTF8
)

type SyntaxError struct{ Kind, Offset int }

func (e *SyntaxError) Error() string { return "jstr: syntax error" }

var ErrInvalidUTF8 = errors.New("jstr: invalid UTF-8 in input")

var encS = [256]string{
	'"': `\"`, '\\': `\\`, '\b': `\b`, '\f': `\f`, '\n': `\n`, '\r': `\r`, '\t': `\t`,
}
var unesc = [256]byte{
	'"': '"', '\\': '\\', '/': '/', 'b': '\b', 'f': '\f', 'n': '\n', 'r': '\r', 't': '\t',
}

const hd = "0123456789abcdef"

func Encode(s string) ([]byte, error) {
	b := append(make([]byte, 0, len(s)+2), '"')
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && n == 1 {
			return nil, ErrInvalidUTF8
		}
		if r < 0x80 && encS[r] != "" {
			b = append(b, encS[r]...)
		} else if r < 0x20 {
			b = append(b, '\\', 'u', '0', '0', hd[r>>4], hd[r&15])
		} else {
			b = append(b, s[i:i+n]...)
		}
		i += n
	}
	return append(b, '"'), nil
}

func Decode(lit []byte) (string, error) {
	d := NewDecoder()
	_, err := d.Write(lit)
	if err != nil {
		return "", err
	}
	return d.Close()
}

// Decoder incrementally decodes one JSON string. st: 0 head, 1 raw,
// 2 backslash, 3 \u hex, 5 done; hi is a pending high surrogate (-1 none).
// Unescaped bytes are buffered in raw and validated at each boundary.
type Decoder struct {
	out                    strings.Builder
	buf                    []byte
	escOff, uOff, rawStart int
	pos, uN, checks        int
	uV, hi                 rune
	st                     uint8
	err                    error
}

func NewDecoder() *Decoder { return &Decoder{hi: -1} }

func (d *Decoder) Checks() int { return d.checks }

func (d *Decoder) fail(k, o int) error {
	d.err, d.st = &SyntaxError{k, o}, 5
	return d.err
}
