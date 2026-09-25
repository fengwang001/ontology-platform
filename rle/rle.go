// Package rle implements escaped run-length encoding over Unicode code points.
package rle

import (
	"errors"
	"strings"
	"unicode/utf8"

	"ontology/runs"
)

// Sentinel errors; classify failures with errors.Is.
var (
	ErrExplicitOne   = errors.New("rle: explicit count of 1")
	ErrZeroCount     = errors.New("rle: count of 0")
	ErrLeadingZero   = errors.New("rle: leading zero in count")
	ErrAdjacentSame  = errors.New("rle: adjacent runs with same symbol")
	ErrBadEscape     = errors.New("rle: escape before non-digit non-backslash")
	ErrTrailingSlash = errors.New("rle: trailing backslash")
	ErrTrailingCount = errors.New("rle: trailing count without symbol")
	ErrInvalidUTF8   = errors.New("rle: invalid UTF-8 in symbol")
	ErrOutputLimit   = errors.New("rle: decoded output exceeds limit")
)

// DecodeError wraps a sentinel cause with the byte offset of failure.
type DecodeError struct {
	Offset int
	Err    error
}

func (e *DecodeError) Error() string { return e.Err.Error() }
func (e *DecodeError) Unwrap() error { return e.Err }

// Encode returns the canonical escaped RLE form of s.
func Encode(s string) string {
	var b strings.Builder
	for _, run := range runs.Scan(s) {
		if run.Count != 1 {
			b.Write(runs.AppendCount(nil, run.Count))
		}
		if run.Symbol == '\\' || run.Symbol >= '0' && run.Symbol <= '9' {
			b.WriteByte('\\')
		}
		b.WriteRune(run.Symbol)
	}
	return b.String()
}

// Decode strictly decodes canonical RLE text.
func Decode(t string) (string, error) {
	d := NewDecoder()
	if _, err := d.Write([]byte(t)); err != nil {
		return "", err
	}
	if err := d.Close(); err != nil {
		return "", err
	}
	return d.String(), nil
}

// Option configures a streaming Decoder.
type Option func(*Decoder)

// WithOutputLimit caps the total decoded byte count.
func WithOutputLimit(n int) Option { return func(d *Decoder) { d.limit = n } }

// Decoder incrementally parses RLE bytes; each input byte is examined once.
type Decoder struct {
	out      strings.Builder
	limit    int
	examined int64

	mode      byte // 0 start, 'd' digits, 's' after backslash, 'r' multibyte
	digits    []byte
	count     int64
	hasDigits bool

	raw    []byte // multibyte rune accumulated so far
	need   int    // full length of the rune
	runOff int    // byte offset of the current symbol
	esc    bool   // symbol was preceded by backslash

	havePrev bool
	prev     rune
}

const defaultLimit = 1 << 30 // 1 GiB of decoded output

// NewDecoder creates a streaming decoder.
func NewDecoder(opts ...Option) *Decoder {
	d := &Decoder{limit: defaultLimit}
	for _, o := range opts {
		o(d)
	}
	return d
}

func (d *Decoder) String() string  { return d.out.String() }
func (d *Decoder) Examined() int64 { return d.examined }

func (d *Decoder) fail(off int, err error) error {
	return &DecodeError{Offset: off, Err: err}
}

// Write feeds encoded bytes. Every byte contributes exactly one examination.
func (d *Decoder) Write(p []byte) (int, error) {
	for _, c := range p {
		off := int(d.examined)
		if d.mode == 'r' || d.mode == 's' {
			off = d.runOff
		}
		d.examined++
		if err := d.step(c, off); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}

// Close rejects incomplete trailing input.
func (d *Decoder) Close() error {
	switch d.mode {
	case 's':
		return d.fail(int(d.examined), ErrTrailingSlash)
	case 'd':
		return d.fail(int(d.examined), ErrTrailingCount)
	case 'r':
		return d.fail(d.runOff, ErrInvalidUTF8)
	}
	return nil
}

func (d *Decoder) step(c byte, off int) error {
	if d.mode == 'r' {
		return d.stepRune(c, off)
	}
	if d.mode == 's' {
		return d.stepEsc(c, off)
	}
	// Mode 0 or 'd'.
	if c >= '0' && c <= '9' {
		if len(d.digits) == 0 {
			d.runOff = off
		}
		d.digits = append(d.digits, c)
		n := d.count*10 + int64(c-'0')
		if n < d.count || n > runs.MaxCount {
			return d.fail(off, runs.ErrCountTooLarge)
		}
		d.count = n
		d.hasDigits = true
		d.mode = 'd'
		return nil
	}
	if err := d.validateDigits(off); err != nil {
		return err
	}
	if c == '\\' {
		d.mode = 's'
		d.runOff = off
		d.esc = true
		return nil
	}
	if c < 0x80 {
		return d.commit(rune(c), 1, off)
	}
	return d.startRune(c, off, true)
}

func (d *Decoder) stepEsc(c byte, off int) error {
	if c == '\\' || c >= '0' && c <= '9' {
		return d.commit(rune(c), 1, off)
	}
	if c < 0x80 {
		return d.fail(off, ErrBadEscape)
	}
	return d.startRune(c, off, false)
}

func (d *Decoder) startRune(c byte, off int, badOnEscape bool) error {
	r, size := utf8.DecodeRune([]byte{c})
	if badOnEscape && d.esc && c >= 0x80 {
		return d.fail(off, ErrBadEscape)
	}
	_ = badOnEscape
	_ = r
	_ = size
	return nil
}
