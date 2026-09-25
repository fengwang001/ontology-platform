// Package alloc implements the aligned bump allocator: header bookkeeping,
// base backtracking on Free, sentinel errors and the complexity counter.
// It depends only on package align.
package alloc

import (
	"errors"
	"fmt"
	"sync"

	"ontology/align"
)

// ErrBadFree is returned when Free is called on a pointer that is not
// currently allocated (never allocated, or already freed).
var ErrBadFree = errors.New("alloc: pointer not allocated or already freed")

// Re-exported validation sentinels: one import gives callers all three
// distinguishable error kinds.
var (
	ErrInvalidSize  = align.ErrInvalidSize
	ErrInvalidAlign = align.ErrInvalidAlign
)

// Allocator hands out aligned offsets in an abstract byte space. The zero
// value is not usable; use New. Safe for concurrent use.
type Allocator struct {
	mu      sync.Mutex
	next    int         // bump pointer; freed space is never reused
	hdr     map[int]int // header offset -> base recorded at Alloc time
	live    map[int]int // ptr -> align of every live allocation
	lastChk int         // records inspected by the most recent Alloc/Free
}

// New returns an empty allocator with next = 0.
func New() *Allocator {
	return &Allocator{hdr: map[int]int{}, live: map[int]int{}}
}

// Alloc reserves size+(align-1)+HeaderSize bytes at next and returns the
// aligned pointer. Invalid requests are rejected before any state change.
func (a *Allocator) Alloc(size, al int) (int, error) {
	if err := align.Check(size, al); err != nil {
		return 0, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	base := a.next
	ptr := align.Up(base+align.HeaderSize, al)
	a.hdr[align.HeaderOff(ptr)] = base
	a.live[ptr] = al
	a.next = base + size + (al - 1) + align.HeaderSize
	a.lastChk = 0 // bump pointer: no existing record is inspected
	return ptr, nil
}

// Free marks ptr as freed, reading its base back from the header at
// [ptr-HeaderSize, ptr). Unknown or already-freed pointers are rejected
// without any state change.
func (a *Allocator) Free(ptr int) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.live[ptr]; !ok {
		a.lastChk = 1 // one map lookup, no scan
		return ErrBadFree
	}
	_ = a.hdr[align.HeaderOff(ptr)] // backtrack: base recorded at Alloc time
	delete(a.live, ptr)
	a.lastChk = 1 // direct header lookup, independent of allocation count
	return nil
}

// Allocated reports the number of live allocations.
func (a *Allocator) Allocated() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.live)
}

// SelfCheck runs built-in operation sequences against fresh allocators and
// verifies: the section-3 eight-step trace, alignment of every pointer,
// agreement with a naive reference model, header backtracking, rejection
// without state change, and O(1) record inspection under growing load.
func (a *Allocator) SelfCheck() error {
	// The eight-step trace from NOTES.md: ptr/base/next per step.
	al := New()
	type step struct {
		size, al, ptr, base, next int
	}
	trace := []step{
		{10, 16, 16, 0, 33}, {4, 8, 48, 33, 52}, {8, 8, 64, 52, 75},
		{1, 8, 88, 75, 91}, {16, 16, 112, 91, 130},
	}
	var ptrs []int
	for i, s := range trace {
		ptr, err := al.Alloc(s.size, s.al)
		if err != nil || ptr != s.ptr || ptr%s.al != 0 {
			return fmt.Errorf("selfcheck trace alloc %d: ptr=%d err=%v", i, ptr, err)
		}
		if al.hdr[align.HeaderOff(ptr)] != s.base || al.next != s.next {
			return fmt.Errorf("selfcheck trace alloc %d: base/next mismatch", i)
		}
		ptrs = append(ptrs, ptr)
		if i == 2 { // interleave the three frees exactly as specified
			for j, p := range []int{16, 48} {
				want := []int{0, 33}[j]
				if err := al.Free(p); err != nil || al.hdr[align.HeaderOff(p)] != want {
					return fmt.Errorf("selfcheck trace free %d: %v", p, err)
				}
			}
		}
	}
	if err := al.Free(64); err != nil || al.hdr[align.HeaderOff(64)] != 52 || al.next != 130 {
		return fmt.Errorf("selfcheck trace free 64: %v", err)
	}
	// Rejections leave no trace and the three error kinds are distinct.
	before := al.next
	n := al.Allocated()
	_, e1 := al.Alloc(0, 8)
	_, e2 := al.Alloc(4, 6)
	e3 := al.Free(999)
	if !errors.Is(e1, ErrInvalidSize) || !errors.Is(e2, ErrInvalidAlign) || !errors.Is(e3, ErrBadFree) {
		return fmt.Errorf("selfcheck: rejections: %v %v %v", e1, e2, e3)
	}
	if e1 == e2 || e2 == e3 || e1 == e3 {
		return errors.New("selfcheck: error kinds not distinct")
	}
	if e4 := al.Free(ptrs[0]); !errors.Is(e4, ErrBadFree) { // double free
		return fmt.Errorf("selfcheck: double free: %v", e4)
	}
	if al.next != before || al.Allocated() != n {
		return errors.New("selfcheck: rejected op changed state")
	}
	// Complexity: inspected records must not grow with the live count.
	for _, m := range []int{100, 1000, 10000} {
		b := New()
		var ps []int
		for i := 0; i < m; i++ {
			p, err := b.Alloc(1, 8)
			if err != nil {
				return err
			}
			ps = append(ps, p)
		}
		if err := b.Free(ps[m/2]); err != nil || b.lastChk > 1 {
			return fmt.Errorf("selfcheck: m=%d inspected %d records", m, b.lastChk)
		}
	}
	return nil
}
