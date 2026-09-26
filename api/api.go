// Package api exposes the single-decree Paxos acceptor state machine.
package api

import (
	"errors"
	"reflect"
	"sync"

	"ontology/acpt"
	"ontology/quorum"
)

// A stale Accept (n < promised) is a normal refusal reported as (false, nil).
var ErrAcceptorIndex = errors.New("paxos: acceptor index out of range")
var ErrProposalNumber = errors.New("paxos: proposal number must be positive")
var ErrStalePrepare = errors.New("paxos: prepare number not greater than promised")

type Paxos struct {
	mu        sync.RWMutex
	acceptors []*acpt.Acceptor
	tally     *quorum.Tally
	majority  int
}

// New builds an instance of n acceptors (n odd by protocol convention).
func New(n int) *Paxos {
	as := make([]*acpt.Acceptor, n)
	for i := range as {
		as[i] = acpt.New()
	}
	return &Paxos{acceptors: as, tally: quorum.New(n), majority: n/2 + 1}
}
func (p *Paxos) check(acc, n int) error {
	if acc < 0 || acc >= len(p.acceptors) {
		return ErrAcceptorIndex
	}
	if n <= 0 {
		return ErrProposalNumber
	}
	return nil
}

// Prepare promises only when n > promised and reports what was accepted.
func (p *Paxos) Prepare(acc, n int) (bool, int, int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if e := p.check(acc, n); e != nil {
		return false, 0, 0, e
	}
	if ok, an, av := p.acceptors[acc].Promise(n); !ok {
		return false, 0, 0, ErrStalePrepare
	} else {
		return true, an, av, nil
	}
}

// Accept uses n >= promised inside the acceptor; refusal changes nothing.
func (p *Paxos) Accept(acc, n, v int) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if e := p.check(acc, n); e != nil {
		return false, e
	}
	a := p.acceptors[acc]
	old := a.AcceptedValue()
	if !a.Accept(n, v) {
		return false, nil
	}
	p.tally.Move(old, v)
	return true, nil
}

// Chosen returns the majority value or (0, false); read-locked for readers.
func (p *Paxos) Chosen() (int, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.tally.Chosen()
}
func (p *Paxos) snap() [][3]int {
	s := make([][3]int, len(p.acceptors))
	for i, a := range p.acceptors {
		s[i] = [3]int{a.Promised(), a.Accepted(), a.AcceptedValue()}
	}
	return s
}

// agrees is invariant 1: the incremental tally equals a naive full-table
// recomputation. (Invariant 2's bookkeeping lives in acpt; its test reads
// acceptor fields directly. The spec's literal "accepted>=promised"
// contradicts its own S1, where promised=2 and accepted=0.)
func (p *Paxos) agrees() bool {
	v, c := p.tally.Chosen()
	nv, nc := quorum.Naive(p.acceptors, p.majority)
	return v == nv && c == nc
}

// SelfCheck verifies the four invariants on fresh instances with built-in
// deterministic sequences: the ten mandated steps, a higher round that must
// inherit the chosen value (invariant 3), rejected ops leaving no trace (4).
func (p *Paxos) SelfCheck() bool {
	q := New(3)
	ops := [10][3]int{{0, 2, 0}, {1, 2, 0}, {2, 2, 0}, {0, 2, 10}, {1, 2, 10}, {0, 3, 0}, {1, 3, 0}, {2, 3, 0}, {0, 3, 10}, {1, 3, 10}}
	for i, o := range ops { // Prepare steps are bit mask 231 (S1-S3, S6-S8)
		if (231>>uint(i))&1 == 1 {
			if _, _, _, e := q.Prepare(o[0], o[1]); e != nil {
				return false
			}
		} else if ok, _ := q.Accept(o[0], o[1], o[2]); !ok {
			return false
		}
		if !q.agrees() {
			return false
		}
	}
	rep := []quorum.Report{} // round 4 proposing own value 99 must inherit 10
	for a := 0; a < 3; a++ {
		if ok, an, av, e := q.Prepare(a, 4); ok && e == nil && an > 0 {
			rep = append(rep, quorum.Report{Accepted: an, Value: av, HasValue: true})
		}
	}
	v := quorum.PickValue(rep, 99)
	for a := 0; a < 3; a++ {
		if ok, _ := q.Accept(a, 4, v); !ok {
			return false
		}
	}
	if w, c := q.Chosen(); !q.agrees() || !c || w != 10 {
		return false
	}
	x := New(3) // invariant 4: every rejection leaves state untouched
	_, _, _, _ = x.Prepare(0, 2)
	_, _ = x.Accept(0, 2, 10)
	before := x.snap()
	for _, z := range [][2]int{{-1, 5}, {3, 5}, {0, 0}, {0, 2}} {
		_, _, _, _ = x.Prepare(z[0], z[1])
	}
	for _, z := range [][3]int{{-1, 5, 1}, {3, 5, 1}, {0, 0, 1}} {
		if _, e := x.Accept(z[0], z[1], z[2]); e == nil {
			return false
		}
	}
	if ok, _ := x.Accept(0, 1, 99); ok {
		return false // normal refusal of a stale accept, not a sentinel error
	}
	same := reflect.DeepEqual(x.snap(), before)
	ok2, _, _, e := x.Prepare(1, 2) // instance still usable afterwards
	return same && ok2 && e == nil
}
