// Package eol identifies the three line-ending shapes \r\n, lone \r and \n,
// including the undecided state when a \r sits at a stream cut point.
package eol

// Kind classifies a byte boundary as seen by the streaming parser.
type Kind uint8

const (
	Other  Kind = iota // not part of a line ending
	CR                 // lone \r already resolvable as one line ending
	LF                 // \n that starts a line ending (its \r was already seen)
	CRLF               // the \n of a \r\n pair
	HoldCR             // \r that must wait for the next byte before resolving
)

// IsEOLByte reports whether b is a raw line-ending byte.
func IsEOLByte(b byte) bool { return b == '\r' || b == '\n' }

// Tracker is a one-byte-memory state machine. Feed is called for every input
// byte in order; it returns the classification of the current byte.
// A \r is reported as HoldCR until the following byte decides whether it is a
// lone \r (Flush emits it) or the first half of \r\n (reported as CRLF).
type Tracker struct {
	holding bool
}

// New creates a Tracker. If heldCR is true the machine starts as if a \r had
// just been read at the end of a preceding chunk (used at parallel cut points).
func New(heldCR bool) Tracker { return Tracker{holding: heldCR} }

// Feed classifies b given the preceding undecided \r, if any.
func (t *Tracker) Feed(b byte) Kind {
	if t.holding {
		t.holding = false
		if b == '\n' {
			return CRLF
		}
		if b == '\r' {
			t.holding = true
			return CR
		}
		return LF
	}
	if b == '\r' {
		t.holding = true
		return HoldCR
	}
	if b == '\n' {
		return LF
	}
	return Other
}

// Flush resolves a \r left pending at a stream boundary. nextFirst is the first
// byte of the following chunk, or zero at true end of stream. It returns CR
// when the held \r is a lone line ending, or CRLF when it pairs with the
// following chunk's '\n' (this chunk emits nothing; the next chunk emits \n).
func (t *Tracker) Flush(nextFirst byte) Kind {
	if !t.holding {
		return Other
	}
	t.holding = false
	if nextFirst == '\n' {
		return CRLF
	}
	return CR
}

// Holding reports whether a \r is currently undecided.
func (t *Tracker) Holding() bool { return t.holding }

// Reset clears all state.
func (t *Tracker) Reset() { t.holding = false }
