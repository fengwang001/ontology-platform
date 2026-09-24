// Package rle implements an escaped run-length encoding for text; see DESIGN.md.
package rle

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math/big"
	"unicode/utf8"

	"ontology/runs"
)

var (
	ErrExplicitOne       = errors.New("rle: explicit count 1")
	ErrZeroCount         = errors.New("rle: zero count")
	ErrLeadingZero       = errors.New("rle: leading zero in count")
	ErrAdjacentSame      = errors.New("rle: adjacent runs share a symbol")
	ErrBadEscape         = errors.New("rle: escape of byte that needs none")
	ErrTrailingBackslash = errors.New("rle: trailing backslash")
	ErrMissingSymbol     = errors.New("rle: count without symbol")
	ErrInvalidUTF8       = errors.New("rle: invalid utf-8")
	ErrOutputTooLarge    = errors.New("rle: decoded output exceeds limit")
)

// DecodeError reports a strict-decoding failure at byte Offset of the input.
type DecodeError struct {
	Err    error
	Offset int
}

func (e *DecodeError) Error() string { return fmt.Sprintf("%v at byte %d", e.Err, e.Offset) }
func (e *DecodeError) Unwrap() error { return e.Err }

const DefaultMaxOutput = 1 << 30 // decoded-size cap used by Decode

// Encode returns the canonical encoding of s (maximal runs; count 1 omitted).
func Encode(s string) string {
	var b []byte
	for _, r := range runs.Split(s) {
		if r.Count > 1 {
			b = runs.AppendCount(b, r.Count)
		}
		b = appendSym(b, r.Sym)
	}
	return string(b)
}

func appendSym(b []byte, sym rune) []byte {
	if sym == '\\' || '0' <= sym && sym <= '9' {
		b = append(b, '\\')
	}
	return utf8.AppendRune(b, sym)
}

func Decode(t string) (string, error) { return DecodeLimit(t, DefaultMaxOutput) }

// DecodeLimit strictly decodes t, failing with ErrOutputTooLarge past max bytes.
func DecodeLimit(t string, max int64) (string, error) {
	var buf bytes.Buffer
	d := NewDecoder(&buf, max)
	if _, err := d.Write([]byte(t)); err != nil {
		return "", err
	}
	if err := d.Close(); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// Decoder streams strict decoding to w, capping output at max bytes.
type Decoder struct {
	w                     io.Writer
	max, out, checked     int64
	digits, rb            []byte
	digOff, escOff, rbOff int
	esc, pend             bool
	pendSym               rune
	pendN                 *big.Int
	err                   error
}

func NewDecoder(w io.Writer, max int64) *Decoder { return &Decoder{w: w, max: max} }

// BytesChecked reports how many input bytes have been examined so far.
func (d *Decoder) BytesChecked() int64 { return d.checked }

func (d *Decoder) fail(err error, off int) {
	if d.err == nil {
		d.err = &DecodeError{Err: err, Offset: off}
	}
}

// Write consumes encoded bytes; the first error is sticky.
func (d *Decoder) Write(p []byte) (int, error) {
	n := 0
	for n < len(p) && d.err == nil {
		d.checked++
		d.step(p[n])
		n++
	}
	return n, d.err
}

func (d *Decoder) step(c byte) {
	off := int(d.checked) - 1
	switch {
	case len(d.rb) > 0:
		d.rb = append(d.rb, c)
	case d.esc:
		d.esc = false
		if c != '\\' && (c < '0' || c > '9') {
			d.fail(ErrBadEscape, d.escOff)
			return
		}
		d.sym(rune(c), d.escOff)
		return
	case '0' <= c && c <= '9':
		if len(d.digits) == 0 {
			d.digOff = off
		}
		d.digits = append(d.digits, c)
		return
	case c == '\\':
		d.esc, d.escOff = true, off
		return
	default:
		d.rb, d.rbOff = append(d.rb, c), off
	}
	if !utf8.FullRune(d.rb) {
		return
	}
	r, size := utf8.DecodeRune(d.rb)
	off = d.rbOff
	d.rb = d.rb[:0]
	if r == utf8.RuneError && size == 1 {
		d.fail(ErrInvalidUTF8, off)
		return
	}
	d.sym(r, off)
}

func (d *Decoder) sym(r rune, off int) {
	n := big.NewInt(1)
	if len(d.digits) > 0 {
		off, s := d.digOff, string(d.digits)
		n = runs.ParseCount(s)
		d.digits = d.digits[:0]
		switch {
		case len(s) > 1 && s[0] == '0':
			d.fail(ErrLeadingZero, off)
		case s == "0":
			d.fail(ErrZeroCount, off)
		case s == "1":
			d.fail(ErrExplicitOne, off)
		}
		if d.err != nil {
			return
		}
	}
	if d.pend && d.pendSym == r {
		d.fail(ErrAdjacentSame, off)
		return
	}
	d.flush()
	if d.err == nil {
		d.pend, d.pendSym, d.pendN = true, r, n
	}
}

func (d *Decoder) flush() {
	if !d.pend {
		return
	}
	d.pend = false
	total := new(big.Int).Mul(d.pendN, big.NewInt(int64(utf8.RuneLen(d.pendSym))))
	if total.Cmp(big.NewInt(d.max-d.out)) > 0 {
		d.fail(ErrOutputTooLarge, int(d.checked))
		return
	}
	if err := runs.Expand(d.w, d.pendSym, d.pendN); err != nil {
		d.fail(err, int(d.checked))
	}
	d.out += total.Int64()
}

// Close validates the tail and flushes the last run.
func (d *Decoder) Close() error {
	if d.err == nil {
		switch {
		case d.esc:
			d.fail(ErrTrailingBackslash, d.escOff)
		case len(d.rb) > 0:
			d.fail(ErrInvalidUTF8, d.rbOff)
		case len(d.digits) > 0:
			d.fail(ErrMissingSymbol, d.digOff)
		default:
			d.flush()
		}
	}
	return d.err
}
