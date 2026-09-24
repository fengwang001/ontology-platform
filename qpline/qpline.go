// Package qpline implements single-line encoding decisions for
// Quoted-Printable (RFC 2045): which bytes must be escaped, how trailing
// whitespace is handled, and where soft line breaks are inserted.
package qpline

// MaxLineLen is the maximum physical line length excluding the trailing
// CRLF. The "=" of a soft line break counts toward this limit.
const MaxLineLen = 76

// SoftBreak is inserted to wrap an over-long physical line; a strict decoder
// removes it entirely.
var SoftBreak = []byte{'=', '\r', '\n'}

var hexDigits = "0123456789ABCDEF"

// Hex returns the two uppercase hexadecimal digits of b, most significant
// first.
func Hex(b byte) string { return string([]byte{hexDigits[b>>4], hexDigits[b&0xf]}) }

// Raw reports whether b is emitted as-is: printable ASCII 33..126 except '='.
func Raw(b byte) bool { return b >= 33 && b <= 126 && b != '=' }

// WhiteSpace reports b is a space or horizontal tab.
func WhiteSpace(b byte) bool { return b == ' ' || b == '\t' }

// TokenWidth returns the encoded width of b when it is not trailing
// whitespace: 3 for "=XX", otherwise 1.
func TokenWidth(b byte) int {
	if Raw(b) {
		return 1
	}
	return 3
}

// AppendToken writes b's encoded token (raw byte or "=XX") to out.
func AppendToken(out []byte, b byte) []byte {
	if Raw(b) {
		return append(out, b)
	}
	return append(out, '=', hexDigits[b>>4], hexDigits[b&0xf])
}

// EncodeLine encodes a single logical line without its line ending.
//
// trailingWS is true when this line ends at input EOF or immediately before
// a line break: its trailing run of spaces/tabs is then dangling whitespace
// and is emitted as "=20"/"=09".
//
// Physical lines are at most MaxLineLen columns (the "=" of a soft break
// counts). A raw space/tab immediately before a soft break would become
// unescaped whitespace at a physical line end, so such bytes are pulled
// back and re-emitted escaped on the next physical line.
//
// It returns the extended buffer and the number of input bytes examined
// (each byte is examined at most twice: once streaming, once by the single
// trailing-whitespace lookahead; pulled-back whitespace is not rescanned).
func EncodeLine(out, line []byte, trailingWS bool) ([]byte, int) {
	examined := len(line)
	wsStart := len(line)
	if trailingWS {
		for wsStart > 0 && WhiteSpace(line[wsStart-1]) {
			wsStart--
			examined++
		}
	}
	col := 0
	emit := func(b byte, escaped bool) {
		if escaped {
			if col+3 > MaxLineLen {
				out = append(out, SoftBreak...)
				col = 0
			}
			out = append(out, '=', hexDigits[b>>4], hexDigits[b&0xf])
			col += 3
			return
		}
		if col+1 > MaxLineLen {
			out = append(out, SoftBreak...)
			col = 0
		}
		out = append(out, b)
		col++
	}
	for i := 0; i < len(line); i++ {
		b := line[i]
		escaped := WhiteSpace(b) && i >= wsStart || !Raw(b) && !WhiteSpace(b)
		width := 1
		if escaped {
			width = 3
		}
		if col+width > MaxLineLen {
			var pulled []byte
			for len(out) > 0 && WhiteSpace(out[len(out)-1]) {
				pulled = append(pulled, out[len(out)-1])
				out = out[:len(out)-1]
			}
			col -= len(pulled)
			for j := len(pulled) - 1; j >= 0; j-- {
				emit(pulled[j], true)
			}
		}
		emit(b, escaped)
	}
	return out, examined
}
