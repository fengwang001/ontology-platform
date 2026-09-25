// Package api is the public facade for the in-memory Ricart–Agrawala
// mutual exclusion simulation. It depends only on ra (which depends on ord).
package api

import (
	"errors"

	"ontology/ra"
)

// Sentinel errors; one distinct, decidable error per failure mode.
var (
	ErrPIDOutOfRange    = ra.ErrPIDOutOfRange
	ErrDuplicateRequest = ra.ErrDuplicateRequest
	ErrNotInterested    = ra.ErrNotInterested
	ErrNegativeTS       = ra.ErrNegativeTS
)

// Group is a fixed set of n processes sharing one memory-resident state.
type Group struct {
	sys *ra.System
	n   int
}

// New creates n processes, all disinterested.
func New(n int) *Group {
	return &Group{sys: ra.NewSystem(n), n: n}
}

// Request broadcasts pid's REQUEST(ts, pid). It fails atomically on an
// out-of-range pid, a negative timestamp, or a duplicate pending request.
func (g *Group) Request(pid, ts int) error { return g.sys.Request(pid, ts) }

// Resolve settles OK/defer for every pair of current requests.
func (g *Group) Resolve() { g.sys.Resolve() }

// Enterable reports whether pid currently holds every OK it needs.
func (g *Group) Enterable(pid int) (bool, error) { return g.sys.Enterable(pid) }

// Exit ends pid's critical-section visit and releases its deferred set.
func (g *Group) Exit(pid int) error { return g.sys.Leave(pid) }

// enterablePids returns every currently enterable pid.
func (g *Group) enterablePids() []int {
	var got []int
	for pid := 0; pid < g.n; pid++ {
		if ok, err := g.Enterable(pid); err == nil && ok {
			got = append(got, pid)
		}
	}
	return got
}

// SelfCheck runs the built-in sequence P2:3, P1:5, P3:5 over a fresh group
// and verifies the four invariants: mutex, total-order entry, no deadlock,
// and atomic failure of every rejected operation.
func (g *Group) SelfCheck() error {
	s := New(4)
	for _, r := range []struct{ pid, ts int }{{2, 3}, {1, 5}, {3, 5}} {
		if err := s.Request(r.pid, r.ts); err != nil {
			return err
		}
	}
	s.Resolve()
	for _, w := range []int{2, 1, 3} {
		cur := s.enterablePids()
		if len(cur) != 1 { // invariants 1 (mutex) and 3 (no deadlock)
			return errors.New("selfcheck: expected exactly one enterable process")
		}
		if cur[0] != w { // invariant 2: total-order entry sequence
			return errors.New("selfcheck: entry order deviates from (ts,pid) order")
		}
		if err := s.Exit(cur[0]); err != nil {
			return err
		}
	}
	// Invariant 4: four distinct, decidable errors with no state change.
	bad := New(2)
	eBadPID := bad.Request(7, 0)
	eNegTS := bad.Request(0, -9)
	eNotInt := bad.Exit(1)
	if err := bad.Request(0, 1); err != nil {
		return errors.New("selfcheck: group unusable after rejected operations")
	}
	eDup := bad.Request(0, 2)
	if eBadPID != ErrPIDOutOfRange || eNegTS != ErrNegativeTS ||
		eNotInt != ErrNotInterested || eDup != ErrDuplicateRequest ||
		eBadPID == eNegTS || eNegTS == eNotInt || eNotInt == eDup {
		return errors.New("selfcheck: rejection errors missing or not distinct")
	}
	bad.Resolve() // idle pid 1 grants immediately: rejected calls left no trace
	ok, err := bad.Enterable(0)
	if err != nil || !ok {
		return errors.New("selfcheck: state polluted by rejected operations")
	}
	return nil
}
