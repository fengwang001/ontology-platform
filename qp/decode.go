package qp

import (
	"errors"

	"ontology/qpline"
)

// Sentinel causes carried by *DecodeError; distinguish with errors.Is.
var (
	ErrBadEscape       = errors.New("quotedprintable: '=' not followed by two hex digits or CRLF")
	ErrTruncatedEscape = errors.New("quotedprintable: dangling '=' at end of input")
	ErrLineTooLong     = errors.New("quotedprintable: encoded line exceeds 76 characters")
	ErrInvalidByte     = errors.New("quotedprintable: unescaped byte outside printable ASCII")
	ErrTrailingSpace   = errors.New("quotedprintable: unescaped trailing whitespace")
)

// DecodeError pairs a sentinel cause with the absolute input offset.
type DecodeError struct {
	Cause  error
	Offset int
}

func (e *DecodeError) Error() string { return e.Cause.Error() }
func (e *DecodeError) Unwrap() error { return e.Cause }

// Decoder is a strict, resumable quoted-printable decoder. Feed it any
// way (even one byte at a time); output and errors are independent of
// write boundaries.
type Decoder struct {
	out     []byte
	col     int  // encoded chars on the current line (soft breaks reset)
	state   byte // 0 normal, 1 '=', 2 '=X', 3 '=\r', 4 normal CR
	hi      byte // first hex-digit value
	tailOff int  // offset of latest raw space/tab, or -1
	softCol int  // column just before a soft-break '='
	total   int  // absolute bytes consumed
	err     *DecodeError
}

// NewDecoder returns a ready strict decoder.
func NewDecoder() *Decoder { return &Decoder{tailOff: -1} }

func (d *Decoder) fail(off int, cause error) error {
	if d.err == nil {
		d.err = &DecodeError{Cause: cause, Offset: off}
	}
	return d.err
}

func hexVal(b byte) (byte, bool) {
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

// Write consumes a chunk. The decoder stays dead after its first error.
func (d *Decoder) Write(p []byte) (int, error) {
	for n, b := range p {
		if d.err != nil {
			return n, d.err
		}
		d.step(d.total, b)
		d.total++
		if d.err != nil {
			return n + 1, d.err
		}
	}
	return len(p), nil
}

func (d *Decoder) step(off int, b byte) {
	switch d.state {
	case 1:
		if v, ok := hexVal(b); ok {
			d.hi, d.state, d.col = v, 2, d.col+1
			return
		}
		if b == '\r' {
			d.softCol, d.state = d.col, 3
			return
		}
		d.fail(off, ErrBadEscape)
	case 2:
		v, ok := hexVal(b)
		if !ok {
			d.fail(off, ErrBadEscape)
			return
		}
		d.emitByte(d.hi<<4 | v)
		d.state, d.col = 0, d.col+2
	case 3: // "=\r"
		if b != '\n' {
			d.fail(off, ErrBadEscape)
			return
		}
		// Encoder breaks only when the next 3-char token would overflow
		// (col 74 +3 > 76); at col 75 a one-byte token always fits.
		if d.softCol+3 <= qpline.MaxLineLen {
			d.fail(off, ErrBadEscape)
			return
		}
		d.state, d.col = 0, 0
	case 4: // normal CR
		if b != '\n' {
			d.fail(off-1, ErrInvalidByte)
			return
		}
		if d.tailOff >= 0 {
			d.fail(d.tailOff, ErrTrailingSpace)
			return
		}
		d.out = append(d.out, '\r', '\n')
		d.state, d.col, d.tailOff = 0, 0, -1
	default:
		d.normal(off, b)
	}
}

func (d *Decoder) normal(off int, b byte) {
	switch {
	case b == '=':
		d.checkCol(off)
		d.col++
		d.state = 1
	case b == '\r':
		d.checkCol(off)
		d.col++
		d.state = 4
	case b == '\n':
		d.fail(off, ErrInvalidByte)
	case b == ' ' || b == '\t':
		d.checkCol(off)
		d.col++
		d.out = append(d.out, b)
		d.tailOff = off
	case b >= 33 && b <= 126:
		d.checkCol(off)
		d.col++
		d.emitByte(b)
	default:
		d.fail(off, ErrInvalidByte)
	}
}

func (d *Decoder) checkCol(off int) {
	if d.col+1 > qpline.MaxLineLen {
		d.fail(off, ErrLineTooLong)
}

func (d *Decoder) emitByte(b byte) {
	d.out = append(d.out, b)
	if b != ' ' && b != '\t' {
		d.tailOff = -1
	}
}

// Close finalizes the stream: dangling escape or unescaped trailing
// whitespace before EOF is an error.
func (d *Decoder) Close() error {
	if d.err != nil {
		return d.err
	}
	switch d.state {
	case 1, 2:
		return d.fail(d.total-1, ErrTruncatedEscape)
	case 3, 4:
		return d.fail(d.total-1, ErrBadEscape)
	}
	if d.tailOff >= 0 {
		return d.fail(d.tailOff, ErrTrailingSpace)
	}
	return nil
}

// Output returns all bytes decoded so far.
func (d *Decoder) Output() []byte { return d.out }

// Decode fully decodes src with a fresh Decoder.
func Decode(src []byte) ([]byte, error) {
	d := NewDecoder()
	if _, err := d.Write(src); err != nil {
		return d.Output(), err
	}
	if err := d.Close(); err != nil {
		return d.Output(), err
	}
	return d.Output(), nil
}
