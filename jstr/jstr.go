// Package jstr strictly decodes and minimally encodes single JSON string
// literals (RFC 8259). Invalid input is rejected, never repaired.
package jstr

import (
	"errors"
	"fmt"
	"sync/atomic"
	"unicode/utf8"

	"ontology/esc"
)

// Sentinel error kinds, distinguishable via errors.Is; failures are
// reported as *Error wrapping one of these.
var (
	ErrOpenQuote     = errors.New("jstr: missing opening quote")
	ErrUnterminated  = errors.New("jstr: missing closing quote")
	ErrTrailing      = errors.New("jstr: bytes after closing quote")
	ErrControl       = errors.New("jstr: unescaped control character")
	ErrUnknownEscape = errors.New("jstr: unknown escape sequence")
	ErrShortHex      = errors.New(`jstr: \u must be followed by 4 hex digits`)
	ErrSurrogate     = errors.New("jstr: invalid UTF-16 surrogate pair")
	ErrUTF8          = errors.New("jstr: invalid UTF-8")
)

// Error describes a failure at byte offset Off (the opening quote is 0).
type Error struct {
	Kind error
	Off  int
}

func (e *Error) Error() string { return fmt.Sprintf("%v at offset %d", e.Kind, e.Off) }
func (e *Error) Unwrap() error { return e.Kind }

var inspected atomic.Int64

// Inspected reports the total number of input bytes examined so far.
func Inspected() int64 { return inspected.Load() }

// ResetInspected zeroes the counter reported by Inspected.
func ResetInspected() { inspected.Store(0) }

const ( // decoder states
	stOpen = iota
	stText
	stEsc
	stHex
	stPairSlash
	stPairU
	stUTF8
	stDone
)

// Decoder incrementally decodes one JSON string literal fed via Write.
type Decoder struct {
	st, off, escOff, nhex, nseq, need, lead int
	out                                     []byte
	hi, hex                                 uint16
	seq                                     [4]byte
	pair, done                              bool
	err                                     error
}

// Write feeds the next chunk of the literal. Results and errors are
// identical for any chunk splitting.
func (d *Decoder) Write(p []byte) error {
	if d.err != nil {
		return d.err
	}
	if d.done {
		return &Error{ErrTrailing, d.off}
	}
	for _, b := range p {
		inspected.Add(1)
		if err := d.step(b); err != nil {
			d.err = err
			return err
		}
	}
	return nil
}

// Close finishes decoding and returns the decoded string.
func (d *Decoder) Close() (string, error) {
	if d.err != nil {
		return "", d.err
	}
	switch d.st {
	case stDone:
		d.done = true
		return string(d.out), nil
	case stOpen:
		d.err = &Error{ErrOpenQuote, 0}
	default:
		d.err = &Error{ErrUnterminated, d.off}
	}
	return "", d.err
}

// Decode strictly decodes one complete JSON string literal.
func Decode(lit []byte) (string, error) {
	d := &Decoder{st: stOpen}
	if err := d.Write(lit); err != nil {
		return "", err
	}
	return d.Close()
}

func (d *Decoder) step(b byte) error {
	off := d.off
	d.off++
	switch d.st {
	case stOpen:
		if b != '"' {
			return &Error{ErrOpenQuote, off}
		}
		d.st = stText
	case stText:
		return d.text(b, off)
	case stEsc:
		if c, ok := esc.Simple(b); ok {
			d.out = append(d.out, c)
			d.st = stText
		} else if b == 'u' {
			d.hex, d.nhex = 0, 0
			d.st = stHex
		} else {
			return &Error{ErrUnknownEscape, off}
		}
	case stHex:
		v, ok := esc.HexDigit(b)
		if !ok {
			return &Error{ErrShortHex, off}
		}
		d.hex = d.hex<<4 | v
		d.nhex++
		if d.nhex == 4 {
			return d.unit()
		}
	case stPairSlash:
		if b != '\\' {
			return &Error{ErrSurrogate, off}
		}
		d.escOff = off
		d.st = stPairU
	case stPairU:
		if b != 'u' {
			return &Error{ErrSurrogate, off}
		}
		d.hex, d.nhex, d.pair = 0, 0, true
		d.st = stHex
	case stUTF8:
		d.seq[d.nseq] = b
		d.nseq++
		if d.nseq == d.need {
			r, size := utf8.DecodeRune(d.seq[:d.nseq])
			inspected.Add(int64(d.nseq))
			if r == utf8.RuneError && size == 1 {
				return &Error{ErrUTF8, d.lead}
			}
			d.out = append(d.out, d.seq[:d.nseq]...)
			d.st = stText
		}
	case stDone:
		return &Error{ErrTrailing, off}
	}
	return nil
}

// unit handles a completed \uXXXX code unit.
func (d *Decoder) unit() error {
	u := d.hex
	d.st = stText
	if !d.pair {
		switch {
		case esc.IsHigh(u):
			d.hi = u
			d.st = stPairSlash
		case esc.IsLow(u):
			return &Error{ErrSurrogate, d.escOff}
		default:
			d.out = utf8.AppendRune(d.out, rune(u))
		}
		return nil
	}
	d.pair = false
	r, ok := esc.Combine(d.hi, u)
	if !ok {
		return &Error{ErrSurrogate, d.escOff}
	}
	d.out = utf8.AppendRune(d.out, r)
	return nil
}

func (d *Decoder) text(b byte, off int) error {
	switch {
	case b == '"':
		d.st = stDone
	case b == '\\':
		d.escOff, d.pair, d.st = off, false, stEsc
	case b < 0x20:
		return &Error{ErrControl, off}
	case b < 0x80:
		d.out = append(d.out, b)
	case b >= 0xC2 && b <= 0xDF:
		d.lead, d.need, d.nseq, d.seq[0], d.st = off, 2, 1, b, stUTF8
	case b >= 0xE0 && b <= 0xEF:
		d.lead, d.need, d.nseq, d.seq[0], d.st = off, 3, 1, b, stUTF8
	case b >= 0xF0 && b <= 0xF4:
		d.lead, d.need, d.nseq, d.seq[0], d.st = off, 4, 1, b, stUTF8
	default:
		return &Error{ErrUTF8, off}
	}
	return nil
}

// Encode minimally escapes s into a JSON string literal: only '"', '\'
// and U+0000–U+001F are escaped (short forms first, else \u00xx).
// It fails with *Error wrapping ErrUTF8 if s is not valid UTF-8.
func Encode(s string) ([]byte, error) {
	out := make([]byte, 0, len(s)+2)
	out = append(out, '"')
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			return nil, &Error{ErrUTF8, i}
		}
		i += size
		switch {
		case r == '"' || r == '\\':
			out = append(out, '\\', byte(r))
		case r >= 0x20:
			out = append(out, s[i-size:i]...)
		default:
			if c, ok := esc.ShortOf(byte(r)); ok {
				out = append(out, '\\', c)
			} else {
				out = fmt.Appendf(out, `\u%04x`, r)
			}
		}
	}
	return append(out, '"'), nil
}
