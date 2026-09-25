// Package eol recognizes line endings (\r\n, lone \r, \n), including a
// pending \r at a split point.
package eol

// Decision classifies a byte in the line-ending state machine.
type Decision uint8

const (
	Content Decision = iota // ordinary byte, emitted unchanged
	CR                      // lone \r (first byte of a line ending)
	CRLF                    // second byte \n of a \r\n pair
	LF                      // standalone \n
	Pending                 // \r awaiting the next byte to disambiguate
)

// State is a single-pending-CR recognizer. Zero value is ready.
type State struct {
	pending bool
}

// Ready reports whether no \r is awaiting disambiguation.
func (s *State) Ready() bool { return !s.pending }

// Feed processes one byte. A pending \r is resolved first: \n turns it into
// CRLF, anything else emits a lone CR (via Flush) followed by the byte's
// own decision.
func (s *State) Feed(b byte) Decision {
	if s.pending {
		s.pending = false
		if b == '\n' {
			return CRLF
		}
	}
	switch b {
	case '\r':
		s.pending = true
		return Pending
	case '\n':
		return LF
	default:
		return Content
	}
}

// Flush resolves a pending \r as a lone CR at end of input; false if ready.
func (s *State) Flush() (Decision, bool) {
	if s.pending {
		s.pending = false
		return CR, true
	}
	return Content, false
}

// Reset returns the recognizer to its zero state.
func (s *State) Reset() { s.pending = false }
