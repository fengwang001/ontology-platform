// Package topk implements a streaming, fixed-capacity Top-K selector.
//
// Elements are (ID, Score) pairs pushed one at a time. At any moment a
// Snapshot returns the current top K elements in a strict composite order:
// the Score dimension follows the configured direction (Desc keeps the
// largest K, Asc keeps the smallest K), while ties on Score are always
// broken by ascending lexicographic ID, regardless of direction.
package topk

import (
	"errors"
	"sync"
)

// Direction selects which end of the Score axis is kept.
type Direction int

const (
	// Desc keeps the K elements with the largest scores.
	Desc Direction = iota
	// Asc keeps the K elements with the smallest scores.
	Asc
)

// ErrNonPositiveCapacity is returned by New when k <= 0.
var ErrNonPositiveCapacity = errors.New("topk: capacity K must be positive")

// Element is a single ranked item.
type Element struct {
	ID    string
	Score float64
}

// Selector is a streaming fixed-capacity Top-K container.
// It is safe for concurrent use.
type Selector struct {
	mu      sync.RWMutex
	dir     Direction
	k       int
	items   []Element // kept fully sorted by less(); len(items) <= k
	index   map[string]int
	skipped int64
}

// New returns a Selector holding at most k elements.
// It returns ErrNonPositiveCapacity when k <= 0.
func New(k int, dir Direction) (*Selector, error) {
	if k <= 0 {
		return nil, ErrNonPositiveCapacity
	}
	return &Selector{
		dir:   dir,
		k:     k,
		items: make([]Element, 0, k),
		index: make(map[string]int),
	}, nil
}

// less reports whether a ranks strictly before b in the snapshot order.
// Score follows the configured direction; equal scores (including +0/-0,
// which compare equal in Go) always break by ascending ID.
func (s *Selector) less(a, b Element) bool {
	if a.Score != b.Score {
		if s.dir == Desc {
			return a.Score > b.Score
		}
		return a.Score < b.Score
	}
	return a.ID < b.ID
}
