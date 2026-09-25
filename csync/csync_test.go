package csync

import (
	"fmt"
	"math/bits"
	"math/rand"
	"testing"
)

// Invariant 3: CountAt equals naive per-clock containment counting.
func TestCountAtMatchesNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for _, m := range []int{2, 7, 50, 200} {
		s := NewSet()
		type ck struct{ off, err int64 }
		clocks := make([]ck, 0, m)
		for i := 0; i < m; i++ {
			c := ck{rng.Int63n(1000) - 500, rng.Int63n(50)}
			clocks = append(clocks, c)
			if err := s.Add(fmt.Sprint(i), c.off, c.err); err != nil {
				t.Fatalf("m=%d add: %v", m, err)
			}
		}
		for _, tt := range []int64{-501, -100, 0, 1, 250, 499, 500, 549} {
			want := 0
			for _, c := range clocks {
				if c.off-c.err <= tt && tt <= c.off+c.err {
					want++
				}
			}
			if got := s.CountAt(tt); got != want {
				t.Errorf("m=%d CountAt(%d)=%d want %d", m, tt, got, want)
			}
		}
	}
}

// Complexity: CountAt probes stay O(log m), not O(m), as m grows.
func TestCountAtSublinear(t *testing.T) {
	prev := 0
	for _, m := range []int{100, 1000, 10000} {
		s := NewSet()
		for i := 0; i < m; i++ {
			if err := s.Add(fmt.Sprint(i), int64(i*4), 1); err != nil {
				t.Fatal(err)
			}
		}
		limit := 2 * (bits.Len(uint(m)) + 1) // two binary searches
		for q := 0; q < 5; q++ {
			s.CountAt(int64(q*8000 - 5))
			s.cmu.Lock()
			got := s.checked
			s.cmu.Unlock()
			if got <= 0 || got > limit {
				t.Errorf("m=%d q=%d probed %d endpoints, want 0 < p <= %d", m, q, got, limit)
			}
			if m > 100 && got > 2*prev { // 10x data, at most 2x probes
				t.Errorf("m=%d probed %d > 2*%d: grows linearly with m", m, got, prev)
			}
			prev = got
		}
	}
}
