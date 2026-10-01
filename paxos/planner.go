// Package paxos implements a Multi-Paxos slot recovery planner.
package paxos

import (
	"errors"
	"sort"
	"sync"
)

// AcceptedEntry is one accepted (ballot, value) pair for a slot.
type AcceptedEntry struct {
	Ballot int64
	Value  string
}

// PromiseReport is a promise reply collected from one acceptor.
type PromiseReport struct {
	Ballot   int64
	Chosen   int
	Accepted map[int]AcceptedEntry
}

// PlanEntry is one recovered slot entry, ordered by slot.
type PlanEntry struct {
	Slot         int
	Noop         bool
	Value        string
	SourceBallot int64
}

// RecoveryPlan is the deterministic recovery plan produced by Plan.
type RecoveryPlan struct {
	Start    int
	NextFree int
	Entries  []PlanEntry
}

var (
	ErrClosed             = errors.New("paxos: planner is closed")
	ErrAcceptorOutOfRange = errors.New("paxos: acceptor out of range")
	ErrWrongBallot        = errors.New("paxos: promise ballot does not match planner ballot")
	ErrSlotNotAfterChosen = errors.New("paxos: accepted slot must be greater than chosen prefix")
	ErrInvalidBallot      = errors.New("paxos: accepted ballot must be in [1, b)")
	ErrAlreadyPromised    = errors.New("paxos: acceptor already submitted a promise")
	ErrNoQuorum           = errors.New("paxos: majority promises not collected")
	ErrInvalidArgument    = errors.New("paxos: ballot and acceptor count must be positive")
)

// ConflictError reports the smallest slot whose maximum-ballot entries disagree.
type ConflictError struct {
	Slot int
}

func (e ConflictError) Error() string { return "paxos: conflicting values at slot" }

// Planner collects promise reports and builds a deterministic recovery plan.
type Planner struct {
	b int64
	n int

	mu       sync.Mutex
	closed   bool
	promised map[int]PromiseReport
}

// NewPlanner constructs a planner for ballot b with n acceptors.
func NewPlanner(b int64, n int) (*Planner, error) {
	if b <= 0 || n <= 0 {
		return nil, ErrInvalidArgument
	}
	return &Planner{
		b:        b,
		n:        n,
		promised: make(map[int]PromiseReport),
	}, nil
}

// AddPromise collects one promise report from acceptor from.
func (p *Planner) AddPromise(from int, report PromiseReport) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return ErrClosed
	}
	if from < 0 || from >= p.n {
		return ErrAcceptorOutOfRange
	}
	if report.Ballot != p.b {
		return ErrWrongBallot
	}

	slots := make([]int, 0, len(report.Accepted))
	for slot := range report.Accepted {
		slots = append(slots, slot)
	}
	sort.Ints(slots)
	for _, slot := range slots {
		if slot <= report.Chosen {
			return ErrSlotNotAfterChosen
		}
		entry := report.Accepted[slot]
		if entry.Ballot == 0 || entry.Ballot >= p.b {
			return ErrInvalidBallot
		}
	}

	if _, exists := p.promised[from]; exists {
		return ErrAlreadyPromised
	}

	stored := PromiseReport{
		Ballot:   report.Ballot,
		Chosen:   report.Chosen,
		Accepted: make(map[int]AcceptedEntry, len(report.Accepted)),
	}
	for slot, entry := range report.Accepted {
		stored.Accepted[slot] = entry
	}
	p.promised[from] = stored
	return nil
}

// Plan computes the recovery plan once a majority has promised.
func (p *Planner) Plan() (*RecoveryPlan, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return nil, ErrClosed
	}
	quorum := p.n/2 + 1
	if len(p.promised) < quorum {
		return nil, ErrNoQuorum
	}

	start := 0
	maxSlot := 0
	for _, report := range p.promised {
		if report.Chosen+1 > start {
			start = report.Chosen + 1
		}
		for slot := range report.Accepted {
			if slot > maxSlot {
				maxSlot = slot
			}
		}
	}

	entries := make([]PlanEntry, 0)
	for slot := start; slot <= maxSlot; slot++ {
		var maxBallot int64
		found := false
		for _, report := range p.promised {
			if entry, ok := report.Accepted[slot]; ok && (!found || entry.Ballot > maxBallot) {
				maxBallot = entry.Ballot
				found = true
			}
		}
		if !found {
			entries = append(entries, PlanEntry{Slot: slot, Noop: true, SourceBallot: 0})
			continue
		}

		value := ""
		first := true
		for _, report := range p.promised {
			entry, ok := report.Accepted[slot]
			if !ok || entry.Ballot != maxBallot {
				continue
			}
			if first {
				value, first = entry.Value, false
			} else if entry.Value != value {
				return nil, ConflictError{Slot: slot}
			}
		}
		entries = append(entries, PlanEntry{Slot: slot, Value: value, SourceBallot: maxBallot})
	}

	nextFree := maxSlot
	if start-1 > nextFree {
		nextFree = start - 1
	}
	nextFree++

	p.closed = true
	return &RecoveryPlan{Start: start, NextFree: nextFree, Entries: entries}, nil
}
