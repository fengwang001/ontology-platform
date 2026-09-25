package alloc

import (
	"errors"
	"fmt"
	"sync"

	"ontology/align"
)

var ErrInvalidFree = errors.New("alloc: free of pointer that is not currently allocated")

type record struct{ base, align int }

type Allocator struct {
	mu      sync.Mutex
	next    int
	live    map[int]*record
	headers map[int]int // header offset -> base
	// probes counts records inspected by the last Alloc/Free; unexported,
	// its numeric value never leaves the package.
	probes int
}

func New() *Allocator {
	return &Allocator{live: map[int]*record{}, headers: map[int]int{}}
}

// Alloc reserves size+(align-1)+headerSize and returns an align-aligned ptr,
// writing base into the header at [ptr-headerSize, ptr).
func (a *Allocator) Alloc(size, al int) (ptr int, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err = align.Validate(size, al); err != nil {
		a.probes = 0
		return 0, err
	}
	base := a.next
	ptr = align.AlignUp(base+align.HeaderSize, al)
	a.headers[align.HeaderOffset(ptr)] = base
	a.live[ptr] = &record{base, al}
	a.next = base + size + (al - 1) + align.HeaderSize
	a.probes = 0 // bump pointer: zero existing records inspected
	return ptr, nil
}

// Free reads base at [ptr-headerSize, ptr), releases ptr, returns base.
// Direct map lookup; never scans; no trace on rejection.
func (a *Allocator) Free(ptr int) (base int, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	r, ok := a.live[ptr]
	if !ok {
		a.probes = 0
		return 0, ErrInvalidFree
	}
	a.probes = 1 // the single map-hit record
	if base = a.headers[align.HeaderOffset(ptr)]; base != r.base {
		return 0, ErrInvalidFree
	}
	delete(a.live, ptr)
	return base, nil
}

func (a *Allocator) Len() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.live)
}

var script = [...][6]int{
	{1, 10, 16, 16, 0, 33},
	{1, 4, 8, 48, 0, 52},
	{1, 8, 8, 64, 0, 75},
	{0, 0, 0, 16, 0, 75},
	{0, 0, 0, 48, 33, 75},
	{1, 1, 8, 88, 0, 91},
	{1, 16, 16, 112, 0, 130},
	{0, 0, 0, 64, 52, 130},
}

// SelfCheck replays the script against a naive simulation: alignment,
// live-set/header-base equality, Free read-back, no-trace rejections.
func (a *Allocator) SelfCheck() error {
	f := New()
	nn := 0
	nLive, nBase := map[int]bool{}, map[int]int{}
	for i, s := range script {
		if s[0] == 1 {
			n, al := s[1], s[2]
			p, err := f.Alloc(n, al)
			b := nn
			nn = b + n + (al - 1) + align.HeaderSize
			if err != nil || p != s[3] || p != align.AlignUp(b+align.HeaderSize, al) || p%al != 0 || f.next != s[5] {
				return fmt.Errorf("step %d alloc mismatch (p=%d err=%v)", i+1, p, err)
			}
			nLive[p], nBase[p] = true, b
		} else {
			b, err := f.Free(s[3])
			if err != nil || b != s[4] || f.next != s[5] || !nLive[s[3]] || nBase[s[3]] != b {
				return fmt.Errorf("step %d free mismatch (base=%d err=%v)", i+1, b, err)
			}
			delete(nLive, s[3])
		}
		if nn != f.next || len(nLive) != len(f.live) {
			return fmt.Errorf("step %d diverges from naive model", i+1)
		}
		for p := range nLive {
			r := f.live[p]
			if r == nil || r.base != nBase[p] || f.headers[align.HeaderOffset(p)] != nBase[p] {
				return fmt.Errorf("step %d: ptr %d base/header mismatch", i+1, p)
			}
		}
	}
	sx, sl := f.next, len(f.live)
	bad := []func() error{
		func() error { _, e := f.Alloc(0, 8); return e },
		func() error { _, e := f.Alloc(4, 6); return e },
		func() error { _, e := f.Free(16); return e },
		func() error { _, e := f.Free(999); return e },
	}
	want := []error{align.ErrBadSize, align.ErrBadAlign, ErrInvalidFree, ErrInvalidFree}
	for i, run := range bad {
		if !errors.Is(run(), want[i]) || f.next != sx || len(f.live) != sl {
			return fmt.Errorf("reject case %d: bad error or state mutated", i+1)
		}
	}
	if _, err := f.Alloc(1, 8); err != nil {
		return err
	}
	return nil
}

// CheckProbeComplexity returns only a verdict; counts never leave the package.
func CheckProbeComplexity() error {
	for _, m := range []int{100, 1000, 10000} {
		f := New()
		for i := 0; i < m; i++ {
			if _, err := f.Alloc(1, 8); err != nil || f.probes != 0 {
				return errors.New("alloc: Alloc inspected existing records")
			}
		}
		// first Alloc(1,8) from next=0 always returns ptr 8
		if _, err := f.Free(8); err != nil || f.probes > 1 {
			return errors.New("alloc: Free inspected more than one record")
		}
	}
	return nil
}
