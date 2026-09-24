// Package rle is a strict escaped run-length codec for Unicode codepoints.
// Digits '0'-'9' and backslash are written escaped ("\1", "\\"); a count of
// 1 is omitted. Decode accepts only canonical encodings.
package rle

import (
	"errors"
	"io"
	"unicode/utf8"

	"ontology/runs"
)

var (
	ErrExplicitOne       = errors.New("rle: explicit run count 1")
	ErrZeroCount         = errors.New("rle: run count 0")
	ErrLeadingZero       = errors.New("rle: count with leading zero")
	ErrAdjacentSame      = errors.New("rle: adjacent runs with same symbol")
	ErrBadEscape         = errors.New("rle: invalid escape")
	ErrTrailingBackslash = errors.New("rle: trailing backslash")
	ErrTrailingCount     = errors.New("rle: count without symbol")
	ErrInvalidUTF8       = errors.New("rle: invalid UTF-8")
	ErrOutputTooLarge    = errors.New("rle: decoded output exceeds limit")
)

// Error wraps a decode failure with its byte offset in the logical input.
type Error struct {
	Op  error
	Off int
}

func (e *Error) Error() string { return e.Op.Error() }
func (e *Error) Unwrap() error { return e.Op }

const defaultMaxOutput = 1 << 30

// Option configures a Decoder.
type Option func(*Decoder)

// WithMaxOutput caps decoded output bytes; 0 keeps the 1 GiB default.
func WithMaxOutput(n uint64) Option {
	return func(d *Decoder) {
		if n > 0 {
			d.max = n
		}
	}
}

// Decoder is an io.WriteCloser. Splitting input across Writes never changes
// output or errors.
type Decoder struct {
	w          io.Writer
	max        uint64
	checked    int
	pos        int
	runStart   int
	state      byte
	count      uint64
	digits     int
	firstZero  bool
	escaped    bool
	pending    runs.Run
	have       bool
	written    uint64
	mb         [utf8.UTFMax]byte
	mbLen      int
	mbNeed     int
	err        error
	closed     bool
}

// NewDecoder streams canonical RLE data into w.
func NewDecoder(w io.Writer, opts ...Option) *Decoder {
	d := &Decoder{w: w, max: defaultMaxOutput}
	for _, opt := range opts {
		opt(d)
	}
	return d
}

// CheckedBytes reports how many input bytes the state machine has inspected.
func (d *Decoder) CheckedBytes() int { return d.checked }

// Encode returns the canonical RLE form of s.
func Encode(s string) string {
	b := make([]byte, 0, len(s))
	for _, r := range runs.Split(s) {
		b = runs.AppendCount(b, r.Count)
		if '0' <= r.Symbol && r.Symbol <= '9' || r.Symbol == '\\' {
			b = append(b, '\\')
		}
		b = utf8.AppendRune(b, r.Symbol)
	}
	return string(b)
}

type byteBuffer struct{ b *[]byte }

func (w byteBuffer) Write(p []byte) (int, error) {
	*w.b = append(*w.b, p...)
	return len(p), nil
}

// Decode strictly decodes t, accepting only canonical encodings.
func Decode(t string, opts ...Option) (string, error) {
	var buf []byte
	d := NewDecoder(byteBuffer{&buf}, opts...)
	if _, err := d.Write([]byte(t)); err != nil {
		return "", err
	}
	if err := d.Close(); err != nil {
		return "", err
	}
	return string(buf), nil
}

// Write feeds one encoded chunk.
func (d *Decoder) Write(p []byte) (int, error) {
	d.checked += len(p)
	if d.err != nil || d.closed {
		return len(p), d.err
	}
	for _, c := range p {
		d.pos++
		if err := d.feed(c); err != nil {
			d.err = err
			return len(p), err
		}
	}
	return len(p), nil
}

func (d *Decoder) fail(op error) error { return &Error{Op: op, Off: d.runStart} }

func (d *Decoder) feed(c byte) error {
	if d.escaped {
		d.escaped = false
		if '0' <= c && c <= '9' || c == '\\' {
			return d.finish(rune(c))
		}
		return d.fail(ErrBadEscape)
	}
	if d.mbLen > 0 {
		d.mb[d.mbLen] = c
		d.mbLen++
		if d.mbLen < d.mbNeed {
			return nil
		}
		r, _ := utf8.DecodeRune(d.mb[:d.mbNeed])
		d.mbLen = 0
		if r == utf8.RuneError {
			return d.fail(ErrInvalidUTF8)
		}
		return d.finish(r)
	}
	switch {
	case '0' <= c && c <= '9':
		if d.digits == 0 {
			d.runStart, d.firstZero = d.pos-1, c == '0'
		}
		v, err := runs.PushDigit(d.count, c)
		if err != nil {
			return d.fail(err)
		}
		d.count, d.digits = v, d.digits+1
	case c == '\\':
		if d.digits == 0 {
			d.runStart = d.pos - 1
		}
		d.escaped = true
	case c < 0x80:
		if d.digits == 0 {
			d.runStart = d.pos - 1
		}
		return d.finish(rune(c))
	default:
		if d.digits == 0 {
			d.runStart = d.pos - 1
		}
		d.mb[0], d.mbLen = c, 1
		d.mbNeed = utf8.RuneLen(rune(c))
		if d.mbNeed < 2 {
			return d.fail(ErrInvalidUTF8)
		}
	}
	return nil
}

func (d *Decoder) finish(symbol rune) error {
	count := d.count
	switch {
	case d.digits > 1 && d.firstZero:
		return d.fail(ErrLeadingZero)
	case d.digits > 0 && count == 0:
		return d.fail(ErrZeroCount)
	case count == 1:
		return d.fail(ErrExplicitOne)
	case d.digits == 0:
		count = 1
	}
	if d.have && d.pending.Symbol == symbol {
		return d.fail(ErrAdjacentSame)
	}
	if err := d.emit(); err != nil {
		return err
	}
	d.pending, d.have = runs.Run{Symbol: symbol, Count: count}, true
	d.count, d.digits, d.firstZero = 0, 0, false
	return nil
}

func (d *Decoder) emit() error {
	if !d.have {
		return nil
	}
	size := uint64(utf8.RuneLen(d.pending.Symbol))
	add := size * d.pending.Count
	if add/size != d.pending.Count || d.written+add > d.max {
		return d.fail(ErrOutputTooLarge)
	}
	var unit [utf8.UTFMax]byte
	n := utf8.EncodeRune(unit[:], d.pending.Symbol)
	for d.pending.Count > 0 {
		times := d.pending.Count
		if times > 4096/uint64(n) {
			times = 4096 / uint64(n)
		}
		buf := make([]byte, 0, times*uint64(n))
		for i := uint64(0); i < times; i++ {
			buf = append(buf, unit[:n]...)
		}
		if _, err := d.w.Write(buf); err != nil {
			return err
		}
		d.pending.Count -= times
	}
	d.written, d.have = d.written+add, false
	return nil
}

// Close flushes the final run or reports truncated input.
func (d *Decoder) Close() error {
	if d.err != nil {
		return d.err
	}
	if d.closed {
		return nil
	}
	d.closed = true
	switch {
	case d.escaped:
		d.err = d.fail(ErrTrailingBackslash)
	case d.mbLen > 0:
		d.err = d.fail(ErrInvalidUTF8)
	case d.digits > 0:
		d.err = d.fail(ErrTrailingCount)
	default:
		d.err = d.emit()
	}
	return d.err
}
