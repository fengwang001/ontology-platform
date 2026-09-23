// Package eol identifies line endings: CRLF, lone CR, LF, and a CR pending
// at a stream boundary. It depends on no other package.
package eol

// Kind classifies the next byte seen by a stream.
type Kind uint8

const (
	Other   Kind = iota // ordinary content byte
	LF                  // a '\n' that ends the current line
	CR                  // a lone '\r' that ends the current line
	CRLF                // the '\n' completing a "\r\n"
)

// Pending reports whether a trailing CR awaits the next byte: the next call
// must be made with pending=true so that a following '\n' joins the CR.
//
// Scan classifies b given whether the previous unconsumed byte was CR:
//
//	'\n' after CR -> CRLF (the CR is deleted; this '\n' is emitted);
//	'\n'          -> LF;
//	'\r'          -> CR (becomes '\n'; the CR is consumed but pending);
//	anything else after a pending CR -> Other, and the lone CR must first be
//	flushed as a line ending by the caller.
func Scan(b byte, pending bool) Kind {
	switch {
	case b == '\n' && pending:
		return CRLF
	case b == '\n':
		return LF
	case b == '\r':
		return CR
	default:
		return Other
	}
}

// IsEOL reports whether k terminates a line (CRLF counts once).
func IsEOL(k Kind) bool { return k == LF || k == CR || k == CRLF }
