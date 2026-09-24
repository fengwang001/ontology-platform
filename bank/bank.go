// Package bank is a banker's-algorithm resource gatekeeper: it grants a
// request only when the resulting state is still safe (deadlock-free).
package bank

import (
	"errors"
	"sort"
	"sync"

	"ontology/vec"
)

// MaxProcs is the maximum number of declared jobs.
const MaxProcs = 64

var (
	ErrUnknown      = errors.New("bank: unknown pid")
	ErrFull         = errors.New("bank: job limit reached")
	ErrExceedsClaim = errors.New("bank: exceeds declared claim")
	ErrUnsafe       = errors.New("bank: grant would be unsafe")
	ErrOverRelease  = errors.New("bank: release exceeds allocation")
)

// State is a consistent snapshot of the gatekeeper.
type State struct {
	Total vec.Vec
	Avail vec.Vec
	Max   map[string]vec.Vec
	Alloc map[string]vec.Vec
}

// Bank guards a fixed total pool of resource classes. Safe for concurrent use.
type Bank struct {
	mu    sync.Mutex
	total vec.Vec
	avail vec.Vec
	max   map[string]vec.Vec
	alloc map[string]vec.Vec
	cmps  int // vector comparisons in the last safety check
}

// New creates a Bank whose available pool starts at total.
func New(total vec.Vec) *Bank {
	return &Bank{total: append(vec.Vec(nil), total...), avail: append(vec.Vec(nil), total...),
		max: map[string]vec.Vec{}, alloc: map[string]vec.Vec{}}
}

// Declare registers pid with its maximum claim per resource class.
func (b *Bank) Declare(pid string, max vec.Vec) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(max) != len(b.total) {
		return vec.ErrDim
	}
	if le, _ := vec.LE(max, b.total); !le {
		return ErrExceedsClaim
	}
	if _, ok := b.max[pid]; !ok {
		if len(b.max) >= MaxProcs {
			return ErrFull
		}
		b.alloc[pid] = make(vec.Vec, len(b.total))
	}
	b.max[pid] = append(vec.Vec(nil), max...)
	return nil
}

// Request grants req to pid only if the resulting state stays safe.
func (b *Bank) Request(pid string, req vec.Vec) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.max[pid]; !ok {
		return ErrUnknown
	}
	if len(req) != len(b.total) {
		return vec.ErrDim
	}
	need, _ := vec.Sub(b.max[pid], b.alloc[pid])
	if le, _ := vec.LE(req, need); !le {
		return ErrExceedsClaim
	}
	if le, _ := vec.LE(req, b.avail); !le {
		return ErrUnsafe
	}
	b.avail, _ = vec.Sub(b.avail, req)
	b.alloc[pid], _ = vec.Add(b.alloc[pid], req)
	if !b.safeLocked() {
		b.avail, _ = vec.Add(b.avail, req)
		b.alloc[pid], _ = vec.Sub(b.alloc[pid], req)
		return ErrUnsafe
	}
	return nil
}

// Release returns rel of pid's allocation to the pool.
func (b *Bank) Release(pid string, rel vec.Vec) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.max[pid]; !ok {
		return ErrUnknown
	}
	if len(rel) != len(b.total) {
		return vec.ErrDim
	}
	if le, _ := vec.LE(rel, b.alloc[pid]); !le {
		return ErrOverRelease
	}
	b.alloc[pid], _ = vec.Sub(b.alloc[pid], rel)
	b.avail, _ = vec.Add(b.avail, rel)
	return nil
}

// Finish returns pid's whole allocation to the pool.
func (b *Bank) Finish(pid string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if a, ok := b.alloc[pid]; ok {
		b.avail, _ = vec.Add(b.avail, a)
		b.alloc[pid] = make(vec.Vec, len(b.total))
	}
}

// Snapshot returns a deep copy of the current state.
func (b *Bank) Snapshot() State {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.stateLocked()
}

// Safe runs the greedy safety check on the current state.
func (b *Bank) Safe() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.safeLocked()
}

// LastCmps returns the comparison count of the last safety check.
func (b *Bank) LastCmps() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.cmps
}

func (b *Bank) stateLocked() State {
	s := State{Total: append(vec.Vec(nil), b.total...), Avail: append(vec.Vec(nil), b.avail...),
		Max: map[string]vec.Vec{}, Alloc: map[string]vec.Vec{}}
	for p := range b.max {
		s.Max[p] = append(vec.Vec(nil), b.max[p]...)
		s.Alloc[p] = append(vec.Vec(nil), b.alloc[p]...)
	}
	return s
}

func (b *Bank) safeLocked() bool {
	safe, cmps := CheckState(b.stateLocked())
	b.cmps = cmps
	return safe
}

// CheckState greedily finishes any job whose need fits work; by the
// monotonicity argument in NOTES.md this needs no backtracking.
func CheckState(s State) (safe bool, cmps int) {
	work := append(vec.Vec(nil), s.Avail...)
	pids := make([]string, 0, len(s.Max))
	for p := range s.Max {
		pids = append(pids, p)
	}
	sort.Strings(pids)
	done := map[string]bool{}
	for left := len(pids); left > 0; {
		progress := false
		for _, p := range pids {
			if done[p] {
				continue
			}
			need, _ := vec.Sub(s.Max[p], s.Alloc[p])
			cmps++
			if le, _ := vec.LE(need, work); le {
				work, _ = vec.Add(work, s.Alloc[p])
				done[p] = true
				left--
				progress = true
			}
		}
		if !progress {
			return false, cmps
		}
	}
	return true, cmps
}
