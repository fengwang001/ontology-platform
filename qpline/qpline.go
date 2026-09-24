// Package qpline implements single-physical-line level Quoted-Printable
// encoding decisions: which bytes are escaped, end-of-line whitespace
// handling, and soft line break placement. It has no dependencies.
package qpline

const (
	// LineLimit is the maximum characters per encoded line excluding CRLF.
	LineLimit = 76
	// SoftBreakWidth is the leading "=" of a soft break, occupying column 76.
	SoftBreakWidth = 1
)

var hex = "0123456789ABCDEF"

// NewlineLen reports 2 for a "\r\n" at i, 1 for a lone "\n" at i,
// and 0 otherwise (a lone "\r" is an ordinary byte).
func NewlineLen(src []byte, i int) int {
	if src[i] == '\n' {
		return 1
	}
	if src[i] == '\r' && i+1 < len(src) && src[i+1] == '\n' {
		return 2
	}
	return 0
}

// EncodeLine appends the QP encoding of src to dst. src must contain no
// hard newline. inspect, if non-nil, is called once per input byte
// examined.
func EncodeLine(dst, src []byte, inspect func()) []byte {
	cur := 0
	for i, b := range src {
		if inspect != nil {
			inspect()
		}
		last := i == len(src)-1
		width := 1
		literal := b >= 33 && b <= 126 && b != '='
		if !literal {
			// Whitespace is kept literal unless it is final on the line;
			// everything else becomes =XX.
			ws := b == ' ' || b == '\t'
			literal = ws && !last
			if !literal {
				width = 3
			}
		}
		limit := LineLimit - SoftBreakWidth
		if last {
			limit = LineLimit
		}
		if cur+width > limit {
			dst = append(dst, '=', '\r', '\n')
			cur = 0
		}
		if literal {
			dst = append(dst, b)
		} else {
			dst = append(dst, '=', hex[b>>4], hex[b&0x0f])
		}
		cur += width
	}
	return dst
}
