package rmq

import (
	"errors"
	"math"
	"math/rand"
	"testing"
)

// TestQueryVisitCountIsLogarithmic is table-driven over several tiers of
// n in [100,10000]; the full interval and loop-generated small random
// intervals must all visit at most 4*ceil(log2(n))+4 nodes. The counter
// is read directly as an unexported field, never via an exported method.
func TestQueryVisitCountIsLogarithmic(t *testing.T) {
	tiers := []int{100, 317, 1000, 3163, 10000}
	for _, n := range tiers {
		arr := make([]int64, n)
		rng := rand.New(rand.NewSource(int64(n)))
		for i := range arr {
			arr[i] = rng.Int63()
		}
		s, err := Build(arr)
		if err != nil {
			t.Fatalf("n=%d Build: %v", n, err)
		}
		bound := int64(4*ceilLog2(n) + 4)
		queries := [][2]int{{0, n}, {0, 1}, {n / 2, n/2 + 1}}
		for k := 0; k < 32; k++ {
			l := rng.Intn(n)
			span := 1 + rng.Intn(9)
			if span > n-l {
				span = n - l
			}
			queries = append(queries, [2]int{l, l + span})
		}
		for _, qd := range queries {
			if _, err := s.Query(qd[0], qd[1]); err != nil {
				t.Fatalf("n=%d Query(%d,%d): %v", n, qd[0], qd[1], err)
			}
			if got := s.visited.Load(); got > bound {
				t.Errorf("n=%d Query(%d,%d) visited %d nodes, bound %d", n, qd[0], qd[1], got, bound)
			}
		}
	}
}

// TestSentinelsDistinct pins pairwise distinctness of the four failures.
func TestSentinelsDistinct(t *testing.T) {
	errs := []error{ErrEmptyInput, ErrInvalidRange, ErrRangeOutOfBounds, ErrIndexOutOfBounds}
	for i := 0; i < len(errs); i++ {
		for j := i + 1; j < len(errs); j++ {
			if errs[i] == errs[j] {
				t.Errorf("sentinel %d and %d are identical", i, j)
			}
		}
	}
}

// TestErrorCases is the table-driven error classification at rmq level.
func TestErrorCases(t *testing.T) {
	s, err := Build([]int64{1, 2, 3})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		want error
		fn   func() error
	}{
		{"empty build", ErrEmptyInput, func() error { _, e := Build(nil); return e }},
		{"l>r", ErrInvalidRange, func() error { _, e := s.Query(2, 1); return e }},
		{"l<0", ErrRangeOutOfBounds, func() error { _, e := s.Query(-1, 0); return e }},
		{"r>n", ErrRangeOutOfBounds, func() error { _, e := s.Query(0, 4); return e }},
		{"i==n", ErrIndexOutOfBounds, func() error { return s.Update(3, 0) }},
		{"i<0", ErrIndexOutOfBounds, func() error { return s.Update(-1, 0) }},
	}
	for _, c := range cases {
		if got := c.fn(); !errors.Is(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

// TestEmptyAndIdentity: Query(l,l) is +Inf and a rejected query does not
// reset the last-query visit counter semantics of subsequent calls.
func TestEmptyAndIdentity(t *testing.T) {
	s, _ := Build([]int64{9, 7, 8})
	cases := [][2]int{{0, 0}, {1, 1}, {3, 3}}
	for _, qd := range cases {
		got, err := s.Query(qd[0], qd[1])
		if err != nil || got != math.MaxInt64 {
			t.Errorf("Query(%d,%d) = (%d,%v), want MaxInt64", qd[0], qd[1], got, err)
		}
	}
	if v, err := s.Query(0, 3); err != nil || v != 7 {
		t.Errorf("after empty queries Query(0,3) = (%d,%v), want 7", v, err)
	}
}
