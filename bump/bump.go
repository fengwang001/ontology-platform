// Package bump implements the linear (bump) allocator over fixed-size arenas.
// Objects never straddle arena boundaries; only Reset invalidates everything
// and bumps the generation so dead-generation offsets are detectable.
package bump

import (
	"errors"
	"sync"

	"ontology/arena"
)

var (
	ErrInvalidSize    = errors.New("bump: n must be >= 1")
	ErrInvalidAlign   = errors.New("bump: align must be a power of two")
	ErrArenaTooSmall  = errors.New("bump: arenaSize must be >= align")
	ErrArenaExhausted = errors.New("bump: arena pool exhausted")
)

// Allocator is safe for concurrent use.
type Allocator struct {
	arenaSize int
	align     int
	maxArenas int
	cur       int   // arena currently being bumped
	bumps     []int // bump per arena; unopened slots stay 0, len == maxArenas
	gen       int   // current generation; 0 after New, +1 per Reset
	probe     int   // arenas examined by the most recent Alloc (unexported)
	mu        sync.Mutex
}

// New validates before creating any state, so a rejected build leaves nothing.
func New(arenaSize, align, maxArenas int) (*Allocator, error) {
	if !arena.IsPowerOfTwo(align) {
		return nil, ErrInvalidAlign
	}
	if arenaSize < align {
		return nil, ErrArenaTooSmall
	}
	if maxArenas < 1 {
		return nil, ErrArenaExhausted
	}
	return &Allocator{
		arenaSize: arenaSize, align: align, maxArenas: maxArenas,
		bumps: make([]int, maxArenas),
	}, nil
}

// Alloc returns (global offset, current generation). A rejected call changes
// no state: bumps, opened arenas and generation all stay put.
func (a *Allocator) Alloc(n int) (off, gen int, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if n < 1 {
		a.probe = 0
		return 0, a.gen, ErrInvalidSize
	}
	sz := arena.AlignUp(n, a.align)
	// Only the current arena is examined (no gap scan), so probe == 1.
	a.probe = 1
	b := a.bumps[a.cur]
	if arena.Fits(b, sz, a.arenaSize) {
		a.bumps[a.cur] = b + sz
		return arena.GlobalOff(a.cur, b, a.arenaSize), a.gen, nil
	}
	if sz > a.arenaSize || a.cur+1 >= a.maxArenas { // tail abandoned; no straddle
		return 0, a.gen, ErrArenaExhausted
	}
	a.cur++
	a.bumps[a.cur] = sz
	return arena.GlobalOff(a.cur, 0, a.arenaSize), a.gen, nil
}

// Reset zeroes every arena, returns to arena 0 and advances the generation,
// invalidating every offset handed out before this call.
func (a *Allocator) Reset() {
	a.mu.Lock()
	defer a.mu.Unlock()
	clear(a.bumps)
	a.cur, a.gen, a.probe = 0, a.gen+1, 0
}

// Valid detects dangling offsets by generation alone; offsets are not read.
func (a *Allocator) Valid(_ int, gen int) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return gen == a.gen
}

// Snapshot returns the current arena index, generation and a copy of each
// opened arena's bump position (diagnostics; never exposes probe).
func (a *Allocator) Snapshot() (cur, gen int, bumps []int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cur, a.gen, append([]int(nil), a.bumps[:a.cur+1]...)
}

// Op is one scripted step: either Reset or Alloc(N).
type Op struct {
	Reset bool
	N     int
}

// Outcome mirrors one Alloc result of a scripted sequence.
type Outcome struct {
	Off, Gen int
	Err      error
}

// NaiveSim is the independent naive reference (align, never straddle, abandon
// tails), written as plainly as possible for cross-checking the allocator.
func NaiveSim(arenaSize, align, maxArenas int, ops []Op) []Outcome {
	b := []int{0}
	cur, opened, gen := 0, 1, 0
	r := make([]Outcome, 0, len(ops))
	for _, op := range ops {
		if op.Reset {
			clear(b)
			cur, gen = 0, gen+1
			continue
		}
		if op.N < 1 {
			r = append(r, Outcome{Gen: gen, Err: ErrInvalidSize})
			continue
		}
		sz := arena.AlignUp(op.N, align)
		if b[cur]+sz <= arenaSize {
			r = append(r, Outcome{Off: cur*arenaSize + b[cur], Gen: gen})
			b[cur] += sz
			continue
		}
		if sz > arenaSize || cur+1 >= maxArenas {
			r = append(r, Outcome{Gen: gen, Err: ErrArenaExhausted})
			continue
		}
		cur++
		if cur == opened {
			b, opened = append(b, 0), opened+1
		}
		b[cur] = sz
		r = append(r, Outcome{Off: cur * arenaSize, Gen: gen})
	}
	return r
}
