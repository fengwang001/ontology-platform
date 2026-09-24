// Package qp implements an RFC 2045 Quoted-Printable encoder and strict decoder.
package qp

import (
	"errors"

	"ontology/qpline"
)

// DecodeError wraps a sentinel error with the absolute input byte offset.
type DecodeError struct {
	Err    error
	Offset int
}

func (e *DecodeError) Error() string { return e.Err.Error() }
func (e *DecodeError) Unwrap() error { return e.Err }

// Sentinel errors distinguish the failure modes; use errors.Is.
var (
	ErrBadEscape          = errors.New("qp: '=' not followed by two hex digits")
	ErrUnexpectedEOF      = errors.New("qp: truncated '=' escape at end of input")
	ErrTrailingEquals     = errors.New("qp: dangling '=' at end of input")
	ErrLineTooLong        = errors.New("qp: encoded line exceeds 76 characters")
	ErrInvalidByte        = errors.New("qp: unescaped byte outside printable ASCII")
	ErrTrailingWhitespace = errors.New("qp: unescaped whitespace at end of line")
	ErrBadLineEnding      = errors.New("qp: bare CR or invalid line ending")
)

// Decode strictly decodes src in one shot.
func Decode(src []byte) ([]byte, error) {
	d := NewDecoder()
	if _, err := d.Write(src); err != nil {
		return nil, err
	}
	return d.Output(), d.Close()
}

// Decoder is a streaming strict decoder. Output and error are identical for
// every chunking of the input, including boundaries inside =XX or =\r\n.
type Decoder struct {
	out                            []byte
	offset, col, trailing, wsStart int
	state                          int
	hi                             byte
}

const (
	stNormal = iota
	stEquals
	stEqHex
	stEqCR
	stCR
)

// NewDecoder returns a streaming decoder.
func NewDecoder() *Decoder { return &Decoder{} }

// Write feeds encoded bytes and returns the number consumed.
func (d *Decoder) Write(p []byte) (int, error) {
	for i := range p {
		if err := d.writeByte(p[i]); err != nil {
			return i, err
		}
		d.offset++
	}
	return len(p), nil
}

func (d *Decoder) Output() []byte              { return d.out }
func (d *Decoder) fail(e error) error          { return &DecodeError{e, d.offset} }
func (d *Decoder) failAt(e error, o int) error { return &DecodeError{e, o} }

func (d *Decoder) step() error {
	if d.col++; d.col > qpline.MaxLen {
		return d.fail(ErrLineTooLong)
	}
	return nil
}

func (d *Decoder) raw(b byte) error {
	if err := d.step(); err != nil {
		return err
	}
	if b == ' ' || b == '\t' {
		if d.trailing == 0 {
			d.wsStart = d.offset
		}
		d.trailing++
	} else {
		d.trailing = 0
	}
	d.out = append(d.out, b)
	return nil
}

func isHex(b byte) (byte, bool) {
	switch {
	case b >= '0' && b <= '9':
		return b - '0', true
	case b >= 'A' && b <= 'F':
		return b - 'A' + 10, true
	case b >= 'a' && b <= 'f':
		return b - 'a' + 10, true
	}
	return 0, false
}

// Close signals end of input and validates any pending escape.
func (d *Decoder) Close() error {
	switch d.state {
	case stEquals:
		return d.failAt(ErrTrailingEquals, d.offset-1)
	case stEqHex:
		return d.failAt(ErrUnexpectedEOF, d.offset-1)
	case stEqCR, stCR:
		return d.failAt(ErrBadLineEnding, d.offset-1)
	}
	if d.trailing > 0 {
		return d.failAt(ErrTrailingWhitespace, d.wsStart)
	}
	return nil
}
