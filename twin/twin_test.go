package twin

import (
	"math"
	"math/rand"
	"slices"
	"testing"

	"ontology/stk"
)

var elevenRows = []struct {
	push    bool
	v       float64
	in, out [][2]float64
	flip    bool
	ev, mx  float64
	moves   int
}{
	{true, 3, [][2]float64{{3, 3}}, nil, false, math.NaN(), 3, 0},
	{true, 1, [][2]float64{{3, 3}, {1, 3}}, nil, false, math.NaN(), 3, 0},
	{true, 3, [][2]float64{{3, 3}, {1, 3}, {3, 3}}, nil, false, math.NaN(), 3, 0},
	{true, 2, [][2]float64{{3, 3}, {1, 3}, {3, 3}, {2, 3}}, nil, false, math.NaN(), 3, 0},
	{true, 1, nil, [][2]float64{{1, 1}, {2, 2}, {3, 3}, {1, 3}}, true, 3, 3, 5},
	{true, 0, [][2]float64{{0, 0}}, [][2]float64{{1, 1}, {2, 2}, {3, 3}}, false, 1, 3, 5},
	{false, 0, [][2]float64{{0, 0}}, [][2]float64{{1, 1}, {2, 2}}, false, 3, 2, 5},
	{true, 2, [][2]float64{{0, 0}, {2, 2}}, [][2]float64{{1, 1}, {2, 2}}, false, math.NaN(), 2, 5},
	{false, 0, [][2]float64{{0, 0}, {2, 2}}, [][2]float64{{1, 1}}, false, 2, 2, 5},
	{false, 0, [][2]float64{{0, 0}, {2, 2}}, nil, false, 1, 2, 5},
	{false, 0, nil, [][2]float64{{2, 2}}, true, 0, 2, 7},
}

func stackEq(s *stk.Stack, want [][2]float64) bool {
	return slices.EqualFunc(s.Entries(), want, func(e stk.Entry, a [2]float64) bool {
		return e.Value == a[0] && e.Agg == a[1]
	})
}
func TestElevenSteps(t *testing.T) {
	q := New()
	for i, r := range elevenRows {
		before, ev, had := q.moves, math.NaN(), false
		if r.push {
			q.Push(r.v)
			if q.Len() > 4 {
				ev, had = q.Evict()
			}
		} else {
			ev, had = q.Evict()
		}
		if (q.moves > before) != r.flip || !stackEq(q.in, r.in) || !stackEq(q.out, r.out) {
			t.Fatalf("step %d: stacks/flip mismatch", i+1)
		}
		if had != !math.IsNaN(r.ev) || (had && ev != r.ev) {
			t.Fatalf("step %d: evicted=%v want=%v", i+1, ev, r.ev)
		}
		if mx, ok := q.Max(); !ok || mx != r.mx || q.moves != r.moves {
			t.Fatalf("step %d: Max/moves mismatch", i+1)
		}
	}
}
func TestStackAggregates(t *testing.T) { // invariant 3 in stk and across flips
	for _, vs := range [][]float64{{3, 1, 3, 2}, {5, 5, 5}, {1, 2, 3}, {3, 2, 1}, {2, 1, 2}} {
		s, cur := stk.New(), 0.0
		for i, v := range vs {
			s.Push(v)
			if i == 0 || v > cur {
				cur = v
			}
			if a, ok := s.TopAgg(); !ok || a != cur {
				t.Fatalf("stk agg mismatch at %d", i)
			}
		}
		q := New()
		for _, v := range vs {
			q.Push(v)
			if q.Len() > 2 {
				_, _ = q.Evict()
			}
			if !q.AggregatesValid() {
				t.Fatalf("aggregates invalid for %v", vs)
			}
		}
		for q.Len() > 0 {
			if _, ok := q.Evict(); !ok || !q.AggregatesValid() {
				t.Fatalf("aggregates invalid draining %v", vs)
			}
		}
	}
}
func TestFIFO(t *testing.T) { // invariant 2: evicted sequence is pushed prefix
	for seed := int64(0); seed < 8; seed++ {
		r, w, q := rand.New(rand.NewSource(seed)), int(1+seed), New()
		var pushed, evicted []float64
		for n := 0; n < 2000; n++ {
			if r.Intn(2) == 0 {
				v := float64(r.Intn(4))
				q.Push(v)
				pushed = append(pushed, v)
			} else if q.Len() > 0 {
				e, _ := q.Evict()
				evicted = append(evicted, e)
			}
			if q.Len() > w {
				e, _ := q.Evict()
				evicted = append(evicted, e)
			}
		}
		for i := range evicted {
			if evicted[i] != pushed[i] {
				t.Fatalf("FIFO violated at %d", i)
			}
		}
	}
}
func TestAmortizedMoves(t *testing.T) { // moves <= pushes; Max never moves
	for _, m := range []int{100, 500, 2000, 10000} {
		for _, denom := range []int{2, 4, 8} {
			w, q := m/denom, New()
			if w < 1 {
				w = 1
			}
			for i := 0; i < m; i++ {
				v := 9.0 // tied maximum, then a strictly decreasing run
				if i >= m/2 {
					v = float64(m - i)
				}
				q.Push(v)
				if q.Len() > w {
					_, _ = q.Evict()
				}
				b := q.moves
				if _, ok := q.Max(); !ok || q.moves != b || q.moves > i+1 {
					t.Fatalf("budget violated m=%d w=%d moves=%d", m, w, q.moves)
				}
			}
			for q.Len() > 0 {
				b := q.moves
				_, _ = q.Max()
				if q.moves != b {
					t.Fatal("Max must not move elements")
				}
				_, _ = q.Evict()
			}
			if q.moves > m {
				t.Fatalf("moves %d > pushes %d", q.moves, m)
			}
		}
	}
}
