package qp

import "ontology/qpline"

// Encode returns the Quoted-Printable encoding of src. A final line without a
// trailing newline is emitted without a trailing CRLF.
func Encode(src []byte) []byte { out, _ := EncodeCount(src); return out }

// EncodeCount behaves like Encode and reports the total number of input byte
// inspections made by the encoder (at most twice the input length).
func EncodeCount(src []byte) ([]byte, int) {
	e := &encoder{}
	out := make([]byte, 0, len(src))
	for start := 0; start <= len(src); {
		end := start
		for end < len(src) {
			e.n++
			if src[end] == '\n' {
				break
			}
			end++
		}
		line := src[start:end]
		if end < len(src) && end > start && src[end-1] == '\r' {
			line = src[start : end-1]
		}
		out = e.appendLine(out, line)
		if end == len(src) {
			break
		}
		out = append(out, '\r', '\n')
		start = end + 1
	}
	return out, e.n
}

type encoder struct{ n int }

func (e *encoder) appendLine(dst, line []byte) []byte {
	first := len(line) // start of the trailing-whitespace run
	for first > 0 && (line[first-1] == ' ' || line[first-1] == '\t') {
		first--
	}
	col := 0
	for i, b := range line {
		e.n++
		w := qpline.Width(b)
		if i >= first && (b == ' ' || b == '\t') {
			w = 3
		}
		// A continued line reserves its last slot for the soft-break '='.
		if col+w > qpline.MaxLen || (col+w == qpline.MaxLen && i+1 < len(line)) {
			dst = qpline.AppendSoftBreak(dst)
			col = 0
		}
		if w == 3 {
			dst = qpline.AppendEscaped(dst, b)
		} else {
			dst = qpline.AppendContent(dst, b)
		}
		col += w
	}
	return dst
}

func (d *Decoder) writeByte(b byte) error {
	switch d.state {
	case stEquals:
		if b == '\r' {
			d.state = stEqCR
			return nil
		}
		v, ok := isHex(b)
		if !ok {
			return d.failAt(ErrBadEscape, d.offset-1)
		}
		d.hi, d.state = v, stEqHex
	case stEqHex:
		v, ok := isHex(b)
		if !ok {
			return d.failAt(ErrBadEscape, d.offset-2)
		}
		for range 3 {
			if err := d.step(); err != nil {
				return err
			}
		}
		d.trailing, d.state = 0, stNormal
		d.out = append(d.out, d.hi<<4|v)
	case stEqCR:
		if b != '\n' {
			return d.failAt(ErrBadEscape, d.offset-2)
		}
		d.col, d.trailing, d.state = 0, 0, stNormal
	case stCR:
		if b != '\n' {
			return d.failAt(ErrBadLineEnding, d.offset-1)
		}
		if d.trailing > 0 {
			return d.failAt(ErrTrailingWhitespace, d.wsStart)
		}
		d.out = append(d.out, '\r', '\n')
		d.col, d.trailing, d.state = 0, 0, stNormal
	default:
		switch {
		case b == '=':
			d.trailing, d.state = 0, stEquals
		case b == '\r':
			d.state = stCR
		case b == '\n':
			return d.fail(ErrBadLineEnding)
		case b == ' ' || b == '\t':
			return d.raw(b)
		case b < 33 || b > 126:
			return d.fail(ErrInvalidByte)
		default:
			return d.raw(b)
		}
	}
	return nil
}
