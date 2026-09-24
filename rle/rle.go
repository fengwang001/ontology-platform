// Package rle implements an escaped run-length encoding.
//
// An encoded text is a concatenation of runs. A run is an optional
// decimal count followed by one symbol (a code point). A symbol that
// is an ASCII digit or a backslash is written as '\' plus the
// character; any other code point is written as itself. A count of 1
// is omitted; counts >= 2 are decimal with no sign and no leading
// zeros. Encode always merges equal adjacent code points into one
// run, and Decode is strict: it accepts a text only if encoding the
// decoded result reproduces the text exactly.
package rle

import (
	"errors"
	"fmt"
	"unicode/utf8"

	"ontology/runs"
)

// Distinguishable decode failures; each is wrapped in an *Error that
// carries the byte offset of the problem.
var (
	ErrExplicitOne    = errors.New("rle: count 1 must be omitted")
	ErrZeroCount      = errors.New("rle: zero count")
	ErrLeadingZero    = errors.New("rle: leading zero in count")
	ErrAdjacentSame   = errors.New("rle: adjacent runs share a symbol")
	ErrBadEscape      = errors.New("rle: backslash not followed by digit or backslash")
	ErrTrailingEscape = errors.New("rle: trailing backslash")
	ErrMissingSymbol  = errors.New("rle: count without symbol")
	ErrInvalidUTF8    = errors.New("rle: invalid UTF-8")
	ErrTooLarge       = errors.New("rle: decoded output exceeds limit")
)

// DefaultLimit caps the decoded output size of a NewDecoder.
const DefaultLimit = 1 << 30

// Error annotates a decode failure with a byte offset.
type Error struct {
	Err error
	Off int
}

func (e *Error) Error() string { return fmt.Sprintf("%s at byte %d", e.Err, e.Off) }
func (e *Error) Unwrap() error { return e.Err }

func isDigit(b byte) bool { return '0' <= b && b <= '9' }

// Encode returns the canonical encoding of s, which must be valid
// UTF-8. Equal adjacent code points are merged into one maximal run;
// no Unicode normalization is performed.
func Encode(s string) string {
	var b []byte
	for _, r := range runs.Split(s) {
		if r.N >= 2 {
			b = runs.AppendCount(b, r.N)
		}
		b = appendSymbol(b, r.R)
	}
	return string(b)
}

func appendSymbol(b []byte, r rune) []byte {
	if r == '\\' || '0' <= r && r <= '9' {
		return append(b, '\\', byte(r))
	}
	return utf8.AppendRune(b, r)
}

// Decode strictly decodes t in one shot with the DefaultLimit.
func Decode(t string) (string, error) {
	d := NewDecoder()
	if _, err := d.Write([]byte(t)); err != nil {
		return "", err
	}
	if err := d.Close(); err != nil {
		return "", err
	}
	return string(d.out), nil
}

// Decoder states.
const (
	stStart = iota // between runs
	stCount        // reading count digits
	stEscape       // just read a backslash
	stRune         // reading continuation bytes of a multi-byte symbol
)

// Decoder is a streaming strict decoder. Input bytes are inspected
// exactly once each, so Write may be called with any chunking.
type Decoder struct {
	out       []byte
	limit     int
	seen      int // input bytes inspected so far
	off       int // offset of the byte currently processed
	state     int
	cnt       runs.Counter
	hasCount  bool
	firstDig  byte
	runStart  int
	escAt     int
	prev      rune
	hasPrev   bool
	rbuf      [4]byte
	rneed     int
	rgot      int
	err       error
}

// NewDecoder returns a Decoder with output capped at DefaultLimit.
func NewDecoder() *Decoder { return &Decoder{limit: DefaultLimit} }

// SetLimit caps the total decoded output in bytes; exceeding it
// fails with ErrTooLarge. Call before the first Write.
func (d *Decoder) SetLimit(n int) { d.limit = n }

// Inspected reports how many input bytes have been examined.
func (d *Decoder) Inspected() int { return d.seen }

// Write decodes a chunk of encoded text, appending to the output.
func (d *Decoder) Write(p []byte) (int, error) {
	if d.err != nil {
		return 0, d.err
	}
	for i, b := range p {
		if err := d.step(b); err != nil {
			d.err = err
			return i, err
		}
		d.off++
	}
	return len(p), nil
}

// Close finishes decoding and reports truncated final runs.
func (d *Decoder) Close() error {
	if d.err != nil {
		return d.err
	}
	switch d.state {
	case stEscape:
		d.err = &Error{ErrTrailingEscape, d.escAt}
	case stCount:
		if d.err = d.checkCount(); d.err == nil {
			d.err = &Error{ErrMissingSymbol, d.off}
		}
	case stRune:
		d.err = &Error{ErrInvalidUTF8, d.runStart}
	}
	return d.err
}

// Bytes returns the decoded output accumulated so far.
func (d *Decoder) Bytes() []byte { return d.out }

func (d *Decoder) step(b byte) error {
	d.seen++
	switch d.state {
	case stCount:
		if isDigit(b) {
			if d.firstDig == '0' {
				return &Error{ErrLeadingZero, d.runStart}
			}
			d.cnt.Add(uint64(b - '0'))
			return nil
		}
		if err := d.checkCount(); err != nil {
			return err
		}
		return d.symbol(b)
	case stEscape:
		if isDigit(b) || b == '\\' {
			return d.emit(rune(b))
		}
		return &Error{ErrBadEscape, d.escAt}
	case stRune:
		if b&0xC0 != 0x80 {
			return &Error{ErrInvalidUTF8, d.off}
		}
		d.rbuf[d.rgot] = b
		d.rgot++
		if d.rgot < d.rneed {
			return nil
		}
		r, _ := utf8.DecodeRune(d.rbuf[:d.rgot])
		if r == utf8.RuneError {
			return &Error{ErrInvalidUTF8, d.runStart}
		}
		return d.emit(r)
	default: // stStart
		d.runStart = d.off
		d.hasCount = false
		d.cnt.Reset(uint64(d.limit) + 1)
		if isDigit(b) {
			d.hasCount = true
			d.firstDig = b
			d.cnt.Add(uint64(b - '0'))
			d.state = stCount
			return nil
		}
		return d.symbol(b)
	}
}

// checkCount rejects count values that the canonical form forbids.
func (d *Decoder) checkCount() error {
	switch d.cnt.Value() {
	case 0:
		return &Error{ErrZeroCount, d.runStart}
	case 1:
		return &Error{ErrExplicitOne, d.runStart}
	}
	return nil
}

// symbol handles the first byte of a run's symbol.
func (d *Decoder) symbol(b byte) error {
	switch {
	case b == '\\':
		d.escAt = d.off
		d.state = stEscape
	case b < 0x80:
		return d.emit(rune(b))
	case b&0xE0 == 0xC0:
		d.rbuf[0], d.rgot, d.rneed, d.state = b, 1, 2, stRune
	case b&0xF0 == 0xE0:
		d.rbuf[0], d.rgot, d.rneed, d.state = b, 1, 3, stRune
	case b&0xF8 == 0xF0:
		d.rbuf[0], d.rgot, d.rneed, d.state = b, 1, 4, stRune
	default:
		return &Error{ErrInvalidUTF8, d.off}
	}
	return nil
}

// emit completes a run of n copies of r.
func (d *Decoder) emit(r rune) error {
	n := 1
	if d.hasCount {
		n = int(d.cnt.Value())
	}
	if d.hasPrev && d.prev == r {
		return &Error{ErrAdjacentSame, d.runStart}
	}
	size := utf8.RuneLen(r)
	if n > (d.limit-len(d.out))/size {
		return &Error{ErrTooLarge, d.runStart}
	}
	sym := utf8.AppendRune(nil, r)
	for i := 0; i < n; i++ {
		d.out = append(d.out, sym...)
	}
	d.prev, d.hasPrev = r, true
	d.state = stStart
	return nil
}
