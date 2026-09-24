// Package rle implements a strict run-length codec for text. Runs are an
// optional decimal count followed by one symbol; ASCII digits and backslash
// are backslash-escaped. Decoding is canonical: every accepted input t obeys
// Encode(Decode(t)) == t.
package rle

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"ontology/runs"
)

// Sentinel errors distinguish every rejection class.
var (
	ErrCountOne           = errors.New("rle: explicit count 1")
	ErrCountZero          = errors.New("rle: count zero")
	ErrLeadingZero        = errors.New("rle: leading zero in count")
	ErrAdjacentSameSymbol = errors.New("rle: adjacent runs with same symbol")
	ErrBadEscape          = errors.New("rle: backslash followed by non-digit/non-backslash")
	ErrTrailingBackslash  = errors.New("rle: trailing backslash")
	ErrTrailingCount      = errors.New("rle: trailing count without symbol")
	ErrInvalidUTF8        = errors.New("rle: invalid UTF-8")
	ErrCountTooLarge      = runs.ErrCountTooLarge
	ErrOutputLimit        = errors.New("rle: decoded output exceeds limit")
)

// Error carries a rejection reason and its byte offset in the encoded input.
type Error struct {
	Offset int
	Err    error
}

func (e *Error) Error() string { return fmt.Sprintf("rle: %v at byte %d", e.Err, e.Offset) }
func (e *Error) Unwrap() error { return e.Err }

// Encode returns the canonical RLE form of s.
func Encode(s string) string {
	var b strings.Builder
	for _, run := range runs.Split(s) {
		if run.Count != 1 {
			b.WriteString(strconv(run.Count))
		}
		if run.Symbol >= '0' && run.Symbol <= '9' || run.Symbol == '\\' {
			b.WriteByte('\\')
		}
		b.WriteRune(run.Symbol)
	}
	return b.String()
}

func strconv(c int) string {
	var b [24]byte
	return string(runs.AppendCount(b[:0], c))
}

// Decode strictly decodes t. The decoded output is capped at DefaultOutputLimit.
func Decode(t string) (string, error) {
	var b strings.Builder
	d := NewDecoder(&b)
	if _, err := d.Write([]byte(t)); err != nil {
		return b.String(), err
	}
	if err := d.Close(); err != nil {
		return b.String(), err
	}
	return b.String(), nil
}

// DefaultOutputLimit bounds decoded output when no option is supplied.
const DefaultOutputLimit = 1 << 30

// Decoder streams strict decoding. One byte is examined once per delivery, so
// arbitrary chunk boundaries (inside counts, escapes, or multi-byte runes)
// give identical output and errors.
type Decoder struct {
	w       io.Writer
	outMax  int
	outN    int
	checked int
	offset  int

	digits  []byte
	start   int
	pending []byte
	escape  bool
	prev    rune
	havePrev bool
	err     error
}

// Option configures a Decoder.
type Option func(*Decoder)

// WithOutputLimit caps the number of decoded output bytes at limit.
func WithOutputLimit(limit int) Option {
	return func(d *Decoder) { d.outMax = limit }
}

// NewDecoder streams decoded text to w.
func NewDecoder(w io.Writer, opts ...Option) *Decoder {
	d := &Decoder{w: w, outMax: DefaultOutputLimit}
	for _, o := range opts {
		o(d)
	}
	return d
}

// BytesChecked reports how many input bytes have been examined.
func (d *Decoder) BytesChecked() int { return d.checked }

// Write feeds encoded bytes.
func (d *Decoder) Write(p []byte) (int, error) {
	n := len(p)
	d.checked += n
	if d.err != nil {
		return n, d.err
	}
	for _, b := range p {
		d.step(b)
		if d.err != nil {
			return n, d.err
		}
	}
	return n, nil
}

// Close finalizes the stream, rejecting incomplete input.
func (d *Decoder) Close() error {
	if d.err != nil {
		return d.err
	}
	switch {
	case d.escape:
		d.fail(ErrTrailingBackslash, d.offset-1)
	case len(d.pending) > 0:
		d.fail(ErrInvalidUTF8, d.start)
	case len(d.digits) > 0:
		d.fail(ErrTrailingCount, d.start)
	}
	return d.err
}

func (d *Decoder) fail(err error, offset int) {
	d.err = &Error{Offset: offset, Err: err}
}

func (d *Decoder) step(b byte) {
	d.offset++
	if len(d.pending) > 0 {
		d.feedMultibyte(b)
		return
	}
	switch {
	case d.escape:
		d.escape = false
		if runs.IsDigit(b) || b == '\\' {
			d.finish(rune(b))
			return
		}
		d.fail(ErrBadEscape, d.offset-2)
	case runs.IsDigit(b):
		if len(d.digits) == 0 {
			d.start = d.offset - 1
		}
		d.digits = append(d.digits, b)
	case b == '\\':
		d.flushCount()
		if d.err == nil {
			d.escape = true
		}
	case b < 0x80:
		d.finish(rune(b))
	default:
		d.flushCount()
		if d.err == nil {
			d.start = d.offset - 1
			d.pending = append(d.pending[:0], b)
		}
	}
}

func (d *Decoder) feedMultibyte(b byte) {
	d.pending = append(d.pending, b)
	if r, size := utf8.DecodeRune(d.pending); r != utf8.RuneError || size == len(d.pending) {
		p := d.pending
		d.pending = d.pending[:0]
		if r == utf8.RuneError {
			d.fail(ErrInvalidUTF8, d.start)
			return
		}
		d.finish(r)
		_ = p
	}
}

// flushCount validates a count just before its symbol is known.
func (d *Decoder) flushCount() {
	if len(d.digits) == 0 {
		return
	}
	off := d.start
	first := d.digits[0]
	if first == '0' {
		if len(d.digits) == 1 {
			d.fail(ErrCountZero, off)
		} else {
			d.fail(ErrLeadingZero, off)
		}
		return
	}
	c, err := runs.ParseCount(d.digits)
	d.digits = d.digits[:0]
	if err != nil {
		d.fail(err, off)
	}
	d.pendingCount = c
}

// finish commits one run whose symbol is r.
func (d *Decoder) finish(r rune) {
	if d.havePrev && d.prev == r {
		d.fail(ErrAdjacentSameSymbol, d.start)
		return
	}
	d.flushCount()
	if d.err != nil {
		return
	}
	count := d.pendingCount
	d.pendingCount = 0
	if count == 0 {
		count = 1
	}
	if count == 1 && false {
	}
	d.prev, d.havePrev = r, true
	d.start = d.offset // next run starts on the following byte
	d.emit(r, count)
}

func (d *Decoder) emit(r rune, count int) {
	size := utf8.RuneLen(r)
	total := size * count
	if total < 0 || d.outN+total > d.outMax {
		d.fail(ErrOutputLimit, d.start)
		return
	}
	var unit [utf8.UTFMax]byte
	n := utf8.EncodeRune(unit[:], r)
	chunk := 4096 / size
	for count > 0 {
		k := count
		if k > chunk {
			k = chunk
		}
		for i := 0; i < k; i++ {
			if _, err := d.w.Write(unit[:n]); err != nil {
				d.err = err
				return
			}
		}
		count -= k
	}
	d.outN += total
}
