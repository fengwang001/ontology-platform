// Package api is the concurrency-safe entry point to the in-memory single-decree Paxos acceptor state machine; depends only on quorum.
package api

import (
	"errors"
	"ontology/quorum"
	"reflect"
	"sync"
)

// Distinct, decidable sentinel errors, one per class of bad input.
var (
	ErrInvalidAcceptor = errors.New("paxos: acceptor index out of range")
	ErrInvalidProposal = errors.New("paxos: proposal number must be positive")
	ErrStalePrepare    = errors.New("paxos: stale prepare, cannot promise again")
)

// Paxos is one single-decree instance backed by process memory.
type Paxos struct {
	mu sync.RWMutex
	cl *quorum.Cluster
}

// New creates an instance of n acceptors; n must be positive and odd.
func New(n int) *Paxos {
	if n <= 0 || n%2 == 0 {
		panic("api: acceptor count must be a positive odd integer")
	}
	return &Paxos{cl: quorum.New(n)}
}

// Prepare sends Prepare(acc,n); it returns the accepted (proposal,value), or a sentinel on stale/illegal input without changing state.
func (p *Paxos) Prepare(acc, n int) (int, int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if acc < 0 || acc >= p.cl.N() {
		return 0, 0, ErrInvalidAcceptor
	}
	if n <= 0 {
		return 0, 0, ErrInvalidProposal
	}
	ok, r := p.cl.Prepare(acc, n)
	if !ok {
		return 0, 0, ErrStalePrepare
	}
	return r.AcceptedProposal, r.AcceptedValue, nil
}

// Accept sends Accept(acc,n,v); illegal input gives a sentinel, a normal refusal (n<promised) gives (false,nil); neither changes state.
func (p *Paxos) Accept(acc, n, v int) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if acc < 0 || acc >= p.cl.N() {
		return false, ErrInvalidAcceptor
	}
	if n <= 0 {
		return false, ErrInvalidProposal
	}
	return p.cl.Accept(acc, n, v), nil
}

// Chosen reports the majority-held value, or (0,false); read-locked.
func (p *Paxos) Chosen() (int, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.cl.Chosen()
}

type snapshot [3]int // promised, accepted, acceptedValue
func (p *Paxos) dump() []snapshot {
	s := make([]snapshot, p.cl.N())
	for i := range s {
		pr, ap, av := p.cl.SnapshotAt(i)
		s[i] = snapshot{pr, ap, av}
	}
	return s
}

// SelfCheck verifies invariants 1-4: naive-rescan agreement (1); promised>=accepted and monotonic (2); at most one chosen (3); no-trace rejections with distinct sentinels (4).
func (p *Paxos) SelfCheck() bool {
	if ErrInvalidAcceptor == ErrInvalidProposal || ErrInvalidAcceptor == ErrStalePrepare || ErrInvalidProposal == ErrStalePrepare {
		return false
	}
	c := quorum.New(3)
	// Ten-step NOTES scenario, then stale attempts, then one fresh round.
	ops := [][4]int{
		{'p', 0, 2, 0}, {'p', 1, 2, 0}, {'p', 2, 2, 0}, {'a', 0, 2, 10}, {'a', 1, 2, 10}, {'p', 0, 3, 0}, {'p', 1, 3, 0},
		{'p', 2, 3, 0}, {'a', 0, 3, 10}, {'a', 1, 3, 10}, {'p', 0, 3, 0}, {'a', 2, 2, 99}, {'p', 2, 4, 0}, {'a', 2, 4, 20},
	}
	prev, maj := []int{0, 0, 0}, c.N()/2+1
	for _, o := range ops {
		if o[0] == 'p' {
			c.Prepare(o[1], o[2])
		} else {
			c.Accept(o[1], o[2], o[3])
		}
		votes, atMaj, nv := map[int]int{}, 0, 0
		for i := 0; i < c.N(); i++ { // naive full rescan
			pr, ap, av := c.SnapshotAt(i)
			if pr < ap || pr < prev[i] {
				return false // invariant 2
			}
			prev[i] = pr
			if ap > 0 {
				votes[av]++
				if votes[av] == maj {
					atMaj, nv = atMaj+1, av // a value just crossed majority
				}
			}
		}
		if atMaj > 1 {
			return false // invariant 3
		}
		if cv, _ := c.Chosen(); cv != nv {
			return false // invariant 1
		}
	}
	// Invariant 4: baseline, then each bad call hits its sentinel and leaves the dump unchanged; a normal refusal leaves no trace either.
	q := New(3)
	if _, _, e := q.Prepare(0, 2); e != nil {
		return false
	}
	if ok, e := q.Accept(0, 2, 10); !ok || e != nil {
		return false
	}
	if _, _, e := q.Prepare(1, 3); e != nil {
		return false
	}
	before := q.dump()
	bad := []struct {
		want error
		call func() error
	}{
		{ErrInvalidAcceptor, func() error { _, _, e := q.Prepare(-1, 1); return e }},
		{ErrInvalidProposal, func() error { _, _, e := q.Prepare(2, 0); return e }},
		{ErrStalePrepare, func() error { _, _, e := q.Prepare(0, 2); return e }},
		{ErrInvalidAcceptor, func() error { _, e := q.Accept(3, 1, 1); return e }},
		{ErrInvalidProposal, func() error { _, e := q.Accept(2, -1, 1); return e }},
	}
	for _, b := range bad {
		if !errors.Is(b.call(), b.want) || !reflect.DeepEqual(q.dump(), before) {
			return false
		}
	}
	if ok, e := q.Accept(1, 2, 99); ok || e != nil || !reflect.DeepEqual(q.dump(), before) {
		return false
	}
	ok, e := q.Accept(1, 3, 77) // instance stays usable afterwards
	return ok && e == nil
}
