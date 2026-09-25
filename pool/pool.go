// Package pool implements the buffer-pool body: pin/unpin/dirty, direct
// pageId→frame lookup, smallest-index eviction and write-back counting.
package pool

import (
	"errors"
	"sync"

	"ontology/frame"
)

// Pairwise-distinct sentinel errors for every rejected operation.
var (
	ErrUnpinNotPinned   = errors.New("pool: unpin on frame with pin 0")
	ErrInvalidFrame     = errors.New("pool: invalid frame index")
	ErrNoEvictableFrame = errors.New("pool: no empty or evictable frame")
)

// FrameSnapshot is an immutable view of one frame's (pageId, dirty, pin).
type FrameSnapshot struct {
	PageID int
	Dirty  bool
	Pin    int
}

// Pool is a fixed-size in-memory buffer pool.
type Pool struct {
	mu     sync.Mutex
	frames []frame.Frame
	index  map[int]int // pageId → frame: direct mapping, never a scan
	writes frame.WriteCounter
	// lastPinChecks counts frames examined by the latest Pin. Unexported;
	// read only by pool's own tests, never through a public method.
	lastPinChecks int
}

// New creates a pool with numFrames >= 1 empty frames (< 1 treated as 1).
func New(numFrames int) *Pool {
	if numFrames < 1 {
		numFrames = 1
	}
	p := &Pool{frames: make([]frame.Frame, numFrames), index: make(map[int]int, numFrames)}
	for i := range p.frames {
		p.frames[i] = frame.New()
	}
	return p
}

func (p *Pool) bad(fr int) bool { return fr < 0 || fr >= len(p.frames) }

// Pin returns and pins the frame caching pageID. A resident page is found by
// direct map lookup; else the smallest empty frame; else the smallest pin-0
// frame is evicted (a dirty victim is written back first). With neither, it
// fails without mutating anything.
func (p *Pool) Pin(pageID int) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastPinChecks = 0
	if fi, ok := p.index[pageID]; ok {
		p.frames[fi].Hold()
		p.lastPinChecks = 1 // only the mapped frame is touched
		return fi, nil
	}
	target := -1
	for i := range p.frames { // smallest empty frame
		p.lastPinChecks++
		if p.frames[i].Empty() {
			target = i
			break
		}
	}
	if target < 0 {
		for i := range p.frames { // smallest pin-0 frame holding a page
			p.lastPinChecks++
			if p.frames[i].Evictable() {
				target = i
				break
			}
		}
	}
	if target < 0 {
		return -1, ErrNoEvictableFrame // nothing mutated: no trace
	}
	f := &p.frames[target]
	if old := f.PageID(); old != frame.EmptyPage {
		f.Flush(&p.writes) // write dirty victim back before overwriting
		delete(p.index, old)
	}
	f.Load(pageID) // freshly loaded: clean, pin 0
	f.Hold()
	p.index[pageID] = target
	return target, nil
}

// Unpin decrements the pin count; fails unchanged on a bad frame or pin==0.
func (p *Pool) Unpin(fr int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.bad(fr) {
		return ErrInvalidFrame
	}
	if !p.frames[fr].Release() {
		return ErrUnpinNotPinned
	}
	return nil
}

// MarkDirty marks the page cached in frame fr as modified.
func (p *Pool) MarkDirty(fr int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.bad(fr) {
		return ErrInvalidFrame
	}
	p.frames[fr].MarkDirty()
	return nil
}

// Writes returns the cumulative number of dirty-page write-backs.
func (p *Pool) Writes() int { p.mu.Lock(); defer p.mu.Unlock(); return p.writes.Get() }

// Snapshot returns an immutable copy of every frame's state.
func (p *Pool) Snapshot() []FrameSnapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]FrameSnapshot, len(p.frames))
	for i, f := range p.frames {
		out[i] = FrameSnapshot{f.PageID(), f.Dirty(), f.Pins()}
	}
	return out
}

// VerifyResidentLookup verifies a resident Pin is a constant-1 direct lookup
// at pool sizes 100/1000/10000; it returns only a verdict, never the counter.
func (p *Pool) VerifyResidentLookup() error {
	for _, m := range []int{100, 1000, 10000} {
		q := New(m)
		for i := 0; i < m; i++ {
			if _, err := q.Pin(i); err != nil {
				return err
			}
		}
		q.lastPinChecks = 0
		if _, err := q.Pin(m / 2); err != nil || q.lastPinChecks != 1 {
			return errors.New("pool: resident lookup examined more than one frame")
		}
	}
	return nil
}
