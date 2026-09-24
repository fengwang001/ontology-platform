// Package rle implements a strict escaped run-length codec.
package rle

import (
	"errors"
	"fmt"
	"math/big"
	"ontology/runs"
	"strings"
	"unicode/utf8"
)

const stRun, stCount, stEscape, stRune = 0, 1, 2, 3

var ErrCountOne, ErrCountZero, ErrLeadingZero = errors.New("explicit count 1"), errors.New("count 0"), errors.New("count with leading zero")
var ErrAdjacentSame, ErrBadEscape, ErrLoneEscape = errors.New("adjacent runs with equal symbols"), errors.New("backslash before non-digit, non-backslash"), errors.New("trailing backslash")
var ErrNoSymbol, ErrBadUTF8, ErrTooLong = errors.New("count without symbol"), errors.New("invalid UTF-8"), errors.New("decoded output exceeds limit")
var inspected int64

// Error reports a decode failure; Err is one of the sentinels above.
type Error struct {
	Offset int
	Err    error
}

func (e *Error) Error() string { return fmt.Sprintf("rle: byte %d: %v", e.Offset, e.Err) }
func (e *Error) Unwrap() error { return e.Err }
func Inspected() int64         { return inspected }
func Encode(s string) string {
	var b strings.Builder
	runs.Split(s, func(sym rune, n int) {
		if n >= 2 {
			b.Write(runs.AppendCount(nil, n))
		}
		if sym == '\\' || '0' <= sym && sym <= '9' {
			b.WriteByte('\\')
		}
		b.WriteRune(sym)
	})
	return b.String()
}

// Decoder is a streaming strict decoder; the zero value is ready.
type Decoder struct {
	MaxOutput          int64 // output byte cap; 0 means 64<<20
	buf                strings.Builder
	digits, pending    []byte
	count              *big.Int
	prev               rune
	left               int64
	state, off, runOff int
}

func (d *Decoder) Write(p []byte) (int, error) {
	if d.count == nil {
		d.count, d.prev, d.left = big.NewInt(1), -1, d.MaxOutput
		if d.left == 0 {
			d.left = 64 << 20
		}
	}
	for i, c := range p {
		inspected++
		if err := d.step(c); err != nil {
			return i, err
		}
		d.off++
	}
	return len(p), nil
}

func (d *Decoder) step(c byte) error {
	switch d.state {
	case stEscape:
		d.state = stRun
		if c != '\\' && (c < '0' || c > '9') {
			return &Error{d.off, ErrBadEscape}
		}
		return d.emit(rune(c))
	case stRune:
		return d.runeByte(c)
	case stCount:
		if '0' <= c && c <= '9' {
			d.digits = append(d.digits, c)
			return nil
		}
		switch s := string(d.digits); {
		case len(s) > 1 && s[0] == '0':
			return &Error{d.runOff, ErrLeadingZero}
		case s == "0":
			return &Error{d.runOff, ErrCountZero}
		case s == "1":
			return &Error{d.runOff, ErrCountOne}
		default:
			d.count = runs.ParseCount(s)
		}
		d.state = stRun
	}
	d.runOff = d.off
	switch {
	case c == '\\':
		d.state = stEscape
	case '0' <= c && c <= '9':
		d.digits, d.state = append(d.digits[:0], c), stCount
	default:
		return d.runeByte(c)
	}
	return nil
}

func (d *Decoder) runeByte(c byte) error {
	d.pending = append(d.pending, c)
	if !utf8.FullRune(d.pending) {
		d.state = stRune
		return nil
	}
	r, size := utf8.DecodeRune(d.pending)
	d.pending, d.state = d.pending[:0], stRun
	if r == utf8.RuneError && size == 1 {
		return &Error{d.runOff, ErrBadUTF8}
	}
	return d.emit(r)
}

func (d *Decoder) emit(r rune) error {
	if r == d.prev {
		return &Error{d.runOff, ErrAdjacentSame}
	}
	need := new(big.Int).Mul(d.count, big.NewInt(int64(utf8.RuneLen(r))))
	if need.Cmp(big.NewInt(d.left)) > 0 {
		return &Error{d.runOff, ErrTooLong}
	}
	d.buf.WriteString(strings.Repeat(string(r), int(d.count.Int64())))
	d.left, d.prev = d.left-need.Int64(), r
	d.count.SetInt64(1)
	return nil
}

func (d *Decoder) Close() (string, error) {
	if err := []error{nil, ErrNoSymbol, ErrLoneEscape, ErrBadUTF8}[d.state]; err != nil {
		return "", &Error{d.runOff, err}
	}
	return d.buf.String(), nil
}
func Decode(t string) (string, error) {
	d := new(Decoder)
	if _, err := d.Write([]byte(t)); err != nil {
		return "", err
	}
	return d.Close()
}
