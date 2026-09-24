// Package eol recognizes the three line-ending shapes across stream cuts.
// A pending CR at a cut point is represented by Pending until the next byte
// (or end of stream) resolves it.
package eol

// Kind classifies a byte boundary with respect to line endings.
type Kind int

const (
	Other   Kind = iota // ordinary byte
	LF                  // bare '\n'
	CRLF                // the '\n' that completes a '\r\n'
	Pending             // '\r' waiting for the next byte
)

// Classify classifies byte b given that the previous byte was a pending CR.
func Classify(b byte, crPending bool) Kind {
	switch {
	case b == '\r':
		return Pending
	case b == '\n' && crPending:
		return CRLF
	case b == '\n':
		return LF
	default:
		return Other
	}
}

// ResolvePending maps a still-pending CR at EOF (or before a non-LF byte) to
// a single normalized line ending.
func ResolvePending() Kind { return LF }

// Consumed reports whether a pending CR is consumed by the classified byte
// (only CRLF consumes it).
func (k Kind) Consumed() bool { return k == CRLF }
