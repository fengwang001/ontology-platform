// Package qpline implements single-line Quoted-Printable encoding decisions
// (RFC 2045): which bytes must be escaped, trailing-whitespace handling and
// soft-line-break placement. It has no dependencies on other packages.
package qpline

import "fmt"

// MaxLine is the maximum number of characters per encoded line, excluding the
// hard line ending CRLF. The "=" that introduces a soft line break counts
// toward this limit.
const MaxLine = 76

// NeedEscape reports whether b must be emitted as a three-character =XX token
// rather than one literal character.
func NeedEscape(b byte) bool {
	switch {
	case b == '=':
		return true
	case b >= 33 && b <= 126:
		return false
	default:
		return true
	}
}

// HexVal decodes one hexadecimal digit, accepting both cases.
func HexVal(b byte) (byte, bool) {
	switch {
	case b >= '0' && b <= '9':
		return b - '0', true
	case b >= 'A' && b <= 'F':
		return b - 'A' + 10, true
	case b >= 'a' && b <= 'f':
		return b - 'a' + 10, true
	}
	return 0, false
}

// EncodeLine encodes one line that is not terminated by a newline. final
// selects EOF (true) versus a following hard line break (false); in both cases
// trailing spaces and tabs must be escaped because the strict decoder rejects
// raw whitespace at a line end. checks, when non-nil, is incremented once per
// input byte examined (the total is at most 2*len(line)).
func EncodeLine(line []byte, final bool, checks *int64) []byte {
	_ = final

	// First pass: find the trailing whitespace run by scanning backward only
	// across that run; the main pass never rescans earlier bytes.
	trailing := len(line)
	for trailing > 0 && (line[trailing-1] == ' ' || line[trailing-1] == '\t') {
		trailing--
		if checks != nil {
			*checks++
		}
	}

	out := make([]byte, 0, len(line)+len(line)/3)
	col := 0

	for i := 0; i < len(line); i++ {
		if checks != nil {
			*checks++
		}
		b := line[i]
		var tok []byte
		switch {
		case (b == ' ' || b == '\t') && i >= trailing:
			tok = []byte(fmt.Sprintf("=%02X", b))
		case b == ' ' || b == '\t':
			tok = []byte{b}
		case NeedEscape(b):
			tok = []byte(fmt.Sprintf("=%02X", b))
		default:
			tok = []byte{b}
		}
		// Each input byte yields exactly one token, so i+1==len(line) means
		// this is the final token. A line filled to exactly MaxLine still
		// needs a soft break when more tokens follow.
		if col+len(tok) > MaxLine || (col+len(tok) == MaxLine && i+1 < len(line)) {
			out = append(out, '=', '\r', '\n')
			col = 0
		}
		out = append(out, tok...)
		col += len(tok)
	}
	return out
}
