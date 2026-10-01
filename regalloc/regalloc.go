// Package regalloc implements a linear-scan register allocator.
//
// Intervals are half-open [start, end) and must be added with
// non-decreasing start points. Each interval is either assigned one of
// the K physical registers (numbered 0..K-1) or spilled to a spill
// slot (numbered 0, 1, 2, ... in spill order, never reused).
package regalloc

import (
	"errors"
	"fmt"
	"sync"
)

// Distinguishable rejection reasons, testable with errors.Is.
var (
	// ErrInvalidRegisterCount is returned by New when K < 1.
	ErrInvalidRegisterCount = errors.New("regalloc: register count must be >= 1")
	// ErrDuplicateID is returned by Add when the interval id already exists.
	ErrDuplicateID = errors.New("regalloc: interval id already exists")
	// ErrInvalidRange is returned by Add when start >= end.
	ErrInvalidRange = errors.New("regalloc: start must be strictly less than end")
	// ErrStartRegression is returned by Add when start is less than the
	// start of the previously accepted interval.
	ErrStartRegression = errors.New("regalloc: start regresses below the last accepted start")
	// ErrNotFound is returned by Query for an unknown interval id.
	ErrNotFound = errors.New("regalloc: interval id not found")
)

// Assignment is the outcome for one interval: exactly one of Register
// or Slot is meaningful, selected by Spilled.
type Assignment struct {
	Spilled  bool // true if the interval was spilled
	Register int  // physical register 0..K-1, valid when !Spilled
	Slot     int  // spill slot 0,1,2,..., valid when Spilled
}

// interval is the internal bookkeeping for one accepted interval.
type interval struct {
	id      int
	start   int
	end     int
	reg     int  // register held, -1 when spilled
	slot    int  // spill slot, valid only when spilled
	spilled bool // true once the interval has been spilled
}

// Allocator is a linear-scan register allocator safe for concurrent use.
type Allocator struct {
	mu        sync.Mutex
	k         int
	free      []bool            // free[r] reports register r available
	intervals map[int]*interval // every accepted interval, by id
	active    map[int]*interval // live, register-holding intervals, by id
	nextSlot  int               // next spill slot to hand out
	lastStart int               // start of the last accepted interval
	hasLast   bool              // whether any interval was accepted
}

// New returns an Allocator with k physical registers numbered 0..k-1.
// It fails with ErrInvalidRegisterCount when k < 1.
func New(k int) (*Allocator, error) {
	if k < 1 {
		return nil, ErrInvalidRegisterCount
	}
	a := &Allocator{
		k:         k,
		free:      make([]bool, k),
		intervals: make(map[int]*interval),
		active:    make(map[int]*interval),
	}
	for r := range a.free {
		a.free[r] = true
	}
	return a, nil
}

// Add registers the half-open interval [start, end) with the given id
// and returns its assignment. It validates, in order: duplicate id,
// start < end, start not before the last accepted start. A rejected
// Add changes no state.
func (a *Allocator) Add(id, start, end int) (Assignment, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if _, dup := a.intervals[id]; dup {
		return Assignment{}, fmt.Errorf("%w: id=%d", ErrDuplicateID, id)
	}
	if start >= end {
		return Assignment{}, fmt.Errorf("%w: id=%d [%d,%d)", ErrInvalidRange, id, start, end)
	}
	if a.hasLast && start < a.lastStart {
		return Assignment{}, fmt.Errorf("%w: id=%d start=%d last=%d", ErrStartRegression, id, start, a.lastStart)
	}

	// Expire every active interval whose end <= start. An interval
	// ending exactly at start is already dead, so its register is
	// reusable immediately.
	for aid, iv := range a.active {
		if iv.end <= start {
			a.free[iv.reg] = true
			delete(a.active, aid)
		}
	}

	iv := &interval{id: id, start: start, end: end, reg: -1, slot: -1}

	if reg := a.lowestFree(); reg >= 0 {
		a.free[reg] = false
		iv.reg = reg
		a.active[id] = iv
	} else {
		// No free register: spill the interval with the greatest end
		// among the active set plus the new interval. Ties against an
		// active interval spill the new one; ties among active
		// intervals spill the smallest id.
		victim := a.spillVictim()
		if victim == nil || end >= victim.end {
			iv.spilled = true
			iv.slot = a.takeSlot()
		} else {
			victim.spilled = true
			victim.slot = a.takeSlot()
			iv.reg = victim.reg
			victim.reg = -1
			delete(a.active, victim.id)
			a.active[id] = iv
		}
	}

	a.intervals[id] = iv
	a.lastStart = start
	a.hasLast = true
	return assignmentOf(iv), nil
}

// Query returns the current assignment for id. An interval whose
// register was transferred to a newer interval reports its spill slot.
// Unknown ids fail with ErrNotFound.
func (a *Allocator) Query(id int) (Assignment, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	iv, ok := a.intervals[id]
	if !ok {
		return Assignment{}, fmt.Errorf("%w: id=%d", ErrNotFound, id)
	}
	return assignmentOf(iv), nil
}

// lowestFree returns the smallest-numbered free register, or -1.
func (a *Allocator) lowestFree() int {
	for r := 0; r < a.k; r++ {
		if a.free[r] {
			return r
		}
	}
	return -1
}

// spillVictim picks the active interval with the greatest end, breaking
// ties by smallest id. Callers compare its end against the new
// interval's end to decide whether the new interval spills itself.
func (a *Allocator) spillVictim() *interval {
	var victim *interval
	for _, iv := range a.active {
		if victim == nil || iv.end > victim.end || (iv.end == victim.end && iv.id < victim.id) {
			victim = iv
		}
	}
	return victim
}

// takeSlot hands out the next spill slot. Slots are never reused.
func (a *Allocator) takeSlot() int {
	slot := a.nextSlot
	a.nextSlot++
	return slot
}

// assignmentOf reports the current outcome for iv: its spill slot once
// spilled, otherwise the register it holds.
func assignmentOf(iv *interval) Assignment {
	if iv.spilled {
		return Assignment{Spilled: true, Slot: iv.slot}
	}
	return Assignment{Register: iv.reg}
}
