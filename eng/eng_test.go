package eng

import (
	"math/rand"
	"reflect"
	"sync"
	"testing"

	"ontology/wrap"
)

// TestComparisonsConstant is the O(1) evidence: after m increasing feeds, the
// next Feed compares exactly one retained offset regardless of m. The field
// is unexported and reachable only from this same-package test.
func TestComparisonsConstant(t *testing.T) {
	for _, m := range []int{100, 1000, 5000, 10000} {
		e := New(1 << 30)
		for i := 1; i <= m; i++ {
			if _, err := e.Feed(uint32(i)); err != nil {
				t.Fatalf("m=%d feed %d: %v", m, i, err)
			}
			if want := boolToInt(i > 1); e.comparisons != want {
				t.Fatalf("m=%d after feed %d comparisons=%d want %d", m, i, e.comparisons, want)
			}
		}
		if _, err := e.Feed(uint32(m + 1)); err != nil {
			t.Fatal(err)
		}
		if e.comparisons != 1 {
			t.Fatalf("m=%d final comparisons=%d, want 1", m, e.comparisons)
		}
	}
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// naiveRef is an independent offline replay; rejected steps leave pu.
func naiveRef(seq []uint32, th uint32) []int64 {
	var prev uint32
	var pu int64
	has := false
	out := make([]int64, len(seq))
	for i, r := range seq {
		if has {
			d := uint64(r) - uint64(prev)
			if r < prev {
				if uint64(prev)-uint64(r) <= uint64(th) {
					out[i] = pu
					continue
				}
				d = wrap.Size - uint64(prev) + uint64(r)
			}
			pu, prev = pu+int64(d), r
		} else {
			pu, prev, has = int64(r), r, true
		}
		out[i] = pu
	}
	return out
}

// TestRandomSequences compares engine state to the replay over random orders.
func TestRandomSequences(t *testing.T) {
	for _, tc := range []struct {
		seed int64
		th   uint32
	}{{1, 1}, {2, 1 << 20}, {3, 1<<31 - 1}, {4, 777}, {5, 123456789}} {
		rng := rand.New(rand.NewSource(tc.seed))
		seq := make([]uint32, 2000)
		for i := range seq {
			seq[i] = rng.Uint32()
		}
		want := naiveRef(seq, tc.th)
		e := New(tc.th)
		var last int64
		for i, r := range seq {
			e.Feed(r)
			got, _ := e.LastUnwrapped()
			if got != want[i] {
				t.Fatalf("seed %d step %d: %d!=%d", tc.seed, i, got, want[i])
			}
			if got < last {
				t.Fatalf("seed %d monotonicity at %d", tc.seed, i)
			}
			last = got
		}
	}
}

// TestConcurrentReadersEng: N goroutines read a fed engine concurrently; all
// snapshots are field-for-field identical. No sleeps.
func TestConcurrentReadersEng(t *testing.T) {
	e := New(1 << 30)
	for i := uint32(1); i <= 500; i++ {
		if _, err := e.Feed(i * 997); err != nil {
			t.Fatal(err)
		}
	}
	const n = 16
	type snap struct {
		u   int64
		raw uint32
		c   map[Event]int
	}
	got := make([]snap, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			for j := 0; j < 32; j++ {
				u, _ := e.LastUnwrapped()
				raw, _ := e.LastRaw()
				got[i] = snap{u, raw, e.Counts()}
			}
		}(i)
	}
	close(start)
	wg.Wait()
	for i := 1; i < n; i++ {
		if got[i].u != got[0].u || got[i].raw != got[0].raw || !reflect.DeepEqual(got[i].c, got[0].c) {
			t.Fatalf("reader %d %+v != %+v", i, got[i], got[0])
		}
	}
}
