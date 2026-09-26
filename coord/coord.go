// Package coord implements the 2PC coordinator: it aggregates
// participant votes (package pt) into Vote/Decide/Recover, keeping
// incremental yes/no counts so decisions never scan the table.
package coord

import (
	"errors"
	"sync"

	"ontology/pt"
)

// Sentinel errors, each distinct so callers can tell rejections apart.
var (
	ErrOutOfRange  = errors.New("coord: participant index out of range")
	ErrNotAllVoted = errors.New("coord: not all participants have voted")
	// ErrAlreadyVoted re-exports pt's: a duplicate Vote is rejected
	// with the same sentinel the participant state uses.
	ErrAlreadyVoted = pt.ErrAlreadyVoted
)

// Decision is the outcome of a transaction.
type Decision int

const (
	Undecided Decision = iota
	Commit
	Abort
)

func (d Decision) String() string {
	switch d {
	case Commit:
		return "Commit"
	case Abort:
		return "Abort"
	default:
		return "Undecided"
	}
}

// Coordinator tracks n participants' votes for one transaction.
// Safe for concurrent use.
type Coordinator struct {
	mu      sync.Mutex
	parts   []pt.State
	yes     int // incrementally maintained; Decide/Recover read these
	no      int // counters instead of scanning parts
	decided bool
	dec     Decision
	checked int // participants examined by last Decide/Recover (proof of O(1))
}

// New creates a coordinator for n participants.
func New(n int) *Coordinator {
	return &Coordinator{parts: make([]pt.State, n)}
}

// Vote records participant p's vote. Rejections (out-of-range index,
// duplicate vote) change nothing.
func (c *Coordinator) Vote(p int, yes bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if p < 0 || p >= len(c.parts) {
		return ErrOutOfRange
	}
	if err := c.parts[p].Cast(yes); err != nil {
		return err // ErrAlreadyVoted; state untouched
	}
	if yes {
		c.yes++
	} else {
		c.no++
	}
	return nil
}

// Decide resolves the transaction once every participant has voted:
// Commit iff all voted yes, Abort if any voted no. If votes are
// missing it fails with ErrNotAllVoted and changes nothing.
func (c *Coordinator) Decide() (Decision, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.checked = 0 // decided from counters; zero participants examined
	if c.decided {
		return c.dec, nil
	}
	if c.yes+c.no < len(c.parts) {
		return Undecided, ErrNotAllVoted
	}
	c.dec = Abort
	if c.no == 0 {
		c.dec = Commit
	}
	c.decided = true
	return c.dec, nil
}

// Recover applies crash-recovery rules: keep a prior decision; else
// decide from complete votes; else Abort the in-flight transaction.
func (c *Coordinator) Recover() Decision {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.checked = 0
	if c.decided {
		return c.dec
	}
	c.dec = Abort
	if c.yes == len(c.parts) { // unanimous yes is the only Commit
		c.dec = Commit
	}
	c.decided = true
	return c.dec
}

// Counts reports the current yes/no tallies.
func (c *Coordinator) Counts() (yes, no int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.yes, c.no
}
