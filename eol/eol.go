// Package eol identifies line endings: CRLF, lone CR, LF, and a CR pending
// at a split point. It has no dependencies on the other packages.
package eol

// Kind classifies a byte/sequence relative to line endings.
type Kind uint8

const (
	Other Kind = iota // ordinary byte
	LF                // a lone '\n' (or the '\n' half of a resolved CRLF)
	CR                // a lone '\r'
	Pending           // a '\r' whose following byte is not yet known
)

// Resolve classifies the byte after a pending '\r'.
// consume is true when next is '\n' and belongs to the same CRLF (it must be
// swallowed rather than emitted again).
func Resolve(next byte) (k Kind, consume bool) {
	if next == '\n' {
		return LF, true // CRLF -> single newline
	}
	if next == '\r' {
		return CR, false // previous CR was lone; new CR becomes pending
	}
	return Other, false // previous CR was lone
}

// IsEOL reports whether a resolved kind is a newline.
func IsEOL(k Kind) bool { return k == LF || k == CR }

// Classify maps a raw byte that is known not to follow a pending CR.
func Classify(b byte) Kind {
	switch b {
	case '\r':
		return Pending
	case '\n':
		return LF
	default:
		return Other
	}
}

// Scan reports the newline boundaries in a complete buffer. Each returned
// pair is [start,end) in b covering the raw ending bytes ("\r\n", "\r", "\n").
func Scan(b []byte) [][2]int {
	var out [][2]int
	for i := 0; i < len(b); i++ {
		if b[i] == '\r' {
			if i+1 < len(b) && b[i+1] == '\n' {
				out = append(out, [2]int{i, i + 2})
				i++
			} else {
				out = append(out, [2]int{i, i + 1})
			}
		} else if b[i] == '\n' {
			out = append(out, [2]int{i, i + 1})
		}
	}
	return out
}
