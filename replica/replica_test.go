package replica

import (
	"errors"
	"reflect"
	"sync"
	"testing"

	"ontology/delta"
)

// TestVersionMonotonic pins invariant 2: Version never decreases and equals d.To.
func TestVersionMonotonic(t *testing.T) {
	r := New()
	for i := 0; i < 50; i++ {
		d := delta.Delta{From: i, To: i + 1, Changes: []delta.Change{delta.Set("k", i)}}
		if e := r.Apply(d); e != nil || r.Version() != i+1 {
			t.Fatalf("apply %d: %v", i, e)
		}
	}
	if e := r.Apply(delta.Delta{From: 0, To: 1}); e != nil || r.Version() != 50 {
		t.Fatal("duplicate moved version")
	}
	if e := r.Apply(delta.Delta{From: 999, To: 1000}); e == nil || r.Version() != 50 {
		t.Fatal("gap moved version")
	}
}

// TestFailureNoTrace pins invariant 4: distinct sentinels, no trace, reusable.
func TestFailureNoTrace(t *testing.T) {
	r := New()
	_ = r.Apply(delta.Delta{From: 0, To: 1, Changes: []delta.Change{delta.Set("a", 1)}})
	type row struct {
		name string
		d    delta.Delta
		want error
	}
	cases := []row{
		{"gap", delta.Delta{From: 5, To: 6}, ErrGap},
		{"range eq", delta.Delta{From: 0, To: 0}, ErrInvalidRange},
		{"range rev", delta.Delta{From: 2, To: 1}, ErrInvalidRange},
		{"neg from", delta.Delta{From: -1, To: 1}, ErrNegativeVersion},
		{"neg to", delta.Delta{From: 0, To: -2}, ErrNegativeVersion},
		{"empty key", delta.Delta{From: 1, To: 2, Changes: []delta.Change{delta.Set("", 1)}}, delta.ErrEmptyKey},
	}
	seen := map[error]bool{}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			bv, bs := r.Version(), r.State()
			if e := r.Apply(c.d); !errors.Is(e, c.want) {
				t.Fatalf("err=%v want %v", e, c.want)
			}
			seen[c.want] = true
			if r.Version() != bv || !reflect.DeepEqual(r.State(), bs) {
				t.Fatal("rejection left a trace")
			}
		})
	}
	if len(seen) != 4 {
		t.Fatalf("want 4 distinct error kinds, got %d", len(seen))
	}
	next := delta.Delta{From: 1, To: 2, Changes: []delta.Change{delta.Set("z", 9)}}
	if e := r.Apply(next); e != nil {
		t.Fatalf("unusable after rejections: %v", e)
	}
}

// TestProbeCountO1 proves dedup/gap judgment reads one version record: the
// non-exported probe count is an m-independent constant (in-package access).
func TestProbeCountO1(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		r := New()
		for i := 0; i < m; i++ {
			d := delta.Delta{From: i, To: i + 1, Changes: []delta.Change{delta.Set("k", i)}}
			if e := r.Apply(d); e != nil {
				t.Fatal(e)
			}
		}
		for _, d := range []delta.Delta{ // in-order, gap, duplicate
			{From: m, To: m + 1}, {From: m + 5, To: m + 6}, {From: 0, To: 1},
		} {
			_ = r.Apply(d)
			if r.probeCount > 1 {
				t.Fatalf("m=%d probeCount=%d exceeds O(1)", m, r.probeCount)
			}
		}
	}
}

// TestConcurrentReaders asserts goroutines see identical snapshots, no sleeps.
func TestConcurrentReaders(t *testing.T) {
	r := New()
	for i := 0; i < 200; i++ {
		d := delta.Delta{From: i, To: i + 1, Changes: []delta.Change{delta.Set(string(rune('A'+i%26)), i)}}
		_ = r.Apply(d)
	}
	var mu sync.Mutex
	var gv int
	var gs map[string]int
	set, bad := false, false
	var wg sync.WaitGroup
	for g := 0; g < 64; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, s := r.Version(), r.State()
			mu.Lock()
			defer mu.Unlock()
			switch {
			case !set:
				gv, gs, set = v, s, true
			case v != gv || !reflect.DeepEqual(s, gs):
				bad = true
			}
		}()
	}
	wg.Wait()
	if !set || bad {
		t.Fatal("concurrent readers disagreed")
	}
}
