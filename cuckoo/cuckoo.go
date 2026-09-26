// Package cuckoo implements a two-table cuckoo hash with alternating evictions.
package cuckoo

import (
	"errors"
	"sync"
	"sync/atomic"

	"ontology/hashk"
)

// Sentinel errors are mutually distinguishable with errors.Is.
var (
	ErrNotFound  = errors.New("cuckoo: key not found")
	ErrDuplicate = errors.New("cuckoo: key already exists")
	ErrTableFull = errors.New("cuckoo: eviction limit exceeded")
	ErrInvalidN  = errors.New("cuckoo: n must be >= 1")
)

// Table is a two-table cuckoo hash with n slots per table.
type Table struct {
	n          int
	maxKicks   int
	mu         sync.RWMutex
	t1, t2     []int
	o1, o2     []bool // occupancy of each slot
	size       int
	lastProbes atomic.Int64 // slots checked by the most recent Lookup (unexported)
}

// New allocates an empty table; n must be >= 1.
func New(n, maxKicks int) (*Table, error) {
	if n < 1 {
		return nil, ErrInvalidN
	}
	return &Table{
		n: n, maxKicks: maxKicks,
		t1: make([]int, n), t2: make([]int, n),
		o1: make([]bool, n), o2: make([]bool, n),
	}, nil
}

// change records one slot's pre-image for rollback.
type change struct {
	inT1, had bool
	i, v      int
}

// put writes v to a slot and appends the pre-image to the journal.
func (t *Table) put(inT1 bool, i, v int, log *[]change) {
	c := change{inT1: inT1, i: i}
	if inT1 {
		c.had, c.v = t.o1[i], t.t1[i]
		t.o1[i], t.t1[i] = true, v
	} else {
		c.had, c.v = t.o2[i], t.t2[i]
		t.o2[i], t.t2[i] = true, v
	}
	*log = append(*log, c)
}

// Insert adds x, walking the alternating eviction chain from T1[h1(x)].
// On ErrTableFull every mutation is replayed backwards: no trace remains.
func (t *Table) Insert(x int) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	p1, p2 := hashk.H1(x, t.n), hashk.H2(x, t.n)
	if (t.o1[p1] && t.t1[p1] == x) || (t.o2[p2] && t.t2[p2] == x) {
		return ErrDuplicate
	}
	var log []change
	undo := func() {
		for i := len(log) - 1; i >= 0; i-- {
			c := log[i]
			if c.inT1 {
				t.o1[c.i], t.t1[c.i] = c.had, c.v
			} else {
				t.o2[c.i], t.t2[c.i] = c.had, c.v
			}
		}
	}
	cur, pos, inT1, kicks := x, p1, true, 0
	for {
		occ := t.o2[pos]
		if inT1 {
			occ = t.o1[pos]
		}
		if !occ {
			t.put(inT1, pos, cur, &log)
			t.size++
			return nil
		}
		if kicks >= t.maxKicks { // this eviction would exceed the bound
			undo()
			return ErrTableFull
		}
		old := t.t1[pos]
		if !inT1 {
			old = t.t2[pos]
		}
		t.put(inT1, pos, cur, &log)
		kicks++
		cur = old
		if inT1 {
			pos = hashk.H2(cur, t.n) // displaced T1 key tries its h2 slot
		} else {
			pos = hashk.H1(cur, t.n)
		}
		inT1 = !inT1
	}
}

// Lookup checks exactly the two candidate slots; missing keys are ErrNotFound.
func (t *Table) Lookup(x int) (bool, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	t.lastProbes.Store(2)
	p1, p2 := hashk.H1(x, t.n), hashk.H2(x, t.n)
	if (t.o1[p1] && t.t1[p1] == x) || (t.o2[p2] && t.t2[p2] == x) {
		return true, nil
	}
	return false, ErrNotFound
}

// Delete clears whichever candidate slot holds x; missing keys are ErrNotFound.
func (t *Table) Delete(x int) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	p1 := hashk.H1(x, t.n)
	if t.o1[p1] && t.t1[p1] == x {
		t.o1[p1], t.t1[p1] = false, 0
		t.size--
		return nil
	}
	p2 := hashk.H2(x, t.n)
	if t.o2[p2] && t.t2[p2] == x {
		t.o2[p2], t.t2[p2] = false, 0
		t.size--
		return nil
	}
	return ErrNotFound
}

// Len returns the number of stored keys.
func (t *Table) Len() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.size
}
