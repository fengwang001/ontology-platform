// Package pt holds a single participant's vote state and the
// "a cast vote can never change" rule. It depends on nothing.
package pt

import "errors"

// ErrAlreadyVoted is returned when a participant that already cast
// its vote is asked to vote again. The state is left untouched.
var ErrAlreadyVoted = errors.New("pt: vote already cast")

// Vote is a participant's vote: not yet cast, yes, or no.
type Vote int

const (
	Unvoted Vote = iota
	Yes
	No
)

// State is one participant's vote slot.
type State struct {
	v Vote
}

// Cast records the participant's vote. A vote, once cast, is final:
// casting again returns ErrAlreadyVoted and changes nothing.
func (s *State) Cast(yes bool) error {
	if s.v != Unvoted {
		return ErrAlreadyVoted
	}
	if yes {
		s.v = Yes
	} else {
		s.v = No
	}
	return nil
}

// Vote reports the current vote (Unvoted if none cast yet).
func (s *State) Vote() Vote { return s.v }
