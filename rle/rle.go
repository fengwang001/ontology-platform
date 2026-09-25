// Package rle implements a strict escaped run-length codec (see DESIGN.md).
package rle

import (
	"errors"
	"fmt"
	"sync/atomic"
	"unicode/utf8"

	"ontology/runs"
)

var ErrExplicitOne, ErrZeroCount, ErrLeadingZero = errors.New("rle: explicit count 1"), errors.New("rle: zero count"), errors.New("rle: leading zero")
var ErrAdjacentSame, ErrBadEscape, ErrLoneEscape = errors.New("rle: adjacent equal symbols"), errors.New("rle: bad escape"), errors.New("rle: trailing backslash")
var ErrNoSymbol, ErrBadUTF8, ErrTooLarge = errors.New("rle: count without symbol"), errors.New("rle: invalid UTF-8"), errors.New("rle: output too large")

type Error struct {
	Off int
	Err error
}

func (e *Error) Error() string { return fmt.Sprintf("rle: offset %d: %v", e.Off, e.Err) }
func (e *Error) Unwrap() error { return e.Err }

// DefaultMaxOutput caps Decode output in bytes; examined counts input bytes.
const DefaultMaxOutput = 64 << 20

var examined atomic.Int64

func BytesExamined() int64 { return examined.Load() }

func Encode(s string) string {
	var b []byte
	for _, r := range runs.Split(s) {
		if r.Count > 1 {
			b = runs.AppendDecimal(b, uint64(r.Count))
		}
		if r.Rune == '\\' || '0' <= r.Rune && r.Rune <= '9' {
			b = append(b, '\\')
		}
		b = utf8.AppendRune(b, r.Rune)
	}
	return string(b)
}

func Decode(t string) (string, error) {
	d := NewDecoder(DefaultMaxOutput)
	_ = d.Write([]byte(t))
	return d.Close()
}

// Decoder is a streaming strict decoder; each input byte is examined once.
type Decoder struct {
	max                       int64
	out                       []byte
	err                       error
	asm                       runs.Assembler
	pos, runAt, state, digits int
	count                     uint64
	prev                      rune
}

// NewDecoder caps total decoded output at maxOutput bytes (negative: no cap).
func NewDecoder(maxOutput int64) *Decoder { return &Decoder{max: maxOutput, prev: -1} }
func (d *Decoder) Write(p []byte) error {
	for _, b := range p {
		if d.err != nil {
			return d.err
		}
		examined.Add(1)
		d.step(b)
		d.pos++
	}
	return d.err
}
func (d *Decoder) Close() (string, error) {
	if d.err == nil && d.state != 0 {
		d.fail(d.runAt, []error{nil, ErrBadUTF8, ErrLoneEscape}[d.state])
	}
	if d.err == nil && d.digits > 0 {
		d.fail(d.runAt, ErrNoSymbol)
	}
	if d.err != nil {
		return "", d.err
	}
	return string(d.out), nil
}
func (d *Decoder) fail(off int, err error) { d.err = &Error{off, err} }
func (d *Decoder) step(b byte) {
	switch d.state {
	case 2:
		d.state = 0
		if b != '\\' && !runs.IsDigit(b) {
			d.fail(d.pos, ErrBadEscape)
			return
		}
		d.finish(rune(b))
	case 1:
		d.feed(b)
	default:
		if d.digits == 0 {
			d.runAt = d.pos
		} else if !runs.IsDigit(b) && d.count < 2 {
			d.fail(d.runAt, []error{ErrZeroCount, ErrExplicitOne}[d.count])
			return
		}
		if runs.IsDigit(b) {
			if d.digits == 1 && d.count == 0 {
				d.fail(d.runAt, ErrLeadingZero)
				return
			}
			d.count, d.digits = runs.AddDigit(d.count, b), d.digits+1
			return
		}
		if b == '\\' {
			d.state = 2
			return
		}
		d.feed(b)
	}
}

func (d *Decoder) feed(b byte) {
	r, done, ok := d.asm.Feed(b)
	switch {
	case !ok:
		d.fail(d.pos, ErrBadUTF8)
	case !done:
		d.state = 1
	default:
		d.state = 0
		d.finish(r)
	}
}

func (d *Decoder) finish(r rune) {
	n := max(d.count, 1)
	if d.prev == r {
		d.fail(d.runAt, ErrAdjacentSame)
		return
	}
	if n*uint64(utf8.RuneLen(r)) > uint64(d.max)-uint64(len(d.out)) {
		d.fail(d.runAt, ErrTooLarge)
		return
	}
	for ; n > 0; n-- {
		d.out = utf8.AppendRune(d.out, r)
	}
	d.prev, d.count, d.digits = r, 0, 0
}
