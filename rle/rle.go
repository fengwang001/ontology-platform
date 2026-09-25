// Package rle implements an escaped run-length encoding: a run is an
// optional decimal count plus one code point; digits and backslash are
// backslash-escaped, count 1 is omitted, and runs are maximal.
package rle

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"unicode/utf8"

	"ontology/runs"
)

// DefaultLimit is Decode's output byte cap; NewDecoder allows other limits.
const DefaultLimit = 16 << 20

var (
	ErrCountOne      = errors.New("rle: explicit count 1")
	ErrCountZero     = errors.New("rle: zero count")
	ErrLeadingZero   = errors.New("rle: count with leading zero")
	ErrAdjacentSame  = errors.New("rle: adjacent runs share a symbol")
	ErrBadEscape     = errors.New("rle: escape not followed by digit or backslash")
	ErrLoneEscape    = errors.New("rle: trailing backslash")
	ErrMissingSymbol = errors.New("rle: count without symbol")
	ErrInvalidUTF8   = errors.New("rle: invalid UTF-8")
	ErrOutputLimit   = errors.New("rle: decoded output exceeds limit")
)

// Error is a strict-decode failure; Off is the input byte offset.
type Error struct {
	Err error
	Off int
}

func (e *Error) Error() string { return fmt.Sprintf("%v at byte %d", e.Err, e.Off) }
func (e *Error) Unwrap() error { return e.Err }

// Encode renders s in canonical form.
func Encode(s string) string {
	var b []byte
	for _, r := range runs.Split(s) {
		if r.Count > 1 {
			b = runs.AppendDecimal(b, r.Count)
		}
		if r.Rune == '\\' || '0' <= r.Rune && r.Rune <= '9' {
			b = append(b, '\\')
		}
		b = utf8.AppendRune(b, r.Rune)
	}
	return string(b)
}

// Decode strict-decodes t with DefaultLimit.
func Decode(t string) (string, error) {
	var buf bytes.Buffer
	d := NewDecoder(&buf, DefaultLimit)
	if _, err := d.Write([]byte(t)); err != nil {
		return "", err
	}
	if err := d.Close(); err != nil {
		return "", err
	}
	return buf.String(), nil
}

const (stStart = iota; stCount; stEsc; stRune)

// Decoder is a streaming strict decoder writing decoded bytes to w.
type Decoder struct {
	w      io.Writer
	err    error
	prev   rune
	count  uint64
	pend   [4]byte
	first  byte
	state  int
	pos    int
	runOff int
	escOff int
	pendLn int
	cntLn  int
	limit  int64
	written int64
	seen   int64
}

// NewDecoder returns a Decoder writing to w with the given output limit.
func NewDecoder(w io.Writer, limit int64) *Decoder { return &Decoder{w: w, limit: limit, prev: -1} }

// Examined reports how many input bytes were examined (each exactly once).
func (d *Decoder) Examined() int64 { return d.seen }

func (d *Decoder) fail(err error, off int) error {
	d.err = &Error{Err: err, Off: off}
	return d.err
}

// Write feeds encoded bytes into the decoder.
func (d *Decoder) Write(p []byte) (int, error) {
	if d.err != nil {
		return 0, d.err
	}
	for i, b := range p {
		if err := d.step(b); err != nil {
			return i, err
		}
	}
	return len(p), nil
}

// Close finishes decoding, reporting a truncated final run.
func (d *Decoder) Close() error {
	if d.err != nil {
		return d.err
	}
	switch d.state {
	case stCount:
		if err := d.checkCount(); err != nil {
			return err
		}
		return d.fail(ErrMissingSymbol, d.runOff)
	case stEsc:
		return d.fail(ErrLoneEscape, d.escOff)
	case stRune:
		return d.fail(ErrInvalidUTF8, d.runOff)
	}
	return nil
}

func (d *Decoder) step(b byte) error {
	d.seen++
	pos := d.pos
	d.pos++
	switch d.state {
	case stCount:
		if isDigit(b) {
			if d.cntLn == 1 && d.first == '0' {
				return d.fail(ErrLeadingZero, d.runOff)
			}
			v, ok := runs.AddDigit(d.count, uint64(b-'0'), uint64(d.limit))
			if !ok {
				return d.fail(ErrOutputLimit, pos)
			}
			d.count, d.cntLn = v, d.cntLn+1
			return nil
		}
		if err := d.checkCount(); err != nil {
			return err
		}
	case stEsc:
		if isDigit(b) || b == '\\' {
			return d.emit([]byte{b}, rune(b))
		}
		return d.fail(ErrBadEscape, d.escOff)
	case stRune:
		return d.runeByte(b, pos)
	}
	return d.startByte(b, pos)
}

func (d *Decoder) startByte(b byte, pos int) error {
	switch {
	case isDigit(b):
		d.state, d.count, d.cntLn, d.first, d.runOff = stCount, uint64(b-'0'), 1, b, pos
	case b == '\\':
		d.state, d.escOff = stEsc, pos
		if d.cntLn == 0 {
			d.runOff = pos
		}
	default:
		return d.runeByte(b, pos)
	}
	return nil
}

func (d *Decoder) runeByte(b byte, pos int) error {
	if d.pendLn == 0 {
		if d.cntLn == 0 {
			d.runOff = pos
		}
	}
	d.pend[d.pendLn] = b
	d.pendLn++
	if !utf8.FullRune(d.pend[:d.pendLn]) {
		d.state = stRune
		return nil
	}
	r, size := utf8.DecodeRune(d.pend[:d.pendLn])
	if r == utf8.RuneError && size == 1 {
		return d.fail(ErrInvalidUTF8, d.runOff)
	}
	return d.emit(d.pend[:size], r)
}

func (d *Decoder) checkCount() error {
	if d.cntLn == 1 && d.first == '0' {
		return d.fail(ErrCountZero, d.runOff)
	}
	if d.cntLn == 1 && d.first == '1' {
		return d.fail(ErrCountOne, d.runOff)
	}
	return nil
}

func (d *Decoder) emit(sym []byte, r rune) error {
	n := d.count
	if d.cntLn == 0 {
		n = 1
	}
	if r == d.prev {
		return d.fail(ErrAdjacentSame, d.runOff)
	}
	if n > uint64((d.limit-d.written)/int64(len(sym))) {
		return d.fail(ErrOutputLimit, d.runOff)
	}
	var tmp [4]byte
	blk := append(tmp[:0], sym...)
	total := int64(n) * int64(len(sym))
	for len(blk) < 1<<15 && int64(len(blk))*2 <= total {
		blk = append(blk, blk...)
	}
	for ; total > 0; total -= int64(len(blk)) {
		w := blk
		if int64(len(w)) > total {
			w = w[:total]
		}
		if _, err := d.w.Write(w); err != nil {
			return err
		}
	}
	d.written += int64(n) * int64(len(sym))
	d.prev = r
	d.state, d.count, d.cntLn, d.pendLn = stStart, 0, 0, 0
	return nil
}

func isDigit(b byte) bool { return '0' <= b && b <= '9' }
