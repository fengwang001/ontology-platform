// Package api is the public face of the buffer pool.
package api

import (
	"fmt"
	"math/rand"
	"slices"

	"ontology/frame"
	"ontology/pool"
)

var ( // re-exported sentinel errors
	ErrBadFrame  = pool.ErrBadFrame
	ErrNotPinned = pool.ErrNotPinned
	ErrPoolFull  = pool.ErrPoolFull
)

// Pool wraps the internal pool with the public API.
type Pool struct{ p *pool.Pool }

// New creates a pool with numFrames frames (numFrames >= 1).
func New(numFrames int) (*Pool, error) {
	p, err := pool.New(numFrames)
	if err != nil {
		return nil, err
	}
	return &Pool{p: p}, nil
}

func (a *Pool) Pin(pageID int) (int, error) { return a.p.Pin(pageID) }
func (a *Pool) Unpin(f int) error           { return a.p.Unpin(f) }
func (a *Pool) MarkDirty(f int) error       { return a.p.MarkDirty(f) }
func (a *Pool) Writes() int                 { return a.p.Writes() }
func (a *Pool) Snapshot() []frame.Frame     { return a.p.Snapshot() }
func (a *Pool) ResidentCount() int          { return a.p.ResidentCount() }

// naive is a deliberately simple reference model: linear scans, no map.
type naive struct {
	fr     []frame.Frame
	writes int
}

func newNaive(n int) *naive {
	m := &naive{fr: make([]frame.Frame, n)}
	for i := range m.fr {
		m.fr[i] = frame.New()
	}
	return m
}
func (m *naive) first(pred func(frame.Frame) bool) int {
	for i, f := range m.fr {
		if pred(f) {
			return i
		}
	}
	return -1
}
func (m *naive) pin(pageID int) (int, error) {
	for i, f := range m.fr {
		if f.PageID == pageID {
			m.fr[i].Pin++
			return i, nil
		}
	}
	idx := m.first(frame.Frame.Empty)
	if idx < 0 {
		if idx = m.first(frame.Frame.Evictable); idx < 0 {
			return -1, pool.ErrPoolFull
		}
		if m.fr[idx].Dirty {
			m.writes++
		}
	}
	m.fr[idx] = frame.Frame{PageID: pageID, Pin: 1}
	return idx, nil
}
func (m *naive) unpin(f int) error {
	if f < 0 || f >= len(m.fr) {
		return pool.ErrBadFrame
	}
	if m.fr[f].Pin == 0 {
		return pool.ErrNotPinned
	}
	m.fr[f].Pin--
	return nil
}
func (m *naive) markDirty(f int) error {
	if f < 0 || f >= len(m.fr) {
		return pool.ErrBadFrame
	}
	m.fr[f].Dirty = true
	return nil
}

// checkRandom runs one deterministic random op sequence on the real pool
// and the naive model, checking all four invariants after every op.
func checkRandom(seed int64, numFrames, ops int) error {
	a, _ := New(numFrames) // callers always pass numFrames >= 1
	m, r := newNaive(numFrames), rand.New(rand.NewSource(seed))
	consistent := func() error { // invariants 1 (unique) and 2 (naive-equal)
		snap := a.Snapshot()
		if a.Writes() != m.writes || !slices.Equal(snap, m.fr) {
			return fmt.Errorf("diverged from naive model")
		}
		seen := map[int]bool{}
		for _, f := range snap {
			if !f.Empty() && seen[f.PageID] {
				return fmt.Errorf("page %d resident twice", f.PageID)
			}
			seen[f.PageID] = true
		}
		return nil
	}
	for step := 0; step < ops; step++ {
		before, beforeW := a.Snapshot(), a.Writes()
		page, f := r.Intn(2*numFrames+2), r.Intn(numFrames+2)-1
		var errA, errM error
		var fa, fm int
		switch r.Intn(3) {
		case 0:
			fa, errA = a.Pin(page)
			fm, errM = m.pin(page)
		case 1:
			errA, errM = a.Unpin(f), m.unpin(f)
		case 2:
			errA, errM = a.MarkDirty(f), m.markDirty(f)
		}
		if (errA == nil) != (errM == nil) || (errA == nil && fa != fm) {
			return fmt.Errorf("step %d: divergence err=%v/%v frame=%d/%d", step, errA, errM, fa, fm)
		}
		if errA != nil && (!slices.Equal(before, a.Snapshot()) || beforeW != a.Writes()) {
			return fmt.Errorf("step %d: rejected op mutated state", step) // invariant 4
		}
		if err := consistent(); err != nil {
			return fmt.Errorf("step %d: %w", step, err)
		}
	}
	return nil
}

// SelfCheck verifies the four invariants on built-in op sequences.
func (a *Pool) SelfCheck() error {
	for _, c := range [][3]int{{1, 3, 200}, {2, 1, 100}, {3, 7, 400}, {4, 16, 400}} {
		if err := checkRandom(int64(c[0]), c[1], c[2]); err != nil {
			return err
		}
	}
	return nil
}
