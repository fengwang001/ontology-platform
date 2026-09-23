// Package eol classifies line endings at a byte position, including the
// undetermined lone CR that sits on a chunk boundary.
package eol

// Kind describes the line ending seen at the start of a byte slice.
type Kind int

const (
	// None means the first byte is not a line-ending byte.
	None Kind = iota
	// LF is a lone '\n'.
	LF
	// CR is a definite lone '\r' (the following byte is known and is not '\n').
	CR
	// CRLF is the two-byte sequence "\r\n".
	CRLF
	// MaybeCR is a lone '\r' with no following byte visible yet; the next
	// byte decides whether it becomes CR or CRLF.
	MaybeCR
)

// At classifies the line ending at p[0]. It never inspects past p[1].
func At(p []byte) Kind {
	if len(p) == 0 {
		return None
	}
	switch p[0] {
	case '\n':
		return LF
	case '\r':
		if len(p) == 1 {
			return MaybeCR
		}
		if p[1] == '\n' {
			return CRLF
		}
		return CR
	default:
		return None
	}
}

// Width reports how many input bytes a definite Kind consumes. MaybeCR
// consumes one byte provisionally and returns 1.
func (k Kind) Width() int {
	switch k {
	case CRLF:
		return 2
	case LF, CR, MaybeCR:
		return 1
	default:
		return 0
	}
}

// Resolve turns a MaybeCR plus the next byte into a definite Kind: '\n'
// yields CRLF, anything else yields CR. Resolving a non-pending kind is
// the identity.
func Resolve(k Kind, next byte, hasNext bool) Kind {
	if k != MaybeCR {
		return k
	}
	if hasNext && next == '\n' {
		return CRLF
	}
	return CR
}
