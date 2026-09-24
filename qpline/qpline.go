// Package qpline makes the single-line layout decisions for
// Quoted-Printable (RFC 2045) encoding: which bytes must be escaped,
// how trailing whitespace is handled, and where soft breaks go.
package qpline

// MaxLen is the maximum line length in characters, excluding the CRLF
// but including the '=' of a soft break.
const MaxLen = 76

const hexdig = "0123456789ABCDEF"

// Encoder lays out encoded output line by line. A trailing space or tab
// is held back as "pending" until the next byte decides whether the
// whitespace sits at a line end (escaped) or not (literal).
type Encoder struct {
	out   []byte
	col   int  // characters on the current line
	ws    byte // pending space/tab, 0 if none
	lastW int  // width of the last emitted unit (1 or 3)
}

// Put encodes one non-newline input byte.
func (e *Encoder) Put(b byte) {
	if b == ' ' || b == '\t' {
		e.flushWS(false) // previous ws is followed by more ws: literal
		e.ws = b
		return
	}
	w := 1
	if b < 33 || b > 126 || b == '=' {
		w = 3
	}
	e.ensureRoom(w)
	if w == 1 {
		e.out = append(e.out, b)
	} else {
		e.out = append(e.out, '=', hexdig[b>>4], hexdig[b&15])
	}
	e.col += w
	e.lastW = w
}

// Newline emits a hard CRLF; pending whitespace is at a line end and is
// escaped first.
func (e *Encoder) Newline() {
	e.flushWS(true)
	e.out = append(e.out, '\r', '\n')
	e.col, e.lastW = 0, 0
}

// Finish flushes pending whitespace (input end counts as a line end)
// and returns the encoded output.
func (e *Encoder) Finish() []byte {
	e.flushWS(true)
	return e.out
}

// ensureRoom guarantees room for a unit of width w, inserting a soft
// break if needed. Pending whitespace never sits before a soft break:
// it moves to the next line, where the unit follows it on the same
// line, so it stays literal.
func (e *Encoder) ensureRoom(w int) {
	need := w
	if e.ws != 0 {
		need++
	}
	if e.col+need > MaxLen {
		e.softBreak()
	}
	e.flushWS(false)
}

// flushWS resolves pending whitespace: escaped as =XX at a line end,
// emitted literally otherwise.
func (e *Encoder) flushWS(lineEnd bool) {
	if e.ws == 0 {
		return
	}
	if lineEnd {
		e.out = append(e.out, '=', hexdig[e.ws>>4], hexdig[e.ws&15])
		e.col += 3
		e.lastW = 3
	} else {
		e.out = append(e.out, e.ws)
		e.col++
		e.lastW = 1
	}
	e.ws = 0
}

// softBreak inserts "=\r\n". If the line already holds 76 characters,
// the last unit is moved to the next line to make room for the '='.
func (e *Encoder) softBreak() {
	var tail []byte
	if e.col == MaxLen {
		tail = append(tail, e.out[len(e.out)-e.lastW:]...)
		e.out = e.out[:len(e.out)-e.lastW]
	}
	e.out = append(e.out, '=', '\r', '\n')
	e.out = append(e.out, tail...)
	e.col = len(tail)
}
