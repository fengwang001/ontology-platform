// Package qp provides a Quoted-Printable (RFC 2045) encoder and a strict
// streaming decoder, built on the single-line decisions in package qpline.
package qp

import (
	"errors"

	"ontology/qpline"
)

// Encode applies Quoted-Printable encoding to src.
func Encode(src []byte) []byte {
	dst, _ := EncodeChecked(src)
	return dst
}

// EncodeChecked is Encode plus the count of input bytes examined by the line
// encoder; see DESIGN.md for the bound checks <= 2*len(src).
func EncodeChecked(src []byte) ([]byte, int64) {
	out := make([]byte, 0, len(src)+len(src)/3)
	var n int64
	for start := 0; ; {
		end := start
		for end < len(src) && src[end] != '\n' {
			end++
		}
		seg := src[start:end]
		if len(seg) > 0 && seg[len(seg)-1] == '\r' {
			seg = seg[:len(seg)-1] // CR belongs to a CRLF ending
		}
		out = append(out, qpline.EncodeLine(seg, end == len(src), &n)...)
		if end == len(src) {
			return out, n
		}
		out = append(out, '\r', '\n')
		start = end + 1
	}
}

var (
	ErrBadEscape      = errors.New("quotedprintable: '=' not followed by two hex digits")
	ErrUnexpectedEOF = errors.New("quotedprintable: dangling escape, soft break or carriage return")
	ErrLineTooLong    = errors.New("quotedprintable: line exceeds 76 characters")
	ErrInvalidByte    = errors.New("quotedprintable: unescaped byte outside printable range")
	ErrTrailingSpace  = errors.New("quotedprintable: unescaped whitespace at end of line")
)

// DecodeError wraps a sentinel with the absolute input offset (from the first
// Write) where the malformed token starts.
type DecodeError struct {
	Err    error
	Offset int
}

func (e *DecodeError) Error() string { return e.Err.Error() }
func (e *DecodeError) Unwrap() error { return e.Err }

const (
	stNormal = iota
	stEq     // saw '='
	stEqHex  // '=' + one hex digit
	stEqCR   // "=\r"
	stCR     // bare CR starting a hard break
)

// Decoder is a strict streaming decoder.
type Decoder struct {
	out                       []byte
	state                     int
	hi                        byte
	eqOff, off, col, wsStart  int
	err                       error
}

// NewDecoder returns an empty strict decoder.
func NewDecoder() *Decoder { return &Decoder{eqOff: -1, wsStart: -1} }

// Output returns all decoded bytes accumulated so far.
func (d *Decoder) Output() []byte { return d.out }

func (d *Decoder) fail(at int, err error) error {
	if d.err == nil {
		d.err = &DecodeError{err, at}
	}
	return d.err
}

func (d *Decoder) emit(at int, b byte, n int) error {
	if d.col += n; d.col > qpline.MaxLine {
		return ErrLineTooLong
	}
	d.out = append(d.out, b)
	if (b == ' ' || b == '\t') && d.wsStart < 0 {
		d.wsStart = at
	} else if b != ' ' && b != '\t' {
		d.wsStart = -1
	}
	return nil
}

func (d *Decoder) hardBreak() error {
	if d.wsStart >= 0 {
		return ErrTrailingSpace
	}
	d.out, d.col = append(d.out, '\r', '\n'), 0
	return nil
}

// Write feeds another chunk. Offsets in DecodeError count from the first byte
// ever written, so every split of one stream gives identical output and errors.
func (d *Decoder) Write(p []byte) (int, error) {
	if d.err != nil {
		return 0, d.err
	}
	for i, b := range p {
		at := d.off + i
		switch d.state {
		case stEq:
			if v, ok := qpline.HexVal(b); ok {
				d.hi, d.state = v, stEqHex
			} else if b == '\r' {
				d.state = stEqCR
			} else if b == '\n' {
				d.col, d.state, d.eqOff = 0, stNormal, -1
			} else {
				return 0, d.fail(d.eqOff, ErrBadEscape)
			}
		case stEqHex:
			lo, ok := qpline.HexVal(b)
			if !ok {
				return 0, d.fail(d.eqOff, ErrBadEscape)
			}
			if err := d.emit(d.eqOff, d.hi<<4|lo, 3); err != nil {
				return 0, d.fail(d.eqOff, err)
			}
			d.state, d.eqOff = stNormal, -1
		case stEqCR:
			if b != '\n' {
				return 0, d.fail(d.eqOff, ErrUnexpectedEOF)
			}
			d.col, d.state, d.eqOff = 0, stNormal, -1
		case stCR:
			if b != '\n' {
				return 0, d.fail(at-1, ErrUnexpectedEOF)
			}
			d.state = stNormal
		default:
			if err := d.normalByte(at, b); err != nil {
				return 0, err
			}
		}
	}
	d.off += len(p)
	return len(p), nil
}

func (d *Decoder) normalByte(at int, b byte) error {
	switch {
	case b == '=':
		d.state, d.eqOff = stEq, at
	case b == '\r':
		if err := d.hardBreak(); err != nil {
			return d.fail(d.wsStart, err)
		}
		d.state = stCR
	case b == '\n':
		if err := d.hardBreak(); err != nil {
			return d.fail(d.wsStart, err)
		}
	default:
		if b != ' ' && b != '\t' && !(b >= 33 && b <= 126) {
			return d.fail(at, ErrInvalidByte)
		}
		if err := d.emit(at, b, 1); err != nil {
			return d.fail(at, err)
		}
	}
	return nil
}

// Close signals end of input and reports dangling escapes/trailing whitespace.
func (d *Decoder) Close() error {
	if d.err != nil {
		return d.err
	}
	switch d.state {
	case stEq, stEqHex, stEqCR:
		return d.fail(d.eqOff, ErrUnexpectedEOF)
	case stCR:
		return d.fail(d.off, ErrUnexpectedEOF)
	}
	if d.wsStart >= 0 {
		return d.fail(d.wsStart, ErrTrailingSpace)
}
	return nil
}

// Decode decodes all of src in one call.
func Decode(src []byte) ([]byte, error) {
	d := NewDecoder()
	if _, err := d.Write(src); err != nil {
		return nil, err
	}
	if err := d.Close(); err != nil {
		return nil, err
	}
	return d.Output(), nil
}
