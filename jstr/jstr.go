// Package jstr strictly decodes and minimally encodes JSON string literals.
package jstr

import (
	"errors"
	"fmt"
	"sync/atomic"
	"unicode/utf8"

	"ontology/esc"
)

var ErrEscape, ErrHex, ErrSurrogate = esc.ErrEscape, esc.ErrHex, esc.ErrSurrogate

var (
	ErrControl      = errors.New("unescaped control character")
	ErrUnterminated = errors.New("missing closing quote")
	ErrTrailing     = errors.New("bytes after closing quote")
	ErrUTF8         = errors.New("invalid UTF-8")
)

// Error is a decode/encode failure at byte offset Off; Err is a sentinel.
type Error struct {
	Err error
	Off int
}

func (e *Error) Error() string { return fmt.Sprintf("jstr: %v (offset %d)", e.Err, e.Off) }
func (e *Error) Unwrap() error { return e.Err }

var checked atomic.Int64

// Checked reports the total number of input bytes examined so far.
func Checked() int64 { return checked.Load() }

const ( // decoder states
	stOpen = iota
	stBody
	stEsc // inside an escape sequence
	stDone
)

// Decoder is a streaming strict decoder for one JSON string literal.
type Decoder struct {
	st, pos, escAt int
	seq            esc.Seq
	utf            esc.UTF8
	out            []byte
}

// Write feeds one chunk; input may be split at any byte boundary.
func (d *Decoder) Write(p []byte) error {
	for _, b := range p {
		checked.Add(1)
		if err := d.step(b); err != nil {
			return err
		}
		d.pos++
	}
	return nil
}
func (d *Decoder) step(b byte) error {
	switch d.st {
	case stOpen:
		if b != '"' {
			return &Error{ErrUnterminated, d.pos}
		}
		d.st = stBody
	case stDone:
		return &Error{ErrTrailing, d.pos}
	case stEsc:
		r, done, off, err := d.seq.Feed(b)
		if err != nil {
			return &Error{err, d.escAt + off}
		}
		if done {
			d.out = append(d.out, string(r)...)
			d.st = stBody
		}
	default: // stBody
		if d.utf.Need > 0 {
			if !d.utf.Feed(b) {
				return &Error{ErrUTF8, d.pos}
			}
			d.out = append(d.out, b)
			return nil
		}
		switch {
		case b == '"':
			d.st = stDone
		case b == '\\':
			d.seq, d.escAt, d.st = esc.Seq{}, d.pos+1, stEsc
		case b < 0x20:
			return &Error{ErrControl, d.pos}
		case b < 0x80:
			d.out = append(d.out, b)
		default:
			u, ok := esc.Lead(b)
			if !ok {
				return &Error{ErrUTF8, d.pos}
			}
			d.utf = u
			d.out = append(d.out, b)
		}
	}
	return nil
}

// Close ends the stream and returns the decoded string.
func (d *Decoder) Close() (string, error) {
	if d.st == stBody && d.utf.Need > 0 {
		return "", &Error{ErrUTF8, d.pos}
	}
	if d.st != stDone {
		return "", &Error{ErrUnterminated, d.pos}
	}
	return string(d.out), nil
}

// Decode strictly decodes lit, a JSON string literal including both quotes.
func Decode(lit []byte) (string, error) {
	d := new(Decoder)
	if err := d.Write(lit); err != nil {
		return "", err
	}
	return d.Close()
}

// Encode minimally encodes s; invalid UTF-8 in s fails with ErrUTF8.
func Encode(s string) ([]byte, error) {
	for i := 0; i < len(s); {
		r, w := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && w == 1 {
			return nil, &Error{ErrUTF8, i}
		}
		i += w
	}
	out := []byte{'"'}
	for i := 0; i < len(s); i++ {
		switch b := s[i]; {
		case b == '"' || b == '\\':
			out = append(out, '\\', b)
		case b < 0x20:
			out = append(out, esc.Short(b)...)
		default:
			out = append(out, b)
		}
	}
	return append(out, '"'), nil
}
