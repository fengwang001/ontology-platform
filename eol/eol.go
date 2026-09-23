// Package eol identifies line endings: CRLF, lone CR, LF, and a pending CR
// at a split point whose following byte is not yet known.
package eol

// Kind classifies a byte position.
type Kind uint8

const (
	KOther     Kind = iota // ordinary byte
	KLF                    // a lone '\n'
	KCRLFStart             // the '\r' of a confirmed "\r\n"
	KCRPending             // a '\r' not yet followed by '\n'
)

// CR and LF byte values.
const (
	CR byte = '\r'
	LF byte = '\n'
)

// ClassifyAt returns the kind of s[i]. next is the byte immediately after i
// (-1 when i is the last known byte, so a trailing CR is PendingCR).
func ClassifyAt(s []byte, i, next int) Kind {
	switch s[i] {
	case LF:
		return KLF
	case CR:
		if next >= 0 && next < len(s) && s[next] == LF {
			return KCRLFStart
		}
		return KCRPending
	default:
		return KOther
	}
}
