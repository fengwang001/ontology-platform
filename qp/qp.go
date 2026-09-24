// Package qp provides a Quoted-Printable (RFC 2045) encoder and a strict
// streaming decoder. It depends only on qpline and the standard library.
package qp

import "ontology/qpline"

// Encode returns the Quoted-Printable encoding of src. Every input newline
// ('\n' or "\r\n") is emitted as hard CRLF; a bare '\r' is an ordinary byte
// and becomes =0D. Encoding is deterministic and minimal (see DESIGN.md).
func Encode(src []byte) ([]byte, int) {
	enc := &encoder{}
	return enc.encode(src)
}

type encoder struct {
	line    qpline.Encoder
	checked int // input bytes examined; each byte is examined at most twice
}

func (e *encoder) encode(src []byte) ([]byte, int) {
	for i := 0; i < len(src); i++ {
		e.checked++
		b := src[i]
		if b == '\n' {
			e.line.Newline()
			continue
		}
		if b == '\r' && i+1 < len(src) && src[i+1] == '\n' {
			e.checked++
			i++
			e.line.Newline()
			continue
		}
		e.line.Put(b)
	}
	e.line.End()
	return append([]byte(nil), e.line.Bytes()...), e.checked
}

// Decoder decodes Quoted-Printable data fed across arbitrary Write calls.
type Decoder struct {
	out    []byte
	state  int // 0 normal, 1 '=', 2 "=X", 3 "=\r"
	hi     byte
	col    int
	trail  int // bare space/tab count at the tail of the current physical line
	offset int
	eqPos  int
	err    error
}

// Output returns all bytes decoded so far.
func (d *Decoder) Output() []byte { return d.out }

// Write feeds one chunk; decoding is independent of chunk boundaries.
func (d *Decoder) Write(p []byte) (int, error) {
	for i := 0; i < len(p); i++ {
		abs := d.offset + i
		b := p[i]
		switch d.state {
		case 0:
			if b == '=' {
				if d.col+1 > 76 {
					d.fail(abs, &LineTooLongError{})
				}
				d.eqPos = abs
				d.state = 1
			} else {
				if b == '\r' || b == '\n' || b != ' ' && b != '\t' && (b < 33 || b > 126) {
					d.fail(abs, &IllegalByteError{Byte: b})
				}
				if d.col+1 > 76 {
					d.fail(abs, &LineTooLongError{})
				}
				d.out = append(d.out, b)
				d.col++
				if b == ' ' || b == '\t' {
					d.trail++
				} else {
					d.trail = 0
				}
			}
		case 1:
			if isHex(b) {
				d.hi, d.state = b, 2
			} else if b == '\r' {
				d.state = 3
			} else {
				d.fail(abs, &InvalidEscapeError{Byte: b})
			}
		case 2:
			if !isHex(b) {
				d.fail(abs, &InvalidEscapeError{Byte: b})
			}
			v := fromHex(d.hi)<<4 | fromHex(b)
			if d.col+3 > 76 {
				d.fail(abs, &LineTooLongError{})
			}
			d.out = append(d.out, v)
			d.col += 3
			d.trail = 0
			d.state = 0
		case 3:
			if b != '\n' {
				d.fail(abs, &InvalidEscapeError{Byte: b})
			}
			if d.trail > 0 {
				d.fail(abs-d.trail, &TrailingWhitespaceError{})
			}
			d.out = append(d.out, '\r', '\n')
			d.col, d.trail, d.state = 0, 0, 0
		}
		if d.err != nil {
			d.offset += i + 1
			return i + 1, d.err
		}
	}
	d.offset += len(p)
	return len(p), nil
}

// Close finalizes the stream, reporting dangling escapes or trailing space.
func (d *Decoder) Close() error {
	if d.err != nil {
		return d.err
	}
	off := d.offset
	switch d.state {
	case 1, 2:
		d.fail(d.eqPos, &DanglingEqualError{})
	case 3:
		d.fail(d.eqPos, &DanglingEqualError{})
	}
	if d.trail > 0 {
		d.fail(off-d.trail, &TrailingWhitespaceError{})
	}
	return d.err
}

func (d *Decoder) fail(off int, err error) {
	if named, ok := err.(interface{ setOffset(int) }); ok {
		named.setOffset(off)
	}
	d.err = err
}

func isHex(b byte) bool {
	return b >= '0' && b <= '9' || b >= 'A' && b <= 'F' || b >= 'a' && b <= 'f'
}

func fromHex(b byte) byte {
	switch {
	case b >= '0' && b <= '9':
		return b - '0'
	case b >= 'A' && b <= 'F':
		return b - 'A' + 10
	default:
		return b - 'a' + 10
	}
}

// Decode decodes src fully and strictly.
func Decode(src []byte) ([]byte, error) {
	d := &Decoder{}
	if _, err := d.Write(src); err != nil {
		return nil, err
	}
	if err := d.Close(); err != nil {
		return nil, err
	}
	return d.Output(), nil
}
