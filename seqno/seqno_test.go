package seqno

import "testing"

func TestSet(t *testing.T) {
	cases := []struct {
		name string
		seqs []int64
		lcp  int64
		max  int64
	}{
		{"empty", nil, 0, 0},
		{"in order", []int64{1, 2, 3}, 3, 3},
		{"out of order", []int64{1, 3, 2}, 3, 3},
		{"hole", []int64{1, 3}, 1, 3},
		{"reverse 100", reverseRange(100), 100, 100},
		{"reverse 10000", reverseRange(10000), 10000, 10000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := New()
			for _, seq := range tc.seqs {
				s.Set(seq)
			}
			if s.LCP() != tc.lcp {
				t.Fatalf("lcp = %d, want %d", s.LCP(), tc.lcp)
			}
			if s.Max() != tc.max {
				t.Fatalf("max = %d, want %d", s.Max(), tc.max)
			}
			for _, seq := range tc.seqs {
				if !s.Has(seq) {
					t.Fatalf("Has(%d) = false", seq)
				}
			}
		})
	}
}

func TestSetIdempotentAndInvalid(t *testing.T) {
	s := New()
	if !s.Set(1) || s.Set(1) {
		t.Fatal("first Set must be true, repeat false")
	}
	if s.Set(0) || s.Set(-5) {
		t.Fatal("non-positive Set must be rejected")
	}
	if s.Has(0) || s.LCP() != 1 {
		t.Fatal("invalid seq must not change state")
	}
}

// TestStepsLinear is the non-exported-counter proof: total lcp steps equal
// the number of distinct first sets regardless of ack order, and reverse
// acks of 100 and 10000 ops stay linear (a rescan-from-1 implementation
// would be quadratic).
func TestStepsLinear(t *testing.T) {
	for _, n := range []int64{100, 10000} {
		s := New()
		var firstAcks int64
		for seq := n; seq >= 1; seq-- {
			if s.Set(seq) {
				firstAcks++
			}
		}
		// Repeated acks add no steps.
		for seq := n; seq >= 1; seq-- {
			s.Set(seq)
		}
		if s.Steps() != n || firstAcks != n {
			t.Fatalf("n=%d: steps=%d firstAcks=%d", n, s.Steps(), firstAcks)
		}
	}
}

func TestTruncateAbove(t *testing.T) {
	s := New()
	for _, seq := range []int64{1, 2, 3, 4} {
		s.Set(seq)
	}
	s.TruncateAbove(2)
	if s.LCP() != 2 || s.Max() != 2 {
		t.Fatalf("after truncate lcp=%d max=%d", s.LCP(), s.Max())
	}
	if s.Has(3) {
		t.Fatal("seq 3 must be forgotten")
	}
	// Re-adding 3 after rollback advances lcp again.
	s.Set(3)
	if s.LCP() != 3 {
		t.Fatalf("lcp after readd = %d", s.LCP())
	}
}

func reverseRange(n int64) []int64 {
	out := make([]int64, 0, n)
	for i := n; i >= 1; i-- {
		out = append(out, i)
	}
	return out
}
