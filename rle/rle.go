// Package rle implements strict escaped run-length encoding (see DESIGN.md).
package rle

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"unicode/utf8"

	"ontology/runs"
)

var (
	ErrCountOne       = errors.New("explicit count of 1")
	ErrCountZero      = errors.New("count of 0")
	ErrLeadingZero    = errors.New("count with leading zero")
	ErrAdjacentSame   = errors.New("adjacent runs share a symbol")
	ErrBadEscape      = errors.New(`\ must be followed by a digit or \`)
	ErrTrailingEscape = errors.New(`trailing \`)
	ErrMissingSymbol  = errors.New("count with no symbol")
	ErrInvalidUTF8    = errors.New("invalid UTF-8")
	ErrCountTooLarge  = errors.New("count overflows uint64")
	ErrOutputTooLarge = errors.New("output byte limit exceeded")
)

// Error reports a decode failure; Kind is a sentinel above, Off the input
// offset at which the error was detected (end of input if truncated).
type Error struct {
	Kind error
	Off  int
}

func (e *Error) Error() string { return fmt.Sprintf("rle: %v at byte %d", e.Kind, e.Off) }
func (e *Error) Unwrap() error { return e.Kind }

// DefaultMaxOutput caps the output of Decode; use a Decoder for other limits.
const DefaultMaxOutput = 1 << 30

var checked atomic.Int64 // total input bytes examined by all decoders

// CheckedBytes reports how many input bytes all decoders have examined.
func CheckedBytes() int64 { return checked.Load() }

// Encode returns the canonical escaped RLE of s (maximal runs).
func Encode(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range runs.Split(s) {
		if r.N > 1 {
			b.Write(runs.AppendCount(nil, r.N))
		}
		if r.R == '\\' || '0' <= r.R && r.R <= '9' {
			b.WriteByte('\\')
		}
		b.WriteRune(r.R)
	}
	return b.String()
}

// Decoder strictly decodes input fed to Write, examining each byte exactly
// once, so any split of the input yields identical output and errors.
type Decoder struct {
	w              io.Writer
	maxOut, out    int64
	last, rn       rune
	off, ndig, rem int
	count          uint64
	lo, hi         byte
	esc            bool
	err            error
}

// NewDecoder returns a Decoder writing to w; negative maxOut means unlimited.
func NewDecoder(w io.Writer, maxOut int64) *Decoder {
	return &Decoder{w: w, maxOut: maxOut, last: -1, lo: 0x80, hi: 0xBF}
}

// Write consumes the next chunk of encoded input.
func (d *Decoder) Write(p []byte) (int, error) {
	if d.err != nil {
		return 0, d.err
	}
	for i, b := range p {
		checked.Add(1)
		if err := d.step(b); err != nil {
			d.err = err
			return i, err
		}
		d.off++
	}
	return len(p), nil
}

func (d *Decoder) step(b byte) error {
	if d.rem > 0 { // UTF-8 continuation byte
		if b < d.lo || b > d.hi {
			return &Error{ErrInvalidUTF8, d.off}
		}
		d.rn, d.lo, d.hi = d.rn<<6|rune(b&0x3F), 0x80, 0xBF
		if d.rem--; d.rem == 0 {
			return d.emit(d.rn)
		}
		return nil
	}
	if d.esc {
		d.esc = false
		if b != '\\' && (b < '0' || b > '9') {
			return &Error{ErrBadEscape, d.off}
		}
		return d.emit(rune(b))
	}
	switch {
	case '0' <= b && b <= '9':
		if d.ndig > 0 && d.count == 0 {
			return &Error{ErrLeadingZero, d.off}
		}
		n, ok := runs.AddDigit(d.count, b-'0')
		if !ok {
			return &Error{ErrCountTooLarge, d.off}
		}
		d.count, d.ndig = n, d.ndig+1
	case b == '\\':
		d.esc = true
	case b < 0x80:
		return d.emit(rune(b))
	default: // UTF-8 lead byte
		switch {
		case b < 0xC2, b > 0xF4:
			return &Error{ErrInvalidUTF8, d.off}
		case b < 0xE0:
			d.rn, d.rem = rune(b&0x1F), 1
		case b < 0xF0:
			d.rn, d.rem = rune(b&0x0F), 2
		default:
			d.rn, d.rem = rune(b&0x07), 3
		}
		// Restrict the first continuation byte to reject overlong forms
		// and surrogates (mirrors unicode/utf8's accept ranges).
		if b == 0xE0 || b == 0xF0 {
			d.lo = 0xA0 - (b - 0xE0)
		}
		if b == 0xED || b == 0xF4 {
			d.hi = 0x9F - (b-0xED)/7*0x10
		}
	}
	return nil
}

func (d *Decoder) emit(r rune) error { // complete one run of symbol r
	n := uint64(1)
	if d.ndig > 0 {
		if d.count < 2 { // counts 0 and 1 are non-canonical
			return &Error{[]error{ErrCountZero, ErrCountOne}[d.count], d.off}
		}
		n = d.count
	}
	if d.last == r {
		return &Error{ErrAdjacentSame, d.off}
	}
	sym := utf8.AppendRune(nil, r)
	if d.maxOut >= 0 && n > uint64(d.maxOut-d.out)/uint64(len(sym)) {
		return &Error{ErrOutputTooLarge, d.off}
	}
	rep := min(n, uint64(4096/len(sym)))
	buf := bytes.Repeat(sym, int(rep))
	for n > 0 {
		m := min(n, rep)
		if _, err := d.w.Write(buf[:int(m)*len(sym)]); err != nil {
			return err
		}
		d.out += int64(m) * int64(len(sym))
		n -= m
	}
	d.last, d.count, d.ndig = r, 0, 0
	return nil
}

// Close reports any error left pending at end of input.
func (d *Decoder) Close() error {
	if d.err != nil {
		return d.err
	}
	switch {
	case d.rem > 0:
		d.err = &Error{ErrInvalidUTF8, d.off}
	case d.esc:
		d.err = &Error{ErrTrailingEscape, d.off}
	case d.ndig > 0:
		d.err = &Error{ErrMissingSymbol, d.off}
	}
	return d.err
}

// Decode strictly decodes t, limiting output to DefaultMaxOutput bytes.
func Decode(t string) (string, error) {
	var b strings.Builder
	d := NewDecoder(&b, DefaultMaxOutput)
	if _, err := d.Write([]byte(t)); err != nil {
		return "", err
	}
	if err := d.Close(); err != nil {
		return "", err
	}
	return b.String(), nil
}
