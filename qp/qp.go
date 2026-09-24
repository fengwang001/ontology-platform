// Package qp implements Quoted-Printable (RFC 2045) encoding and
// strict decoding. Encode maps every input newline ("\n" or "\r\n")
// to "\r\n"; a lone '\r' is an ordinary byte and is escaped. The
// round-trip equation is Decode(Encode(x)) == norm(x), where norm
// replaces every "\r\n" and every lone "\n" with "\r\n".
package qp

import (
	"errors"
	"fmt"

	"ontology/qpline"
)

// checked counts input-byte inspections performed by Encode.
var checked int64

// Checked reports how many input-byte inspections Encode has done.
func Checked() int64 { return checked }

// Encode encodes src as Quoted-Printable; see the package doc for
// the exact newline semantics. It never inspects an input byte more
// than twice (one scan for newlines, one token pass per line).
func Encode(src []byte) []byte {
	checked += int64(len(src)) // pass 1: scan for newlines
	var dst []byte
	start := 0
	for i := 0; i < len(src); i++ {
		if src[i] != '\n' {
			continue
		}
		line := src[start:i]
		if n := len(line); n > 0 && line[n-1] == '\r' {
			line = line[:n-1]
		}
		dst = encodeLine(dst, line)
		dst = append(dst, '\r', '\n')
		start = i + 1
	}
	return encodeLine(dst, src[start:])
}

func encodeLine(dst, line []byte) []byte {
	checked += int64(len(line)) // pass 2: token emission
	var enc qpline.Encoder
	for i, b := range line {
		dst = enc.Feed(dst, b, i == len(line)-1)
	}
	return dst
}

// Error sentinels, wrapped in *DecodeError (use errors.Is).
var (
	ErrBadEscape          = errors.New("qp: = not followed by hex or CRLF")
	ErrUnexpectedEOF      = errors.New("qp: input ends inside an escape or CR")
	ErrLineTooLong        = errors.New("qp: line exceeds 76 characters")
	ErrInvalidByte        = errors.New("qp: unescaped non-printable byte")
	ErrTrailingWhitespace = errors.New("qp: unescaped whitespace at end of line")
	ErrBadSoftBreak       = errors.New("qp: unnecessary soft break")
	ErrClosed             = errors.New("qp: write after close")
)

// DecodeError reports a decoding failure at a byte offset.
type DecodeError struct {
	Err    error // one of the sentinels above
	Offset int64 // offset of the offending byte
}

func (e *DecodeError) Error() string { return fmt.Sprintf("qp: offset %d: %v", e.Offset, e.Err) }
func (e *DecodeError) Unwrap() error { return e.Err }

// Decoder states.
const (
	stNormal = iota // between bytes
	stEq            // saw '='
	stHex           // saw '=' and one hex digit
	stEqCR          // saw "=\r"
	stCR            // saw '\r'
)

// Decoder is a streaming strict Quoted-Printable decoder. The zero
// value is ready to use. Once a Write or Close fails, the error is
// sticky. Soft breaks must be necessary (greedy full lines), matching
// the encoder; lines may end only with CRLF or EOF.
type Decoder struct {
	out     []byte
	state   int
	col     int   // characters on the current line
	full    int   // content width before an unchecked soft break, +1
	pos     int64 // offset of the next byte
	eqAt    int64 // offset of the pending '='
	crAt    int64 // offset of the pending '\r'
	ws      int64 // offset+1 of a trailing literal blank, 0 if none
	fullAt  int64 // offset of the unchecked soft break's '='
	hi      byte  // pending hex nibble
	err     error
	closed  bool
}

// Write decodes p, stopping at the first error.
func (d *Decoder) Write(p []byte) (int, error) {
	if d.err != nil {
		return 0, d.err
	}
	if d.closed {
		return 0, ErrClosed
	}
	for i, b := range p {
		if d.step(b) != nil {
			return i, d.err
		}
	}
	return len(p), nil
}

// Close finishes decoding and reports truncation errors.
func (d *Decoder) Close() error {
	if d.err != nil {
		return d.err
	}
	d.closed = true
	switch {
	case d.state == stEq || d.state == stHex || d.state == stEqCR:
		return d.fail(ErrUnexpectedEOF, d.eqAt)
	case d.state == stCR:
		return d.fail(ErrInvalidByte, d.crAt)
	case d.ws > 0:
		return d.fail(ErrTrailingWhitespace, d.ws-1)
	case d.full > 0:
		return d.fail(ErrBadSoftBreak, d.fullAt)
	}
	return nil
}

// Output returns the bytes decoded so far.
func (d *Decoder) Output() []byte { return d.out }

func (d *Decoder) fail(err error, off int64) error {
	d.err = &DecodeError{Err: err, Offset: off}
	return d.err
}

func (d *Decoder) step(b byte) error {
	off := d.pos
	d.pos++
	switch d.state {
	case stEq:
		if v, ok := qpline.Unhex(b); ok {
			d.hi, d.state = v, stHex
			return d.grow(off)
		}
		if b == '\r' {
			d.state = stEqCR
			return nil
		}
		return d.fail(ErrBadEscape, d.eqAt)
	case stHex:
		if v, ok := qpline.Unhex(b); ok {
			d.state = stNormal
			if err := d.grow(off); err != nil {
				return err
			}
			return d.emit(d.hi<<4|v, 3)
		}
		return d.fail(ErrBadEscape, d.eqAt)
	case stEqCR:
		if b != '\n' {
			return d.fail(ErrBadEscape, d.eqAt)
		}
		return d.breakLine(true)
	case stCR:
		if b != '\n' {
			return d.fail(ErrInvalidByte, d.crAt)
		}
		return d.breakLine(false)
	}
	switch {
	case b == '=':
		d.eqAt, d.state = off, stEq
		return d.grow(off)
	case b == '\r':
		d.crAt, d.state = off, stCR
		return nil
	case b == '\n':
		return d.fail(ErrInvalidByte, off)
	case qpline.Blank(b):
		d.ws = off + 1
		return d.literal(b, off)
	case qpline.Printable(b):
		d.ws = 0
		return d.literal(b, off)
	default:
		return d.fail(ErrInvalidByte, off)
	}
}

func (d *Decoder) literal(b byte, off int64) error {
	if err := d.grow(off); err != nil {
		return err
	}
	return d.emit(b, 1)
}

// emit appends a decoded byte whose encoding is w columns wide.
func (d *Decoder) emit(b byte, w int) error {
	d.out = append(d.out, b)
	if d.full > 0 { // the next token proves the soft break necessary
		if d.full-1+w <= qpline.Max-1 {
			return d.fail(ErrBadSoftBreak, d.fullAt)
		}
		d.full = 0
	}
	return nil
}

func (d *Decoder) grow(off int64) error {
	d.col++
	if d.col > qpline.Max {
		return d.fail(ErrLineTooLong, off)
	}
	return nil
}

// breakLine handles a soft (=\r\n) or hard (\r\n) line break.
func (d *Decoder) breakLine(soft bool) error {
	if d.ws > 0 {
		return d.fail(ErrTrailingWhitespace, d.ws-1)
	}
	if d.full > 0 {
		return d.fail(ErrBadSoftBreak, d.fullAt)
	}
	if soft {
		d.full, d.fullAt = d.col, d.eqAt
	}
	d.col, d.ws, d.state = 0, 0, stNormal
	return nil
}
