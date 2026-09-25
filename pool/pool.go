// Package pool is the buffer pool itself: pin/unpin/mark-dirty,
// page-to-frame location, eviction, and sentinel errors.
package pool

import (
	"errors"
	"sync"

	"ontology/frame"
)

// Sentinel errors, mutually distinct, all decidable with errors.Is.
var (
	ErrBadNumFrames = errors.New("pool: numFrames must be >= 1")
	ErrBadFrame     = errors.New("pool: frame does not exist")
	ErrNotPinned    = errors.New("pool: frame pin count is already zero")
	ErrPoolFull     = errors.New("pool: no empty or evictable frame")
)

// Pool is a fixed set of frames caching pages. Safe for concurrent use.
type Pool struct {
	mu      sync.Mutex
	frames  []frame.Frame
	loc     map[int]int // pageID -> frame index, O(1) residency lookup
	writes  frame.WriteCounter
	checked int // frames inspected by the most recent Pin (unexported on purpose)
}

// New creates a pool with numFrames empty frames.
func New(numFrames int) (*Pool, error) {
	if numFrames < 1 {
		return nil, ErrBadNumFrames
	}
	p := &Pool{frames: make([]frame.Frame, numFrames), loc: make(map[int]int, numFrames)}
	for i := range p.frames {
		p.frames[i] = frame.New()
	}
	return p, nil
}

// Pin returns the frame holding pageID, loading and evicting if needed.
// All validation happens before any state change: failures leave no trace.
func (p *Pool) Pin(pageID int) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if idx, ok := p.loc[pageID]; ok { // direct map hit: O(1), no scan
		p.checked = 1
		p.frames[idx].Pin++
		return idx, nil
	}
	idx, checked := p.firstEmpty()
	evicting := false
	if idx < 0 {
		idx, checked = p.firstEvictable()
		if idx < 0 {
			p.checked = checked
			return -1, ErrPoolFull
		}
		evicting = true
	}
	p.checked = checked
	if evicting {
		victim := p.frames[idx]
		if victim.Dirty {
			p.writes.Add()
		}
		delete(p.loc, victim.PageID)
	}
	p.frames[idx] = frame.Frame{PageID: pageID, Pin: 1}
	p.loc[pageID] = idx
	return idx, nil
}

// Unpin decrements the pin count of frame f.
func (p *Pool) Unpin(f int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if f < 0 || f >= len(p.frames) {
		return ErrBadFrame
	}
	if p.frames[f].Pin == 0 {
		return ErrNotPinned
	}
	p.frames[f].Pin--
	return nil
}

// MarkDirty marks frame f dirty.
func (p *Pool) MarkDirty(f int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if f < 0 || f >= len(p.frames) {
		return ErrBadFrame
	}
	p.frames[f].Dirty = true
	return nil
}

// Writes returns the accumulated number of dirty-page write-backs.
func (p *Pool) Writes() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.writes.Get()
}

// ResidentCount returns how many frames currently hold a page.
func (p *Pool) ResidentCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.loc)
}

// Snapshot returns a copy of all frame states.
func (p *Pool) Snapshot() []frame.Frame {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]frame.Frame, len(p.frames))
	copy(out, p.frames)
	return out
}

// firstEmpty returns the lowest-index empty frame and frames inspected.
func (p *Pool) firstEmpty() (int, int) {
	for i, f := range p.frames {
		if f.Empty() {
			return i, i + 1
		}
	}
	return -1, len(p.frames)
}

// firstEvictable returns the lowest-index evictable (pin==0) frame.
func (p *Pool) firstEvictable() (int, int) {
	for i, f := range p.frames {
		if f.Evictable() {
			return i, i + 1
		}
	}
	return -1, len(p.frames)
}
