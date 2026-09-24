// Package qpline implements single-line Quoted-Printable layout: which bytes
// need escaping and where soft line breaks are inserted. It knows nothing
// about input fragments, streams, or errors.
package qpline

// MaxLine is the maximum encoded line length excluding the trailing CRLF.
// The soft-break "=" counts as one of these characters.
const MaxLine = 76

const hex = "0123456789ABCDEF"

// Printable reports whether b is emitted raw per RFC 2045: ASCII 33..126
// except '='. Space and tab are raw elsewhere but handled specially at EOL.
func Printable(b byte) bool {
	return b >= 33 && b <= 126 && b != '='
}

// IsSpace reports a bare-whitespace byte (space or horizontal tab).
func IsSpace(b byte) bool { return b == ' ' || b == '\t' }

// TokenLen is the encoded width of one input byte inside a logical line:
// 3 for "=XX", 1 for a raw byte.
func TokenLen(b byte) int {
	if Printable(b) || IsSpace(b) {
		return 1
	}
	return 3
}

// AppendToken encodes a single byte onto dst without inserting line breaks.
func AppendToken(dst []byte, b byte) []byte {
	if Printable(b) || IsSpace(b) {
		return append(dst, b)
	}
	return append(dst, '=', hex[b>>4], hex[b&0xf])
}

// NeedSoftBreak decides whether a soft break ("=\r\n") must precede a token
// of width tokLen placed in a line already holding col characters. more
// reports whether another token follows on the same logical line: when it
// does, one column is reserved for the eventual soft-break "=".
func NeedSoftBreak(col, tokLen int, more bool) bool {
	if more {
		return col+tokLen+1 > MaxLine
	}
	return col+tokLen > MaxLine
}

// Writer lays out one logical line (input terminated by a hard newline or
// EOF), emitting soft breaks as required.
type Writer struct {
	out []byte
	col int
}

// NewWriter returns a Writer appending onto dst.
func NewWriter(dst []byte) *Writer { return &Writer{out: dst} }

// Put appends the encoded form of b. more has the same meaning as in
// NeedSoftBreak: another token follows before the hard line end.
func (w *Writer) Put(b byte, more bool) {
	if NeedSoftBreak(w.col, TokenLen(b), more) {
		w.out = append(w.out, '=', '\r', '\n')
		w.col = 0
	}
	w.out = AppendToken(w.out, b)
	w.col += TokenLen(b)
}

// EndLine writes the hard line ending and resets for the next line.
func (w *Writer) EndLine() []byte {
	w.out = append(w.out, '\r', '\n')
	w.col = 0
	return w.out
}

// Bytes returns the accumulated output.
func (w *Writer) Bytes() []byte { return w.out }
