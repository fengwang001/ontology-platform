// Package qp provides a Quoted-Printable (RFC 2045) encoder and a strict
// streaming decoder. It depends on package qpline for per-line decisions.
package qp

import "ontology/qpline"

// Encode returns the full Quoted-Printable encoding of src.
func Encode(src []byte) []byte {
	return NewEncoder().Encode(src)
}

// Encoder encodes whole byte slices. Checks counts every input byte
// examined (see DESIGN.md: bounded by 2*len(src)).
type Encoder struct{ Checks int }

// NewEncoder creates an Encoder with a zeroed inspection counter.
func NewEncoder() *Encoder { return &Encoder{} }

// Encode appends the QP encoding of src to out and returns the result.
func (e *Encoder) Encode(src []byte) []byte {
	out := make([]byte, 0, len(src)+len(src)/20+8)
	mark := func() { e.Checks++ }
	for i := 0; i < len(src); {
		e.Checks++
		if n := qpline.NewlineLen(src, i); n > 0 {
			out = append(out, '\r', '\n')
			i += n
			continue
		}
		j := i
		for j < len(src) && qpline.NewlineLen(src, j) == 0 {
			j++
		}
		out = qpline.EncodeLine(out, src[i:j], mark)
		i = j
	}
	return out
}

// Decoder is a strict streaming Quoted-Printable decoder.
type Decoder struct {
	out   []byte
	cons  int
	state int
	hi    byte
	col   int
	wsOff int
	ws    byte
	err   error
}

// NewDecoder creates a fresh Decoder.
func NewDecoder() *Decoder { return &Decoder{} }

const (
	stData  = 0
	stEq    = 1
	stHex   = 2
	stCR    = 3
	stEqCR  = 4
	stEqLF  = 5
)

// Write feeds encoded bytes and returns the decoded prefix.
func (d *Decoder) Write(src []byte) []byte {
	start := len(d.out)
	for k, b := range src {
		off := d.cons + k
		if d.err != nil {
			d.cons += len(src) - k
			return d.out[start:]
		}
		switch d.state {
		case stData:
			d.dataByte(b, off)
		case stEq:
			d.afterEq(b, off)
		case stHex:
			d.afterHi(b, off)
		case stCR:
			if b == '\n' {
				d.hardBreak(off)
				d.state = stData
			} else {
				d.fail(ErrUnprintable, off-1)
			}
		case stEqCR:
			if b == '\n' {
				d.softBreak(off)
				d.state = stData
			} else {
				d.fail(ErrSoftLineBreak, off-1)
			}
		case stEqLF:
			d.softBreak(off - 1)
			d.dataByte(b, off)
		}
	}
	d.cons += len(src)
	return d.out[start:]
}

func (d *Decoder) dataByte(b byte, off int) {
	switch b {
	case '=':
		d.state = stEq
		d.col++
	case '\r':
		d.state = stCR
	case '\n':
		d.hardBreak(off)
	default:
		if b == ' ' || b == '\t' {
			if d.wsOff == -1 {
				d.wsOff, d.ws = off, b
			}
			d.col++
			return
		}
		d.wsOff = -1
		if b < 33 || b > 126 {
			d.fail(ErrUnprintable, off)
			return
		}
		d.col++
		d.out = append(d.out, b)
	}
}

func (d *Decoder) afterEq(b byte, off int) {
	switch {
	case b == '\r':
		d.state = stEqCR
	case b == '\n':
		d.state = stEqLF
	case isHex(b):
		d.hi = b
		d.state = stHex
	default:
		d.fail(ErrBadEscape, off)
	}
}

func (d *Decoder) afterHi(b byte, off int) {
	if !isHex(b) {
		d.fail(ErrBadEscape, off)
		return
	}
	v := fromHex(d.hi)<<4 | fromHex(b)
	d.out = append(d.out, v)
	d.wsOff = -1
	d.state = stData
}

func (d *Decoder) hardBreak(off int) {
	if d.wsOff >= 0 {
		d.fail(ErrTrailingSpace, d.wsOff)
		return
	}
	if d.col > qpline.LineLimit {
		d.fail(ErrLineTooLong, off-d.col)
		return
	}
	d.col = 0
	d.out = append(d.out, '\r', '\n')
}

func (d *Decoder) softBreak(_ int) {
	if d.col-1 > qpline.LineLimit {
		d.fail(ErrLineTooLong, -1)
		return
	}
	d.col = 0
	d.wsOff = -1
}

// Close flushes the stream and reports any trailing error.
func (d *Decoder) Close() error {
	if d.err != nil {
		return d.err
	}
	switch d.state {
	case stEq, stHex:
		d.fail(ErrDanglingEscape, d.cons)
	case stCR:
		d.fail(ErrUnprintable, d.cons-1)
	case stEqCR:
		d.fail(ErrSoftLineBreak, d.cons-1)
	}
	if d.wsOff >= 0 {
		d.fail(ErrTrailingSpace, d.wsOff)
}
	return d.err
}

// Output returns the full decoded output accumulated so far.
func (d *Decoder) Output() []byte { return d.out }

// Err returns the first decoder error, or nil.
func (d *Decoder) Err() error { return d.err }

func (d *Decoder) fail(s error, off int) {
	if d.err == nil {
		d.err = &Error{Cause: s, Offset: off}
	}
}

func isHex(b byte) bool {
	return b >= '0' && b <= '9' || b >= 'A' && b <= 'F' || b >= 'a' && b <= 'f'
}

func fromHex(b byte) byte {
	switch {
	case b >= '0' && b <= '9':
		return b - '0'
	case b >= 'a' && b <= 'f':
		return b - 'a' + 10
	default:
		return b - 'A' + 10
	}
}

// Decode is a convenience wrapper decoding src in one shot.
func Decode(src []byte) ([]byte, error) {
	d := NewDecoder()
	_ = d.Write(src)
	if err := d.Close(); err != nil {
		return nil, err
	}
	return d.Output(), nil
}
