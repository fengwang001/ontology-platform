// Package rle implements the escaped run-length codec (canonical form only).
package rle

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"ontology/runs"
)

// Sentinel errors for Decode; count-syntax and UTF-8 kinds live in runs.
var (
	ErrAdjacentSame, ErrBadEscape       = errors.New("rle: adjacent equal symbols"), errors.New("rle: bad escape")
	ErrDanglingEscape, ErrMissingSymbol = errors.New("rle: trailing backslash"), errors.New("rle: count without symbol")
	ErrOutputLimit                      = errors.New("rle: output exceeds the byte limit")

	checkedByte int64 // input bytes examined; the decoder is single-pass
)

// DecodeError reports a strict-decoding failure at byte offset Off.
type DecodeError struct {
	Kind error
	Off  int
}

func (e *DecodeError) Error() string { return fmt.Sprintf("%v (byte %d)", e.Kind, e.Off) }
func (e *DecodeError) Unwrap() error { return e.Kind }

// Encode returns the canonical encoding of s, which must be valid UTF-8.
func Encode(s string) string {
	var b strings.Builder
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

// Decode strictly decodes t, capping the output at 1<<30 bytes.
func Decode(t string) (string, error) {
	d := NewDecoder(1 << 30)
	d.Write([]byte(t))
	return d.Close()
}

// Decoder is a streaming strict decoder; any input chunking behaves alike.
type Decoder struct {
	out                strings.Builder
	digits             []byte
	scan               runs.RuneScanner
	err                error
	prev               rune
	limit              int64
	pos, escOff, start int
	esc, hasPrev       bool
}

// NewDecoder caps decoded output at limit bytes; negative = unlimited.
func NewDecoder(limit int64) *Decoder { return &Decoder{limit: limit} }

func (d *Decoder) fail(kind error, off int) error {
	d.err = &DecodeError{Kind: kind, Off: off}
	return d.err
}

// Write feeds p into the decoder, examining each byte exactly once.
func (d *Decoder) Write(p []byte) (int, error) {
	if d.err != nil {
		return 0, d.err
	}
	for i, b := range p {
		checkedByte++
		if err := d.step(b); err != nil {
			return i, err
		}
		d.pos++
	}
	return len(p), nil
}

// Close finishes decoding and returns the decoded string.
func (d *Decoder) Close() (string, error) {
	if d.err == nil {
		switch {
		case d.esc:
			d.fail(ErrDanglingEscape, d.escOff)
		case d.scan.Pending():
			d.fail(runs.ErrInvalidUTF8, d.start)
		case len(d.digits) > 0:
			d.fail(ErrMissingSymbol, d.start)
		}
	}
	return d.out.String(), d.err
}

func (d *Decoder) step(b byte) error {
	switch {
	case d.esc:
		d.esc = false
		if b != '\\' && (b < '0' || b > '9') {
			return d.fail(ErrBadEscape, d.pos)
		}
		return d.emit(rune(b))
	case d.scan.Pending():
		r, ok, err := d.scan.Feed(b)
		if err != nil {
			return d.fail(runs.ErrInvalidUTF8, d.pos)
		}
		if ok {
			return d.emit(r)
		}
	case '0' <= b && b <= '9':
		d.digits = append(d.digits, b)
	case b == '\\':
		d.esc, d.escOff = true, d.pos
	case b < 0x80:
		return d.emit(rune(b))
	default:
		if _, _, err := d.scan.Feed(b); err != nil {
			return d.fail(runs.ErrInvalidUTF8, d.pos)
		}
	}
	return nil
}

func (d *Decoder) emit(r rune) error {
	n, err := runs.Count(string(d.digits))
	if err != nil {
		return d.fail(err, d.start)
	}
	d.digits = d.digits[:0]
	if d.hasPrev && r == d.prev {
		return d.fail(ErrAdjacentSame, d.start)
	}
	if !n.IsUint64() || d.limit >= 0 && n.Uint64() > uint64(d.limit-int64(d.out.Len()))/uint64(utf8.RuneLen(r)) {
		return d.fail(ErrOutputLimit, d.start)
	}
	for c := n.Uint64(); c > 0; c-- {
		d.out.WriteRune(r)
	}
	d.prev, d.hasPrev, d.start = r, true, d.pos+1
	return nil
}
