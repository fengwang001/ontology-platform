// Package rle implements an escaped run-length codec: Encode emits the
// unique canonical form and Decode accepts only that form, so every
// accepted t satisfies Encode(Decode(t)) == t. See DESIGN.md.
package rle

import (
	"errors"
	"fmt"
	"unicode/utf8"

	"ontology/runs"
)

var (
	ErrCountOne, ErrCountZero       = errors.New("explicit count 1"), errors.New("zero count")
	ErrLeadingZero, ErrAdjacentSame = errors.New("leading zero in count"), errors.New("adjacent runs share a symbol")
	ErrBadEscape                    = errors.New("backslash escapes only digits and backslash")
	ErrLoneBackslash                = errors.New("trailing backslash")
	ErrMissingSymbol                = errors.New("count without symbol at end")
	ErrInvalidUTF8                  = errors.New("invalid UTF-8")
	ErrCountOverflow                = errors.New("count overflows uint64")
	ErrOutputTooLarge               = errors.New("decoded output exceeds limit")
)

// Error is a decode failure at byte Offset of the concatenated input.
type Error struct {
	Offset int
	Err    error
}

func (e *Error) Error() string { return fmt.Sprintf("rle: %v (byte %d)", e.Err, e.Offset) }
func (e *Error) Unwrap() error { return e.Err }

// DefaultMaxOutput caps decoded bytes when NewDecoder gets 0.
const DefaultMaxOutput = 1 << 30

func isDigit(b byte) bool { return '0' <= b && b <= '9' }

// Encode returns the canonical RLE form of s; code points are compared
// directly, so é and e+U+0301 never merge.
func Encode(s string) string {
	var b []byte
	for _, r := range runs.Split(s) {
		if r.Count > 1 {
			b = runs.AppendCount(b, r.Count)
		}
		if r.Sym < 0x80 && (isDigit(byte(r.Sym)) || r.Sym == '\\') {
			b = append(b, '\\', byte(r.Sym))
		} else {
			b = utf8.AppendRune(b, r.Sym)
		}
	}
	return string(b)
}

// Decode strictly decodes t; any non-canonical input yields an *Error.
func Decode(t string) (string, error) {
	d := NewDecoder(DefaultMaxOutput)
	if _, err := d.Write([]byte(t)); err != nil {
		return "", err
	}
	return d.Close()
}

// Decoder is a streaming strict decoder over arbitrarily split writes;
// each input byte is examined exactly once (field inspected).
type Decoder struct {
	count, maxOut                    uint64
	out                              []byte
	err                              error
	inspected, pos, runStart, escAt  int
	symLen, need                     int
	symBuf                           [4]byte
	last                             rune
	hasCount, zeroHead, esc, hasLast bool
}

// NewDecoder makes a Decoder; maxOut 0 means DefaultMaxOutput.
func NewDecoder(maxOut uint64) *Decoder {
	if maxOut == 0 {
		maxOut = DefaultMaxOutput
	}
	return &Decoder{maxOut: maxOut}
}

// Inspected reports how many input bytes have been examined so far.
func (d *Decoder) Inspected() int { return d.inspected }

func (d *Decoder) Write(p []byte) (int, error) {
	if d.err != nil {
		return 0, d.err
	}
	for i, b := range p {
		d.inspected++
		if err := d.step(b); err != nil {
			d.err = err
			return i, err
		}
		d.pos++
	}
	return len(p), nil
}

func (d *Decoder) Close() (string, error) {
	switch {
	case d.err != nil:
	case d.esc:
		d.err = &Error{d.escAt, ErrLoneBackslash}
	case d.need > 0:
		d.err = &Error{d.pos, ErrInvalidUTF8}
	case d.hasCount:
		d.err = &Error{d.runStart, ErrMissingSymbol}
	default:
		return string(d.out), nil
	}
	return "", d.err
}

func (d *Decoder) step(b byte) error {
	off := d.pos
	switch {
	case d.need > 0:
		if b&0xC0 != 0x80 {
			return &Error{off, ErrInvalidUTF8}
		}
		d.symBuf[d.symLen], d.symLen, d.need = b, d.symLen+1, d.need-1
		if d.need > 0 {
			return nil
		}
		if !utf8.Valid(d.symBuf[:d.symLen]) {
			return &Error{off, ErrInvalidUTF8}
		}
		r, _ := utf8.DecodeRune(d.symBuf[:d.symLen])
		return d.emit(r)
	case d.esc:
		d.esc = false
		if !isDigit(b) && b != '\\' {
			return &Error{off, ErrBadEscape}
		}
		d.symBuf[0], d.symLen = b, 1
		return d.emit(rune(b))
	case b == '\\':
		d.esc, d.escAt = true, off
		if !d.hasCount {
			d.runStart = off
		}
	case isDigit(b):
		if !d.hasCount {
			d.hasCount, d.zeroHead, d.count, d.runStart = true, b == '0', uint64(b-'0'), off
			return nil
		}
		if d.zeroHead {
			return &Error{d.runStart, ErrLeadingZero}
		}
		n, ok := runs.AddDigit(d.count, uint64(b-'0'))
		if !ok {
			return &Error{off, ErrCountOverflow}
		}
		d.count = n
	case b < 0x80:
		if !d.hasCount {
			d.runStart = off
		}
		d.symBuf[0], d.symLen = b, 1
		return d.emit(rune(b))
	case b >= 0xC0:
		if !d.hasCount {
			d.runStart = off
		}
		d.symBuf[0], d.symLen = b, 1
		d.need = 1
		if b >= 0xE0 {
			d.need = 2 + int(b>>4&1)
		}
	default:
		return &Error{off, ErrInvalidUTF8}
	}
	return nil
}

func (d *Decoder) emit(r rune) error {
	if d.hasCount && d.count < 2 {
		if d.count == 0 {
			return &Error{d.runStart, ErrCountZero}
		}
		return &Error{d.runStart, ErrCountOne}
	}
	if !d.hasCount {
		d.count = 1
	}
	if d.hasLast && r == d.last {
		return &Error{d.runStart, ErrAdjacentSame}
	}
	if n := uint64(d.symLen); d.count > (d.maxOut-uint64(len(d.out)))/n {
		return &Error{d.runStart, ErrOutputTooLarge}
	}
	for ; d.count > 0; d.count-- {
		d.out = append(d.out, d.symBuf[:d.symLen]...)
	}
	d.last, d.hasLast = r, true
	d.hasCount, d.zeroHead = false, false
	return nil
}
