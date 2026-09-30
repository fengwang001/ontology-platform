// Package ws defers the verdict on a run of spaces and tabs.
//
// A whitespace byte is trailing only once a line ending or the stream end is
// seen; any other byte proves the run is interior content. State is therefore a
// single pending run plus its live length, which is all norm needs to enforce a
// bounded buffer without copying more than necessary.
package ws

const (
	Space byte = ' '
	Tab   byte = '\t'
)

// IsSpace reports whether b is trailing-trimmable horizontal whitespace.
func IsSpace(b byte) bool { return b == Space || b == Tab }

// Verdict resolves a pending whitespace run.
type Verdict int

const (
	// VerdictPending: not enough information yet.
	VerdictPending Verdict = iota
	// VerdictInterior: a non-whitespace byte proved the run is kept content.
	VerdictInterior
	// VerdictTrailing: a line ending / EOF proved the run is deleted.
	VerdictTrailing
)

// State tracks one undecided whitespace run.
type State struct {
	active bool
	length int
}

func New() *State { return &State{} }

// Pending reports whether a run is open and gives its length.
func (s *State) Pending() (bool, int) { return s.active, s.length }

// Observe feeds one byte; spaces/tabs extend the run, other bytes close it as
// interior. Line endings are reported via EndLine by the caller instead.
func (s *State) Observe(b byte) (v Verdict) {
	if IsSpace(b) {
		s.active = true
		s.length++
		return VerdictPending
	}
	if s.active {
		s.Reset()
		return VerdictInterior
	}
	return VerdictPending
}

// EndLine closes a run because a line ending was observed.
func (s *State) EndLine() (v Verdict) {
	if s.active {
		s.Reset()
		return VerdictTrailing
	}
	return VerdictPending
}

// EndStream closes a run at EOF: trailing whitespace of the last line is
// deleted.
func (s *State) EndStream() (v Verdict) { return s.EndLine() }

// Reset discards the pending run.
func (s *State) Reset() {
	s.active = false
	s.length = 0
}
