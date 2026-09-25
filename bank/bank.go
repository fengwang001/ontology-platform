package bank

import (
	"errors"
	"maps"
	"sync"

	"ontology/vec"
)

var ErrUnknown, ErrFull = errors.New("bank: unknown process"), errors.New("bank: process table full")
var ErrExceedsClaim, ErrOverRelease = errors.New("bank: request exceeds remaining claim"), errors.New("bank: release exceeds allocation")
var ErrUnsafe = errors.New("bank: request would leave state unsafe")

const MaxProcs = 64

type Bank struct {
	mu         sync.Mutex
	avail      vec.V
	max, alloc map[int]vec.V
	cmp        int
}

func New(total vec.V) *Bank {
	return &Bank{avail: append(vec.V(nil), total...), max: map[int]vec.V{}, alloc: map[int]vec.V{}}
}
func (b *Bank) lock() func() { b.mu.Lock(); return b.mu.Unlock }
func (b *Bank) Declare(pid int, max vec.V) error {
	defer b.lock()()
	if len(b.max) >= MaxProcs {
		return ErrFull
	}
	b.max[pid], b.alloc[pid] = append(vec.V(nil), max...), make(vec.V, len(b.avail))
	return nil
}
func (b *Bank) Request(pid int, req vec.V) error {
	defer b.lock()()
	if _, ok := b.max[pid]; !ok {
		return ErrUnknown
	}
	need, _ := vec.Sub(b.max[pid], b.alloc[pid])
	if fits, _ := vec.LE(req, need); !fits {
		return ErrExceedsClaim
	}
	avail, _ := vec.Sub(b.avail, req)
	alloc := maps.Clone(b.alloc)
	alloc[pid], _ = vec.Add(alloc[pid], req)
	if !safe(avail, b.max, alloc, &b.cmp) {
		return ErrUnsafe
	}
	b.avail, b.alloc = avail, alloc
	return nil
}
func (b *Bank) Release(pid int, rel vec.V) error {
	defer b.lock()()
	alloc, ok := b.alloc[pid]
	if !ok {
		return ErrUnknown
	}
	if fits, _ := vec.LE(rel, alloc); !fits {
		return ErrOverRelease
	}
	b.alloc[pid], _ = vec.Sub(alloc, rel)
	b.avail, _ = vec.Add(b.avail, rel)
	return nil
}
func (b *Bank) Finish(pid int) {
	defer b.lock()()
	if alloc, ok := b.alloc[pid]; ok {
		b.avail, _ = vec.Add(b.avail, alloc)
		delete(b.alloc, pid)
		delete(b.max, pid)
	}
}
func (b *Bank) Snapshot() (vec.V, map[int]vec.V, map[int]vec.V) {
	defer b.lock()()
	return append(vec.V(nil), b.avail...), maps.Clone(b.max), maps.Clone(b.alloc)
}
func (b *Bank) Comparisons() int { defer b.lock()(); return b.cmp }
func safe(avail vec.V, max, alloc map[int]vec.V, cmp *int) bool {
	*cmp = 0
	work, left := append(vec.V(nil), avail...), maps.Clone(max)
	for len(left) > 0 {
		progress := false
		for p, m := range left {
			need, _ := vec.Sub(m, alloc[p])
			*cmp++
			if fits, _ := vec.LE(need, work); fits {
				work, _ = vec.Add(work, alloc[p])
				delete(left, p)
				progress = true
			}
		}
		if !progress {
			return false
		}
	}
	return true
}
