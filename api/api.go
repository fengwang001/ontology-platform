// Package api is the outward face of the 2PC coordinator: it wraps
// package coord and provides SelfCheck, which verifies the four
// invariants from NOTES.md against built-in operation sequences.
package api

import (
	"errors"
	"fmt"

	"ontology/coord"
)

// Re-exported sentinels so callers only import api.
var (
	ErrOutOfRange   = coord.ErrOutOfRange
	ErrAlreadyVoted = coord.ErrAlreadyVoted
	ErrNotAllVoted  = coord.ErrNotAllVoted
)

// Decision is coord.Decision, re-exported.
type Decision = coord.Decision

const (
	Undecided = coord.Undecided
	Commit    = coord.Commit
	Abort     = coord.Abort
)

// Coordinator coordinates one transaction among n participants.
type Coordinator struct {
	c *coord.Coordinator
}

// New creates a coordinator for n participants.
func New(n int) *Coordinator { return &Coordinator{coord.New(n)} }

// Vote records participant p's vote (yes/no). A cast vote is final.
func (a *Coordinator) Vote(p int, yes bool) error { return a.c.Vote(p, yes) }

// Decide resolves the transaction; fails with ErrNotAllVoted while
// any participant has not voted.
func (a *Coordinator) Decide() (Decision, error) { return a.c.Decide() }

// Recover applies crash-recovery rules and returns the decision.
func (a *Coordinator) Recover() Decision { return a.c.Recover() }

// Counts reports the current yes/no tallies.
func (a *Coordinator) Counts() (yes, no int) { return a.c.Counts() }

// SelfCheck verifies the four invariants on built-in sequences:
//  1. Decide matches a naive rescan of every participant;
//  2. Commit requires unanimity;
//  3. a cast vote cannot change;
//  4. rejected operations leave no trace.
//
// It returns nil when all hold, else the first violation found.
func SelfCheck() error {
	// Invariants 1+2: for every no-position (and none), Decide must
	// equal the naive "scan all, Commit iff every vote is yes".
	for n := 1; n <= 4; n++ {
		for noAt := -1; noAt < n; noAt++ {
			c := New(n)
			naive := Commit
			for p := 0; p < n; p++ {
				yes := p != noAt
				if err := c.Vote(p, yes); err != nil {
					return fmt.Errorf("selfcheck: vote: %w", err)
				}
				if !yes {
					naive = Abort
				}
			}
			got, err := c.Decide()
			if err != nil || got != naive {
				return fmt.Errorf("selfcheck: n=%d noAt=%d: got %v want %v", n, noAt, got, naive)
			}
		}
	}
	// Invariant 3: a cast vote cannot be overwritten.
	c := New(2)
	if err := c.Vote(0, false); err != nil {
		return fmt.Errorf("selfcheck: vote: %w", err)
	}
	if !errors.Is(c.Vote(0, true), ErrAlreadyVoted) {
		return errors.New("selfcheck: duplicate vote not rejected")
	}
	if y, n := c.Counts(); y != 0 || n != 1 {
		return errors.New("selfcheck: duplicate vote changed counts")
	}
	// Invariant 4: each rejection leaves state untouched and the
	// coordinator keeps working afterwards.
	c = New(2)
	if !errors.Is(c.Vote(7, true), ErrOutOfRange) {
		return errors.New("selfcheck: out-of-range not rejected")
	}
	if _, err := c.Decide(); !errors.Is(err, ErrNotAllVoted) {
		return errors.New("selfcheck: premature Decide not rejected")
	}
	if y, n := c.Counts(); y != 0 || n != 0 {
		return errors.New("selfcheck: rejection left a trace")
	}
	if err := c.Vote(0, true); err != nil {
		return errors.New("selfcheck: coordinator unusable after rejection")
	}
	if err := c.Vote(1, true); err != nil {
		return fmt.Errorf("selfcheck: vote: %w", err)
	}
	if d, err := c.Decide(); err != nil || d != Commit {
		return errors.New("selfcheck: all-yes did not commit")
	}
	return nil
}
