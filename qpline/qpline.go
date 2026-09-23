// Package qpline implements single-line level Quoted-Printable (RFC 2045)
// encoding decisions: which bytes must be escaped, trailing-whitespace
// handling, and where soft line breaks are inserted. It has no dependencies.
package qpline

const (
	// MaxLineLen is the maximum encoded line length excluding CRLF.
	// The "=" of a soft break counts toward this limit.
	MaxLineLen = 76
	hexDigits  = "0123456789ABCDEF"
)

// IsPrintable reports whether b is printable ASCII other than "=".
func IsPrintable(b byte) bool {
	return b >= 33 && b <= 126 && b != '='
}

// IsWhitespace reports whether b is a space or horizontal tab.
func IsWhitespace(b byte) bool {
	return b == ' ' || b == '\t'
}

// IsNewline reports whether the input at i is a newline: "\n" or "\r\n".
// A lone "\r" is not a newline.
func IsNewline(src []byte, i int) bool {
	if src[i] == '\n' {
		return true
	}
	return src[i] == '\r' && i+1 < len(src) && src[i+1] == '\n'
}

// Encoder encodes byte slices. The zero value is ready to use.
type Encoder struct {
	checks int // number of times input bytes were inspected
}

// Checks returns the total number of input-byte inspections performed so far.
func (e *Encoder) Checks() int { return e.checks }

// Encode returns the Quoted-Printable encoding of src.
func (e *Encoder) Encode(src []byte) []byte {
	out := make([]byte, 0, len(src)+len(src)/4)
	col := 1
	for i := 0; i < len(src); {
		b := src[i]
		e.checks++
		if b == '\n' || (b == '\r' && i+1 < len(src) && src[i+1] == '\n') {
			out = append(out, '\r', '\n')
			if b == '\r' {
				i += 2
				e.checks++
			} else {
				i++
			}
			col = 1
			continue
		}
		token := rawToken(b)
		if IsWhitespace(b) {
			trailing := i+1 == len(src)
			if !trailing {
				e.checks++
				trailing = IsNewline(src, i+1)
			}
			if trailing {
				token = escapedToken(b)
			}
		}
		if col+len(token) > MaxLineLen {
			out = append(out, '=', '\r', '\n')
			col = 1
		}
		out = append(out, token...)
		col += len(token)
		i++
	}
	return out
}

func rawToken(b byte) []byte {
	if IsPrintable(b) || IsWhitespace(b) {
		return []byte{b}
	}
	return escapedToken(b)
}

func escapedToken(b byte) []byte {
	return []byte{'=', hexDigits[b>>4], hexDigits[b&0x0f]}
}
