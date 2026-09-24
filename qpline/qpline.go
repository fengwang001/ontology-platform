// Package qpline implements single-line Quoted-Printable encoding decisions:
// which bytes need escaping, trailing-whitespace handling and soft-break
// insertion. It has no dependencies.
package qpline

const hexUpper = "0123456789ABCDEF"

// NeedEscape reports whether b must be emitted as =XX.
// b is 33..126 except '='. Space (0x20) and tab (0x09) are reportable here;
// the caller leaves them bare unless they are the last byte of a line.
func NeedEscape(b byte) bool {
	if b == '=' {
		return true
	}
	if b == ' ' || b == '\t' {
		return false
	}
	return b < 33 || b > 126
}

// Encoder builds one encoded stream line by line. Put receives every input
// byte in order; Newline terminates the current logical line; End flushes the
// final line. Trailing bare whitespace is rewritten to =20/=09 at line/input
// boundaries without rescanning earlier input.
type Encoder struct {
	buf   []byte
	col   int // columns used on the current physical line
	trail int // consecutive bare space/tab bytes at the tail of the line
}

// Reset restores the encoder to its initial state.
func (e *Encoder) Reset() {
	e.buf = e.buf[:0]
	e.col, e.trail = 0, 0
}

// Bytes returns all encoded output accumulated so far.
func (e *Encoder) Bytes() []byte { return e.buf }

// Put appends one non-newline input byte. A bare '\r' is an ordinary byte and
// is emitted as =0D; the caller recognizes "\r\n" and uses Newline instead.
func (e *Encoder) Put(b byte) {
	if b == ' ' || b == '\t' {
		e.buf = append(e.buf, b)
		e.col++
		e.trail++
		return
	}
	if NeedEscape(b) {
		e.emitEscaped(b)
	} else {
		e.emitBare(b)
	}
	e.trail = 0
}

// Newline rewrites any trailing whitespace and emits a hard CRLF.
func (e *Encoder) Newline() {
	e.finishTrail()
	e.buf = append(e.buf, '\r', '\n')
	e.col, e.trail = 0, 0
}

// End rewrites trailing whitespace on the final line (no CRLF appended).
func (e *Encoder) End() {
	e.finishTrail()
}

// finishTrail re-encodes trailing bare space/tab bytes so they survive
// transport at a hard line boundary or end of input.
func (e *Encoder) finishTrail() {
	for e.trail > 0 {
		b := e.buf[len(e.buf)-1]
		e.buf = e.buf[:len(e.buf)-1]
		e.col--
		e.trail--
		e.emitEscaped(b)
	}
	e.trail = 0
}

func (e *Encoder) softBreak() {
	e.buf = append(e.buf, '=', '\r', '\n')
	e.col = 0
	e.trail = 0
}

func (e *Encoder) emitBare(b byte) {
	if e.col+1 > 75 {
		e.softBreak()
	}
	e.buf = append(e.buf, b)
	e.col++
}

func (e *Encoder) emitEscaped(b byte) {
	if e.col+3 > 75 {
		e.softBreak()
	}
	e.buf = append(e.buf, '=', hexUpper[b>>4], hexUpper[b&0x0f])
	e.col += 3
}
