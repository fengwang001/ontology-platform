// Package api is the public surface for weighted-median insertion and
// lookup. It depends only on median (which depends on wmid); the
// dependency direction never reverses.
package api

import (
	"errors"
	"fmt"
	"sort"
	"sync"

	"ontology/median"
	"ontology/wmid"
)

// Distinct, decidable sentinel errors (aliases keep a single identity).
var (
	ErrNonPositiveWeight = wmid.ErrNonPositiveWeight
	ErrDuplicateValue    = wmid.ErrDuplicateValue
	ErrEmpty             = median.ErrEmpty
)

// Container is the in-memory, concurrency-safe service.
type Container struct {
	mu sync.RWMutex
	f  median.Finder
}

// New returns an empty container.
func New() *Container { return &Container{} }

// Insert adds one element. A rejected insert (weight <= 0 or duplicate
// value) fails as a whole and leaves tree and total unchanged.
func (c *Container) Insert(value, weight int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.f.Insert(value, weight)
}

// Median returns the current lower weighted median; ErrEmpty if no
// elements have been inserted.
func (c *Container) Median() (int64, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.f.Median()
}

// Total returns W, the sum of inserted weights.
func (c *Container) Total() int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.f.Total()
}

// SelfCheck verifies the four invariants over built-in insertion
// sequences and returns nil iff they all hold. It uses only local state,
// so it never touches (or locks) the caller's container and is safe to
// call concurrently with anything.
func (c *Container) SelfCheck() error {
	if _, err := new(median.Finder).Median(); !errors.Is(err, ErrEmpty) {
		return errors.New("selfcheck: empty set did not report ErrEmpty")
	}
	cases := [][][2]int64{
		{{30, 3}, {10, 3}, {40, 3}, {20, 3}}, // canonical NOTES case -> 20
		{{1, 1}},
		{{5, 100}, {1, 1}, {9, 1}, {-3, 1}, {7, 7}},
		{{10, 1}, {20, 1}, {30, 1}, {40, 1}, {50, 1}, {60, 1}},
	}
	for i, seq := range cases {
		var f median.Finder
		elems := make([][2]int64, 0, len(seq))
		for _, e := range seq {
			if err := f.Insert(e[0], e[1]); err != nil {
				return err
			}
			elems = append(elems, e)
		}
		got, err := f.Median()
		if err != nil {
			return err
		}
		if want := naiveMedian(elems); got != want {
			return fmt.Errorf("selfcheck: case %d got median %d want %d", i, got, want)
		}
		// Invariants 1 (two-sided bounds) and 2 (minimality), explicitly.
		var below, above int64
		for _, e := range elems {
			switch {
			case e[0] < got:
				below += e[1]
			case e[0] > got:
				above += e[1]
			}
		}
		if w := f.Total(); 2*below > w || 2*above > w || 2*below >= w {
			return fmt.Errorf("selfcheck: case %d bounds/minimality violated", i)
		}
	}

	// Invariant 4: rejected inserts leave tree and total untouched.
	var g median.Finder
	g.Insert(3, 2)
	g.Insert(1, 4)
	wBefore := g.Total()
	mBefore, _ := g.Median()
	if err := g.Insert(1, 0); !errors.Is(err, ErrNonPositiveWeight) {
		return fmt.Errorf("selfcheck: bad weight err = %v", err)
	}
	if err := g.Insert(1, 2); !errors.Is(err, ErrDuplicateValue) {
		return fmt.Errorf("selfcheck: duplicate err = %v", err)
	}
	if g.Total() != wBefore {
		return errors.New("selfcheck: rejected insert changed total")
	}
	if m, _ := g.Median(); m != mBefore {
		return errors.New("selfcheck: rejected insert changed median")
	}
	return nil
}

// naiveMedian is the O(m log m) reference: sort by value, scan prefix
// weights, return the first value with 2*P >= W (== P >= W/2 over reals).
func naiveMedian(elems [][2]int64) int64 {
	s := make([][2]int64, len(elems))
	copy(s, elems)
	sort.Slice(s, func(i, j int) bool { return s[i][0] < s[j][0] })
	var w int64
	for _, e := range s {
		w += e[1]
	}
	var p int64
	for _, e := range s {
		p += e[1]
		if 2*p >= w {
			return e[0]
		}
	}
	return s[len(s)-1][0]
}
