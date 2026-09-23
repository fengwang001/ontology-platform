// Package qpline implements single-line Quoted-Printable encoding decisions:
// which bytes must be escaped, trailing-whitespace handling, and soft line
// break placement. It has no dependencies on other packages.
package qpline

const maxLine = 76 // RFC 2045: max characters per output line excluding CRLF

const hexUpper = "0123456789ABCDEF"

// NeedsEscape reports whether b must be emitted as =XX.
// Printable ASCII 33..126 except '=' is literal; space and tab are reported
// literal here too (line-end handling is the caller's concern via EndLine/End).
func NeedsEscape(b byte) bool {
	switch {
	case b == '=':
		return true
	case b >= 33 && b <= 126:
		return false
	default:
		return true // includes space/tab-independent bytes, lone CR, binary
	}
}

// LiteralSpace reports that space/tab are only literal mid-line.
func LiteralSpace(b byte) bool { return b == ' ' || b == '\t' }

// Writer encodes one logical line (bytes excluding the hard line break).
// Call EndLine at a hard line break, End at end of input.
type Writer struct {
	buf []byte
	col int // encoded chars on the current soft-break segment
}

// Put feeds one input byte (must not be a hard line break byte).
func (w *Writer) Put(b byte) {
	if LiteralSpace(b) {
		w.reserve(1)
		w.buf = append(w.buf, b)
		return
	}
	if NeedsEscape(b) {
		w.reserve(3)
		w.buf = append(w.buf, '=', hexUpper[b>>4], hexUpper[b&0x0f])
		return
	}
	w.reserve(1)
	w.buf = append(w.buf, b)
}

func (w *Writer) reserve(n int) {
	// Content segment is capped at 75 so the soft-break '=' is always at
	// column <=76; a 3-char =XX token therefore never straddles a break.
	if w.col+n > maxLine-1 {
		w.buf = append(w.buf, '=', '\r', '\n')
		w.col = 0
	}
	w.col += n
}

// EndLine finishes the current line at a hard line break and appends CRLF.
func (w *Writer) EndLine() []byte { return w.finish(true) }

// End finishes the final line at end of input (no CRLF appended).
func (w *Writer) End() []byte { return w.finish(false) }

func (w *Writer) finish(hard bool) []byte {
	start := 0
	if i := bytesLastIndex(w.buf, []byte{'=', '\r', '\n'}); i >= 0 {
		start = i + 3 // only the final soft-break segment can end the line
	}
	end := len(w.buf)
	seg := w.buf[start:end]
	i := len(seg)
	for i > 0 && (seg[i-1] == ' ' || seg[i-1] == '\t') {
		i--
	}
	if i < len(seg) {
		trailing := seg[i:]
		head := w.buf[:start+i]
		out := make([]byte, 0, len(w.buf)+len(trailing)*2+2)
		out = append(out, head...)
		for _, b := range trailing {
			out = append(out, '=', hexUpper[b>>4], hexUpper[b&0x0f])
		}
		w.buf = out
	}
	out := w.buf
	if hard {
		out = append(out, '\r', '\n')
	}
	w.buf = nil
	w.col = 0
	return out
}

// Reset returns the writer to its initial state.
func (w *Writer) Reset() { w.buf = nil }

func bytesLastIndex(s, sep []byte) int {
	for i := len(s) - len(sep); i >= 0; i-- {
		match := true
		for j := range sep {
			if s[i+j] != sep[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}
