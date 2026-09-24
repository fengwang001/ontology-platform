// Package qpline implements single-line Quoted-Printable encoding decisions:
// which bytes escape, end-of-line whitespace handling, and soft-break points.
package qpline

const maxCol = 76

var hex = "0123456789ABCDEF"

// Encoder is a single-line (no hard newlines) Quoted-Printable encoder.
//
// The caller (package qp) owns hard newlines: it feeds line bytes via Put and
// then calls Line; Finish handles end-of-input. A token that brings the column
// to exactly maxCol is held as a "margin" token until the next byte proves the
// line continues, so a line ending at column 76 gets no spurious soft break.
// Trailing space/tab bytes stay pending until the following byte decides
// whether they are end-of-line whitespace (=20/=09) or inline whitespace.
type Encoder struct {
	out    []byte
	col    int
	checks int64

	margPos int // start in out of the held exactly-76 token; -1 when none
	margLen int

	wsStart int // start in out of the pending raw whitespace run; -1 when none
	wsN     int
}

// NewEncoder returns an empty line encoder.
func NewEncoder() *Encoder { return &Encoder{margPos: -1, wsStart: -1} }

// Put feeds one input byte (never '\n'; hard newlines are the qp layer's job).
func (e *Encoder) Put(b byte) {
	e.checks++
	e.commitMargin()
	if b == ' ' || b == '\t' {
		e.putWhitespace(b)
		return
	}
	if e.wsN > 0 {
		e.makeRoom(tokenLen(b)) // pending run is now known to be inline
		e.wsStart, e.wsN = -1, 0
	}
	e.putToken(b)
}

// Line ends the current logical line: pending whitespace becomes =20/=09, a
// held margin token stays on this line, then a hard CRLF is appended.
func (e *Encoder) Line() {
	e.finishPendingEncoded()
	e.out = append(e.out, '\r', '\n')
	e.col, e.margPos = 0, -1
	e.wsStart, e.wsN = -1, 0
}

// Finish flushes end-of-input pending whitespace as =20/=09.
func (e *Encoder) Finish() { e.finishPendingEncoded() }

// Bytes returns the encoded output accumulated so far.
func (e *Encoder) Bytes() []byte { return e.out }

// Checks reports how many input bytes Put has examined.
func (e *Encoder) Checks() int64 { return e.checks }

// Literal reports whether b may be emitted as itself inside a line.
func Literal(b byte) bool { return b >= 33 && b <= 126 && b != '=' }

func tokenLen(b byte) int {
	if Literal(b) {
		return 1
	}
	return 3
}

// putToken appends b's token; a token exactly filling maxCol is held as margin.
func (e *Encoder) putToken(b byte) {
	if Literal(b) {
		e.out = append(e.out, b)
		e.col++
		if e.col == maxCol {
			e.margPos, e.margLen = len(e.out)-1, 1
		}
		return
	}
	e.emitEscaped(b)
}

// emitEscaped appends =XX as one indivisible token, with a plain soft break
// when it would overflow. It neither holds a margin nor relocates whitespace.
func (e *Encoder) emitEscaped(b byte) {
	if e.col+3 > maxCol {
		e.out = append(e.out, '=', '\r', '\n')
		e.col = 0
	}
	e.out = append(e.out, '=', hex[b>>4], hex[b&0xf])
	e.col += 3
}

// makeRoom guarantees room for an n-byte token, inserting a soft break first.
// If the column already equals maxCol it holds a pending whitespace byte at
// column 76: pull it back so the soft-break '=' occupies column 76 and carry
// that whitespace raw onto the new line (see DESIGN.md §3).
func (e *Encoder) makeRoom(n int) {
	if e.col+n <= maxCol {
		return
	}
	carry := e.col == maxCol
	if carry {
		e.out = e.out[:len(e.out)-1]
		e.col, e.wsN = e.col-1, e.wsN-1
	}
	e.out = append(e.out, '=', '\r', '\n')
	e.col = 0
	if carry {
		e.out = append(e.out, ' ')
		e.col, e.wsStart, e.wsN = 1, len(e.out)-1, 1
	}
}

func (e *Encoder) putWhitespace(b byte) {
	if e.col == maxCol {
		// Column 76 holds a pending space; break, carry it plus the new byte.
		last := e.out[len(e.out)-1]
		e.out = append(e.out[:len(e.out)-1], '=', '\r', '\n', last, b)
		e.col = 2
		e.wsStart, e.wsN = len(e.out)-2, 2
		return
	}
	if e.wsN == 0 {
		e.wsStart = len(e.out)
	}
	e.out = append(e.out, b)
	e.col, e.wsN = e.col+1, e.wsN+1
}

func (e *Encoder) commitMargin() {
	if e.margPos < 0 {
		return
	}
	tail := append([]byte(nil), e.out[e.margPos:]...)
	e.out = append(e.out[:e.margPos], '=', '\r', '\n')
	e.out = append(e.out, tail...)
	e.col = e.margLen
	e.margPos, e.margLen = -1, 0
}

// finishPendingEncoded rewrites the pending raw whitespace run as =20/=09. The
// run ends a logical line (hard newline or EOF); whitespace tokens are laid by
// putToken, which inserts soft breaks normally if the run is long.
func (e *Encoder) finishPendingEncoded() {
	start, n := e.wsStart, e.wsN
	e.wsStart, e.wsN, e.margPos = -1, 0, -1
	if n == 0 {
		return
	}
	ws := append([]byte(nil), e.out[start:start+n]...)
	e.out, e.col = e.out[:start], e.col-n
	for _, b := range ws {
		e.emitEscaped(b)
	}
}
