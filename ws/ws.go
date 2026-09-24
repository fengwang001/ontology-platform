// Package ws delays classification of trailing spaces/tabs until a line
// ending or end of stream proves whether they are trailing whitespace.
package ws

import "errors"

// ErrLimit is reported when a pending whitespace run exceeds the configured
// buffer limit. The run is retained and the classifier enters a terminal state.
var ErrLimit = errors.New("ws: trailing whitespace buffer limit exceeded")

// State buffers the current run of spaces/tabs whose fate is unknown.
// One instance is not safe for concurrent use.
type State struct {
	limit int
	run   []byte
	start int
}

// NewState creates a State with the maximum buffered run length (minimum 1).
func NewState(limit int) *State {
	if limit < 1 {
		limit = 1
	}
	return &State{limit: limit, start: -1}
}

// IsSpace reports whether b is a space or tab.
func IsSpace(b byte) bool { return b == ' ' || b == '\t' }

// Add appends a space/tab at original offset off. It returns ErrLimit when the
// run would exceed the limit; afterwards the state must not be reused.
func (s *State) Add(b byte, off int) error {
	if len(s.run) == 0 {
		s.start = off
	}
	if len(s.run) >= s.limit {
		return ErrLimit
	}
	s.run = append(s.run, b)
	return nil
}

// Pending reports a buffered run: its bytes and [Start,End) original span.
func (s *State) Pending() (b []byte, start, end int, ok bool) {
	if len(s.run) == 0 {
		return nil, 0, 0, false
	}
	return s.run, s.start, s.start + len(s.run), true
}

// FlushKeep resolves the run as in-line whitespace: the bytes are returned and
// the buffer cleared.
func (s *State) FlushKeep() []byte {
	b := s.run
	s.run = nil
	s.start = -1
	return b
}

// FlushDrop resolves the run as trailing whitespace: it is discarded.
func (s *State) FlushDrop() (start, end int, ok bool) {
	if len(s.run) == 0 {
		return 0, 0, false
	}
	start, end = s.start, s.start+len(s.run)
	s.run = nil
	s.start = -1
	return start, end, true
}
