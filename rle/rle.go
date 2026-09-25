// Package rle implements an escaped run-length codec. Decode is
// strict: it accepts only the canonical encodings produced by Encode.
package rle

import (
	"errors"
	"fmt"
	"math/big"
	"strings"
	"sync/atomic"
	"unicode/utf8"

	"ontology/runs"
)

var (
	ErrExplicitOne    = errors.New("rle: explicit count 1")
	ErrZeroCount      = errors.New("rle: zero count")
	ErrLeadingZero    = errors.New("rle: count with leading zero")
	ErrAdjacentSame   = errors.New("rle: adjacent runs share a symbol")
	ErrBadEscape      = errors.New("rle: escape not followed by digit or backslash")
	ErrTrailingEscape = errors.New("rle: trailing backslash")
	ErrMissingSymbol  = errors.New("rle: count without symbol")
	ErrInvalidUTF8    = errors.New("rle: invalid UTF-8")
	ErrOutputTooLarge = errors.New("rle: decoded output exceeds byte limit")
)

// DecodeError reports a failure and the byte offset where it occurred.
type DecodeError struct {
	Err    error
	Offset int64
}

func (e *DecodeError) Error() string { return fmt.Sprintf("%v (byte %d)", e.Err, e.Offset) }
func (e *DecodeError) Unwrap() error { return e.Err }

// DefaultMaxOutput is the decoded-byte limit used by Decode.
const DefaultMaxOutput = 1 << 30

var inspected atomic.Int64

// InspectedBytes returns the total number of input bytes examined.
func InspectedBytes() int64 { return inspected.Load() }

// Encode returns the canonical encoding of s.
func Encode(s string) string {
	var b strings.Builder
	for _, r := range runs.Split(s) {
		if r.N >= 2 {
			b.Write(runs.AppendCount(nil, r.N))
		}
		if r.Sym == '\\' || ('0' <= r.Sym && r.Sym <= '9') {
			b.WriteByte('\\')
		}
		b.WriteRune(r.Sym)
	}
	return b.String()
}

// Decode strictly decodes t; see the Err* sentinels for rejections.
func Decode(t string) (string, error) {
	d := NewDecoder(DefaultMaxOutput)
	if _, err := d.Write([]byte(t)); err != nil {
		return "", err
	}
	return d.Close()
}

const ( stDefault = iota; stCount; stEscape )

// Decoder incrementally strict-decodes bytes, limiting decoded output
// to maxOut bytes. Counts may be arbitrarily large: parsed with
// big.Int and checked against the budget before output is produced.
type Decoder struct {
	count, pend       []byte
	out               strings.Builder
	maxOut, off, mark int64
	state             int
	prev              rune
	err               error
}

// NewDecoder returns a Decoder with output limited to maxOut bytes.
func NewDecoder(maxOut int64) *Decoder { return &Decoder{maxOut: maxOut, prev: -1} }

// Write consumes encoded bytes; any chunking gives identical results.
func (d *Decoder) Write(p []byte) (int, error) {
	if d.err != nil {
		return 0, d.err
	}
	for i, b := range p {
		inspected.Add(1)
		d.off++
		if len(d.pend) == 0 {
			d.mark = d.off - 1
		}
		d.pend = append(d.pend, b)
		if !utf8.FullRune(d.pend) {
			continue
		}
		r, size := utf8.DecodeRune(d.pend)
		d.pend = d.pend[:0]
		err := d.accept(r, d.off-int64(size))
		if r == utf8.RuneError && size == 1 {
			err = &DecodeError{ErrInvalidUTF8, d.mark}
		}
		if err != nil {
			d.err = err
			return i, err
		}
	}
	return len(p), nil
}

// Close finishes decoding and returns the decoded output.
func (d *Decoder) Close() (string, error) {
	if d.err == nil {
		switch {
		case len(d.pend) > 0:
			d.err = &DecodeError{ErrInvalidUTF8, d.mark}
		case d.state == stEscape:
			d.err = &DecodeError{ErrTrailingEscape, d.mark}
		case d.state == stCount:
			d.err = &DecodeError{ErrMissingSymbol, d.off}
		}
	}
	return d.out.String(), d.err
}

func (d *Decoder) accept(r rune, off int64) error {
	digit := '0' <= r && r <= '9'
	switch {
	case d.state == stDefault && digit:
		d.count, d.state = append(d.count[:0], byte(r)), stCount
	case d.state == stDefault && r == '\\':
		d.state, d.mark = stEscape, off
	case d.state == stCount && digit:
		if d.count[0] == '0' {
			return &DecodeError{ErrLeadingZero, off}
		}
		d.count = append(d.count, byte(r))
	case d.state == stEscape && r != '\\' && !digit:
		return &DecodeError{ErrBadEscape, off}
	case d.state == stCount && r == '\\':
		d.state, d.mark = stEscape, off
	default: // a symbol arrives; any pending count ends here
		return d.emit(r, off)
	}
	return nil
}

func (d *Decoder) emit(r rune, off int64) error {
	if len(d.count) == 1 {
		if e := badCount[d.count[0]]; e != nil {
			return &DecodeError{e, off}
		}
	}
	c := big.NewInt(1)
	if len(d.count) > 0 {
		c = runs.ParseCount(string(d.count))
	}
	if c.Mul(c, big.NewInt(int64(utf8.RuneLen(r)))).Cmp(big.NewInt(d.maxOut-int64(d.out.Len()))) > 0 {
		return &DecodeError{ErrOutputTooLarge, off}
	}
	if d.prev == r {
		return &DecodeError{ErrAdjacentSame, off}
	}
	d.prev, d.count, d.state = r, d.count[:0], stDefault
	d.out.WriteString(strings.Repeat(string(r), int(c.Int64())))
	return nil
}

var badCount = map[byte]error{'0': ErrZeroCount, '1': ErrExplicitOne}
