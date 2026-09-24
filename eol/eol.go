// Package eol identifies line endings across stream split points.
// A bare \r is pending until the next byte decides whether it starts \r\n.
package eol

// State is the pending-\r recognizer. Zero value is ready.
type State struct {
	pending bool
}

// Feed consumes b. It returns how many input bytes were consumed by this call
// to settle a pending \r (0 or 1), and whether b itself is a newline.
// When a \r arrives while another \r is pending, the previous \r is first
// settled as a standalone newline (the new \r stays pending).
func (s *State) Feed(b byte) (settledCR int, newline bool) {
	switch {
	case s.pending && b == '\n':
		s.pending = false
		return 1, true // \r\n: the \n byte closes the pair
	case s.pending:
		s.pending = false
		settledCR = 1 // previous \r was standalone
	}
	if b == '\r' {
		s.pending = true
	}
	return settledCR, b == '\n'
}

// Pending reports whether a \r awaits the next byte.
func (s *State) Pending() bool { return s.pending }

// Flush is called at end of stream: a pending \r is a standalone newline.
func (s *State) Flush() bool {
	if s.pending {
		s.pending = false
		return true
	}
	return false
}

// Reset clears the state.
func (s *State) Reset() { s.pending = false }
