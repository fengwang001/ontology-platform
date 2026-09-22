// Package reclaim decides which correlation IDs may be reused and in what
// order. It has no dependency on any other package and holds no notion of
// time or replies: it only tracks integer IDs in [0, capacity).
//
// Reuse order is fixed and documented: smallest available ID first. IDs that
// were never used, IDs that were released, and IDs left by finished
// generations are indistinguishable to this pool; the correlator keeps the
// generation (epoch) information needed to reject late replies safely.
package reclaim

import "errors"

// ErrExhausted is returned by Acquire when every ID is currently held.
var ErrExhausted = errors.New("reclaim: no ID available (capacity reached)")

// Pool is a fixed-size set of integer IDs. The zero value is not usable;
// create one with New.
type Pool struct {
	held []bool
	size int
}

// New creates a pool whose IDs range from 0 to capacity-1. A non-positive
// capacity yields a pool that rejects every acquisition.
func New(capacity int) *Pool {
	if capacity < 0 {
		capacity = 0
	}
	return &Pool{held: make([]bool, capacity)}
}

// Capacity reports the fixed capacity.
func (p *Pool) Capacity() int { return len(p.held) }

// Size reports how many IDs are currently held.
func (p *Pool) Size() int { return p.size }

// Available reports whether at least one ID can be acquired.
func (p *Pool) Available() bool { return p.size < len(p.held) }

// Acquire reserves and returns the smallest currently free ID.
// It returns ErrExhausted when the pool is full, changing nothing.
func (p *Pool) Acquire() (int, error) {
	for id := range p.held {
		if !p.held[id] {
			p.held[id] = true
			p.size++
			return id, nil
		}
	}
	return 0, ErrExhausted
}

// Release returns an ID to the pool. Releasing an out-of-range ID or an ID
// that is not held is an error and leaves the pool unchanged.
func (p *Pool) Release(id int) error {
	if id < 0 || id >= len(p.held) || !p.held[id] {
		return ErrNotHeld
	}
	p.held[id] = false
	p.size--
	return nil
}

// Held reports whether an ID is currently reserved.
func (p *Pool) Held(id int) bool {
	if id < 0 || id >= len(p.held) {
		return false
	}
	return p.held[id]
}
