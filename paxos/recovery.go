// Package paxos implements a slot-recovery planner for a new leader in
// Multi-Paxos. The planner collects promise reports from a majority of
// acceptors and derives, for every pending slot, the value that must be
// reproposed (the value accepted with the highest ballot) plus Noop
// fillers for holes, so that the recovery plan and the next free slot
// are exactly reproducible.
package paxos

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

var (
	// ErrClosed is reported when the planner has already produced a plan.
	ErrClosed = errors.New("paxos: planner is closed")
	// ErrFromOutOfRange is reported when the acceptor id is not in [0, n).
	ErrFromOutOfRange = errors.New("paxos: acceptor id out of range")
	// ErrBallotMismatch is reported when the promise ballot differs from b.
	ErrBallotMismatch = errors.New("paxos: promise ballot does not match planner ballot")
	// ErrSlotNotAfterChosen is reported when an accepted slot is not
	// greater than the report's chosen prefix (slot 0 included).
	ErrSlotNotAfterChosen = errors.New("paxos: accepted slot not greater than chosen prefix")
	// ErrInvalidAcceptBallot is reported when an accept ballot is 0 or >= b.
	ErrInvalidAcceptBallot = errors.New("paxos: accept ballot must be in [1, b)")
	// ErrDuplicatePromise is reported when an acceptor promises twice.
	ErrDuplicatePromise = errors.New("paxos: duplicate promise from acceptor")
	// ErrNoMajority is reported when fewer than floor(n/2)+1 promises exist.
	ErrNoMajority = errors.New("paxos: majority of promises not reached")
)

// ConflictError is reported by Plan when the entries holding the highest
// accept ballot for a slot carry different values. Slot is the smallest
// such slot.
type ConflictError struct {
	Slot int
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("paxos: conflicting values at slot %d for the highest accept ballot", e.Slot)
}

// AcceptedValue is one entry of an acceptor's accepted table: the ballot
// at which Value was accepted.
type AcceptedValue struct {
	Ballot int
	Value  string
}

// PromiseReport is the promise sent by one acceptor to the new leader.
//
//	PB:       the promise ballot; must equal the planner's ballot b.
//	Chosen:   the determined prefix length; slots 1..Chosen are known chosen.
//	Accepted: slot -> (accept ballot, value); values may be empty strings.
type PromiseReport struct {
	PB       int
	Chosen   int
	Accepted map[int]AcceptedValue
}

// PlanEntry describes the recovery action for one slot.
//
// Noop entries fill holes and carry Ballot 0; they are distinct from
// entries whose Value is the empty string (a real accepted value, with
// Ballot > 0).
type PlanEntry struct {
	Slot   int
	Noop   bool
	Value  string
	Ballot int
}

// RecoveryPlan is the result of a successful Plan call.
type RecoveryPlan struct {
	Start    int
	NextFree int
	Entries  []PlanEntry
}

// Planner collects promise reports for ballot b from n acceptors
// (ids 0..n-1) and computes the slot-recovery plan. It is safe for
// concurrent use.
type Planner struct {
	mu       sync.Mutex
	b        int
	n        int
	closed   bool
	promises map[int]PromiseReport
}

// NewPlanner builds a planner for ballot b and n acceptors. Both must be
// positive.
func NewPlanner(b, n int) (*Planner, error) {
	if b <= 0 {
		return nil, fmt.Errorf("paxos: ballot must be positive, got %d", b)
	}
	if n <= 0 {
		return nil, fmt.Errorf("paxos: acceptor count must be positive, got %d", n)
	}
	return &Planner{b: b, n: n, promises: make(map[int]PromiseReport)}, nil
}

// AddPromise collects one promise report from acceptor `from`.
//
// Validation order (first failure wins): planner closed; from out of
// range; promise ballot mismatch; accepted entries in ascending slot
// order (slot not greater than chosen first, then invalid accept
// ballot); duplicate promise from the same acceptor. A rejected report
// changes no state.
func (p *Planner) AddPromise(from int, rep PromiseReport) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return ErrClosed
	}
	if from < 0 || from >= p.n {
		return ErrFromOutOfRange
	}
	if rep.PB != p.b {
		return ErrBallotMismatch
	}
	slots := make([]int, 0, len(rep.Accepted))
	for slot := range rep.Accepted {
		slots = append(slots, slot)
	}
	sort.Ints(slots)
	for _, slot := range slots {
		if slot <= rep.Chosen {
			return ErrSlotNotAfterChosen
		}
		if av := rep.Accepted[slot]; av.Ballot == 0 || av.Ballot >= p.b {
			return ErrInvalidAcceptBallot
		}
	}
	if _, dup := p.promises[from]; dup {
		return ErrDuplicatePromise
	}

	stored := PromiseReport{PB: rep.PB, Chosen: rep.Chosen}
	if len(rep.Accepted) > 0 {
		stored.Accepted = make(map[int]AcceptedValue, len(rep.Accepted))
		for slot, av := range rep.Accepted {
			stored.Accepted[slot] = av
		}
	}
	p.promises[from] = stored
	return nil
}

// Plan computes the recovery plan and closes the planner on success.
//
// Error order (first failure wins): planner closed; majority not
// reached; value conflict at the smallest slot. A failed Plan does not
// close the planner. The returned plan shares no state with the
// planner.
func (p *Planner) Plan() (RecoveryPlan, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return RecoveryPlan{}, ErrClosed
	}
	if len(p.promises) < p.n/2+1 {
		return RecoveryPlan{}, ErrNoMajority
	}

	start := 1
	maxSlot := 0
	for _, rep := range p.promises {
		if rep.Chosen+1 > start {
			start = rep.Chosen + 1
		}
		for slot := range rep.Accepted {
			if slot > maxSlot {
				maxSlot = slot
			}
		}
	}

	var entries []PlanEntry
	for slot := start; slot <= maxSlot; slot++ {
		bestBallot := 0
		var bestValue string
		conflict := false
		for _, rep := range p.promises {
			av, ok := rep.Accepted[slot]
			if !ok {
				continue
			}
			switch {
			case av.Ballot > bestBallot:
				bestBallot = av.Ballot
				bestValue = av.Value
				conflict = false
			case av.Ballot == bestBallot && av.Value != bestValue:
				conflict = true
			}
		}
		if conflict {
			return RecoveryPlan{}, &ConflictError{Slot: slot}
		}
		if bestBallot == 0 {
			entries = append(entries, PlanEntry{Slot: slot, Noop: true})
		} else {
			entries = append(entries, PlanEntry{Slot: slot, Value: bestValue, Ballot: bestBallot})
		}
	}

	nextFree := maxSlot
	if start-1 > nextFree {
		nextFree = start - 1
	}
	nextFree++

	p.closed = true
	return RecoveryPlan{Start: start, NextFree: nextFree, Entries: entries}, nil
}
