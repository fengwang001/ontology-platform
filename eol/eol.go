// Package eol classifies byte-stream line endings: "\r\n", lone "\r" and
// "\n". A trailing "\r" stays pending until the next byte or EOF.
package eol

// State is a zero-value-ready streaming recognizer.
type State struct {
	pendingCR bool
}

// Feed consumes exactly one byte b and reports:
//   - nl: one normalized newline must be emitted now;
//   - pairCRLF: that newline came from a "\r\n" pair (the '\r' is deleted,
//     the '\n' kept at its original offset); otherwise it is a lone '\r'/'\n';
//   - verb with hasVerb: b itself passes through verbatim.
//
// A lone '\r' resolving before a verbatim byte produces BOTH nl and verb in
// the same step (e.g. "\rX" -> newline for CR, X verbatim); a second '\r'
// replaces the pending one and yields only nl.
func (s *State) Feed(b byte) (nl, pairCRLF, hasVerb bool, verb byte) {
	if s.pendingCR {
		switch b {
		case '\n':
			s.pendingCR = false
			return true, true, false, 0
		case '\r':
			return true, false, false, 0
		default:
			s.pendingCR = false
			return true, false, true, b
		}
	}
	if b == '\r' {
		s.pendingCR = true
		return false, false, false, 0
	}
	if b == '\n' {
		return true, false, false, 0
	}
	return false, false, true, b
}

// Flush resolves a pending '\r' at EOF as a lone newline; it returns true when
// one more newline must be emitted.
func (s *State) Flush() bool {
	p := s.pendingCR
	s.pendingCR = false
	return p
}

// Pending reports whether a '\r' awaits the next byte.
func (s *State) Pending() bool { return s.pendingCR }
