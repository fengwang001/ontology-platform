// Package qpline decides how a single line is encoded in
// Quoted-Printable (RFC 2045): which bytes must be escaped, how
// trailing whitespace is handled, and where soft breaks go.
package qpline

// Max is the maximum number of characters per encoded line,
// excluding the trailing CRLF. A soft break's '=' counts toward it.
const Max = 76

// Printable reports whether b is printable ASCII (33-126).
func Printable(b byte) bool { return b >= 33 && b <= 126 }

// Blank reports whether b is a space or a tab.
func Blank(b byte) bool { return b == ' ' || b == '\t' }

// Esc returns the =XX escape sequence for b, with uppercase hex.
func Esc(b byte) [3]byte {
	const digits = "0123456789ABCDEF"
	return [3]byte{'=', digits[b>>4], digits[b&0x0f]}
}

// Unhex parses one hex digit, accepting either case.
func Unhex(c byte) (byte, bool) {
	switch {
	case '0' <= c && c <= '9':
		return c - '0', true
	case 'A' <= c && c <= 'F':
		return c - 'A' + 10, true
	case 'a' <= c && c <= 'f':
		return c - 'a' + 10, true
	}
	return 0, false
}

// Encoder emits one input line, inserting soft breaks so that no
// output line exceeds Max characters. The zero value is ready to
// use; one Encoder must be used per input line.
type Encoder struct{ col int }

// Feed appends the encoding of b to dst. last reports whether b is
// the final byte of the input line (followed by a newline or EOF).
func (e *Encoder) Feed(dst []byte, b byte, last bool) []byte {
	limit := Max - 1 // leave room for a soft break's '='
	if last {
		limit = Max
	}
	tok := [3]byte{b}
	w := 1
	switch {
	case Blank(b):
		// A blank is escaped when it would sit at the end of an
		// output line: at the end of the input line, or when the
		// next token would force a soft break right after it.
		if last || e.col+1 == Max-1 {
			tok, w = Esc(b), 3
		}
	case Printable(b) && b != '=':
	default:
		tok, w = Esc(b), 3
	}
	if e.col+w > limit {
		dst = append(dst, '=', '\r', '\n')
		e.col = 0
	}
	e.col += w
	return append(dst, tok[:w]...)
}
