package alloc

import (
	"errors"
	"math/rand/v2"
	"testing"

	"ontology/align"
)

// TestTrace8 pins the eight-step trace from NOTES.md: ptr, backtracked base, next.
func TestTrace8(t *testing.T) {
	a := New()
	steps := []struct{ size, al, ptr, base, next int }{ // size<0: Free step, base=backtracked
		{10, 16, 16, 0, 33}, {4, 8, 48, 33, 52}, {8, 8, 64, 52, 75},
		{-1, 0, 16, 0, 75}, {-1, 0, 48, 33, 75}, {1, 8, 88, 75, 91},
		{16, 16, 112, 91, 130}, {-1, 0, 64, 52, 130},
	}
	for i, s := range steps {
		if s.size < 0 {
			if err := a.Free(s.ptr); err != nil {
				t.Fatalf("step %d: free %d: %v", i, s.ptr, err)
			}
		} else {
			ptr, err := a.Alloc(s.size, s.al)
			if err != nil || ptr != s.ptr || ptr%s.al != 0 {
				t.Fatalf("step %d: ptr=%d err=%v, want %d", i, ptr, err, s.ptr)
			}
		}
		if got := a.hdr[align.HeaderOff(s.ptr)]; got != s.base {
			t.Fatalf("step %d: header base=%d, want %d", i, got, s.base)
		}
		if a.next != s.next {
			t.Fatalf("step %d: next=%d, want %d", i, a.next, s.next)
		}
	}
	if got := a.Allocated(); got != 2 {
		t.Fatalf("final live=%d, want 2", got)
	}
}

// TestNaiveModel compares random Alloc/Free sequences against a naive simulation after every op.
func TestNaiveModel(t *testing.T) {
	for _, seed := range []uint64{1, 7, 42, 2026} {
		r := rand.New(rand.NewPCG(seed, 0))
		a, next := New(), 0
		live := map[int][2]int{} // ptr -> {base, align}
		for step := 0; step < 2000; step++ {
			if len(live) == 0 || r.IntN(3) > 0 { // alloc with prob ~2/3
				size, al := 1+r.IntN(64), 1<<r.IntN(6)
				ptr, err := a.Alloc(size, al)
				want := (next + align.HeaderSize + al - 1) &^ (al - 1)
				if err != nil || ptr != want || ptr%al != 0 {
					t.Fatalf("seed %d step %d: ptr=%d want=%d err=%v", seed, step, ptr, want, err)
				}
				live[ptr] = [2]int{next, al}
				next += size + (al - 1) + align.HeaderSize
			} else { // free a random live pointer
				var victim int
				for p := range live {
					victim = p
				}
				if err := a.Free(victim); err != nil {
					t.Fatalf("seed %d step %d: free %d: %v", seed, step, victim, err)
				}
				delete(live, victim)
			}
			if a.next != next || len(a.live) != len(live) {
				t.Fatalf("seed %d step %d: next/live drift", seed, step)
			}
			for p, ba := range live { // invariants 2+3: set and headers match
				if _, ok := a.live[p]; !ok || a.hdr[align.HeaderOff(p)] != ba[0] {
					t.Fatalf("seed %d step %d: ptr %d diverged", seed, step, p)
				}
			}
		}
	}
}

// TestErrorsDistinct pins invariant 4: distinguishable sentinels, no state change.
func TestErrorsDistinct(t *testing.T) {
	a := New()
	p, err := a.Alloc(10, 16)
	if err != nil {
		t.Fatal(err)
	}
	next, hdr := a.next, a.hdr[align.HeaderOff(p)]
	cases := []struct {
		name string
		op   func() error
		want error
	}{
		{"size<=0", func() error { _, e := a.Alloc(0, 8); return e }, ErrInvalidSize},
		{"align not pow2", func() error { _, e := a.Alloc(4, 6); return e }, ErrInvalidAlign},
		{"free unknown", func() error { return a.Free(999) }, ErrBadFree},
		{"double free", func() error { return a.Free(p) }, ErrBadFree},
	}
	if err := a.Free(p); err != nil { // arm the double-free case
		t.Fatal(err)
	}
	var errs []error
	for _, c := range cases {
		err := c.op()
		if !errors.Is(err, c.want) {
			t.Fatalf("%s: got %v, want %v", c.name, err, c.want)
		}
		errs = append(errs, err)
		if a.next != next || len(a.live) != 0 || a.hdr[align.HeaderOff(p)] != hdr {
			t.Fatalf("%s: state changed after rejection", c.name)
		}
	}
	sentinels := []error{ErrInvalidSize, ErrInvalidAlign, ErrBadFree}
	for i, c := range cases[:3] { // the three kinds are pairwise distinguishable
		for _, s := range sentinels {
			if s != c.want && errors.Is(errs[i], s) {
				t.Fatalf("%s: %v indistinguishable from %v", c.name, errs[i], s)
			}
		}
	}
	if _, err := a.Alloc(1, 8); err != nil { // still usable afterwards
		t.Fatalf("allocator unusable after rejections: %v", err)
	}
}

// TestComplexityCounter proves Free inspects O(1) records regardless of m.
func TestComplexityCounter(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		a := New()
		var ptrs []int
		for i := 0; i < m; i++ {
			p, err := a.Alloc(1, 8)
			if err != nil {
				t.Fatal(err)
			}
			ptrs = append(ptrs, p)
			if a.lastChk != 0 {
				t.Fatalf("m=%d: Alloc inspected %d records", m, a.lastChk)
			}
		}
		if err := a.Free(ptrs[m/2]); err != nil {
			t.Fatal(err)
		}
		if a.lastChk > 1 {
			t.Fatalf("m=%d: Free inspected %d records, grows with m", m, a.lastChk)
		}
	}
	if err := New().SelfCheck(); err != nil { // built-in check of all 4 invariants
		t.Fatal(err)
	}
}
