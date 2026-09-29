// Package ws delays the decision on trailing spaces and tabs: a run is only
// known to be trailing once a newline or EOF is reached.
package ws

// IsTrailingByte reports whether b is part of a candidate trailing run
// (space or tab). No other bytes are ever buffered.
func IsTrailingByte(b byte) bool { return b == ' ' || b == '\t' }

// State buffers a single contiguous run of spaces/tabs whose fate is unknown.
type State struct {
	buf   []byte
	start int // original offset of the first buffered byte
}

// NewState returns a ready state with the given initial buffer capacity.
func NewState(hint int) *State {
	if hint < 0 {
		hint = 0
	}
	return &State{buf: make([]byte, 0, hint)}
}

// Start is the original offset of the pending run (undefined when empty).
func (s *State) Start() int { return s.start }

// Len is the number of buffered bytes.
func (s *State) Len() int { return len(s.buf) }

// Pending reports whether bytes are buffered.
func (s *State) Pending() bool { return len(s.buf) > 0 }

// Add buffers b at original offset off. It must be a space or tab. The first
// byte of a run anchors Start; subsequent bytes must be contiguous.
func (s *State) Add(off int, b byte) {
	if len(s.buf) == 0 {
		s.start = off
	}
	s.buf = append(s.buf, b)
}

// Flush returns and clears the run: it turned out to be interior whitespace
// and must be emitted verbatim.
func (s *State) Flush() []byte {
	out := s.buf
	s.buf = nil
	return out
}

// Drop discards the run: a newline/EOF proved it was trailing whitespace.
func (s *State) Drop() { s.buf = nil }

// Bytes exposes the buffered content without clearing it.
func (s *State) Bytes() []byte { return s.buf }
