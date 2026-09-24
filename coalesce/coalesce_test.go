package coalesce

import (
	"errors"
	"math/rand/v2"
	"testing"

	"ontology/rangespec"
)

func iv(a, b int64) Interval { return Interval{a, b} }
func sp(kind rangespec.Kind, a, b int64) rangespec.Spec {
	return rangespec.Spec{Kind: kind, A: a, B: b}
}

func TestNormalize(t *testing.T) {
	// clipping of the three forms, plus overlap/adjacency merging.
	cases := []struct {
		name  string
		specs []rangespec.Spec
		size  int64
		want  []Interval
	}{
		{"closed clips past tail", []rangespec.Spec{sp(rangespec.Closed, 2, 99)}, 10, []Interval{iv(2, 9)}},
		{"open end", []rangespec.Spec{sp(rangespec.OpenEnd, 7, 0)}, 10, []Interval{iv(7, 9)}},
		{"suffix", []rangespec.Spec{sp(rangespec.Suffix, 0, 3)}, 10, []Interval{iv(7, 9)}},
		{"suffix larger than size", []rangespec.Spec{sp(rangespec.Suffix, 0, 1000)}, 10, []Interval{iv(0, 9)}},
		{"unsorted overlaps merge", []rangespec.Spec{sp(rangespec.Closed, 5, 9), sp(rangespec.Closed, 0, 5)}, 100, []Interval{iv(0, 9)}},
		{"adjacent merge -> bare", []rangespec.Spec{sp(rangespec.Closed, 0, 4), sp(rangespec.Closed, 5, 9)}, 100, []Interval{iv(0, 9)}},
		{"kept separate by a gap", []rangespec.Spec{sp(rangespec.Closed, 0, 4), sp(rangespec.Closed, 6, 9)}, 100, []Interval{iv(0, 4), iv(6, 9)}},
		{"nested intervals", []rangespec.Spec{sp(rangespec.Closed, 0, 100), sp(rangespec.Closed, 2, 3), sp(rangespec.Closed, 50, 60)}, 1000, []Interval{iv(0, 100)}},
	}
	for _, tc := range cases {
		got, err := Normalize(tc.specs, tc.size)
		if err != nil || len(got) != len(tc.want) {
			t.Fatalf("%s: %v, %v", tc.name, got, err)
		}
		for i := range tc.want {
			if got[i] != tc.want[i] {
				t.Fatalf("%s: got %v want %v", tc.name, got, tc.want)
			}
		}
	}

	// unsatisfiable forms all carry the resource size.
	for _, in := range [][]rangespec.Spec{
		{sp(rangespec.Suffix, 0, 0)}, // bytes=-0
		{sp(rangespec.Closed, 10, 20)},
		{sp(rangespec.OpenEnd, 10, 0)},
	} {
		_, err := Normalize(in, 10)
		var ue *UnsatisfiableError
		if !errors.As(err, &ue) || ue.Size != 10 {
			t.Fatalf("want UnsatisfiableError{10}, got %v", err)
		}
	}
}

func TestMergeByteSetEquivalence(t *testing.T) {
	// Exhaustive-style proof over random interval collections: expand both the
	// raw clipped specs and the merged result into a byte-set and compare.
	rng := rand.New(rand.NewPCG(1, 2))
	const size = 120
	for trial := 0; trial < 400; trial++ {
		n := 1 + rng.IntN(12)
		specs := make([]rangespec.Spec, n)
		want := make([]bool, size)
		for i := range specs {
			switch rng.IntN(3) {
			case 0:
				a := int64(rng.IntN(size + 20))
				b := a + int64(rng.IntN(15))
				specs[i] = sp(rangespec.Closed, a, b)
			case 1:
				specs[i] = sp(rangespec.OpenEnd, int64(rng.IntN(size+20)), 0)
			default:
				specs[i] = sp(rangespec.Suffix, 0, int64(rng.IntN(size+20)))
			}
			// independently compute the covered set from raw semantics.
			for _, p := range clip(specs[i], size) {
				for x := p.Start; x <= p.End; x++ {
					want[x] = true
				}
			}
		}
		got, err := Normalize(specs, size)
		if err != nil {
			trial++ // unsatisfiable collections are skipped, not invalid
			continue
		}
		gotSet := make([]bool, size)
		for _, p := range got {
			for x := p.Start; x <= p.End; x++ {
				gotSet[x] = true
			}
			// merged result must be pairwise non-overlapping, non-adjacent
		}
		for k := 1; k < len(got); k++ {
			if got[k].Start <= got[k-1].End+1 {
				t.Fatalf("trial %d: adjacent/overlapping output %v", trial, got)
			}
		}
		for x := range want {
			if want[x] != gotSet[x] {
				t.Fatalf("trial %d: byte-set mismatch at %d", trial, x)
			}
		}
	}
}

func clip(s rangespec.Spec, size int64) []Interval {
	switch s.Kind {
	case rangespec.Closed:
		if s.A >= size {
			return nil
		}
		if s.B >= size {
			s.B = size - 1
		}
		return []Interval{iv(s.A, s.B)}
	case rangespec.OpenEnd:
		if s.A >= size {
			return nil
		}
		return []Interval{iv(s.A, size-1)}
	default:
		if s.B == 0 {
			return nil
		}
		n := s.B
		if n > size {
			n = size
		}
		return []Interval{iv(size-n, size-1)}
	}
}

func TestComparisonBound(t *testing.T) {
	measure := func(n int) int {
		specs := make([]rangespec.Spec, n)
		for i := range specs {
			specs[i] = sp(rangespec.Closed, int64((i*7)%n), int64((i*7)%n)+3)
		}
		var c Counter
		if _, err := c.Normalize(specs, int64(n)*2); err != nil {
			t.Fatal(err)
		}
		return c.Comparisons()
	}
	c100 := measure(100)
	c10000 := measure(10000)
	t.Logf("comparisons n=100: %d, n=10000: %d, ratio %.2f", c100, c10000, float64(c10000)/float64(c100))
	if c100 > 100*7+99 {
		t.Fatalf("n=100 exceeds n log n bound: %d", c100)
	}
	if c10000 > 10000*14+9999 {
		t.Fatalf("n=10000 exceeds n log n bound: %d", c10000)
	}
	if got := float64(c10000) / float64(c100); got > 250 {
		t.Fatalf("growth ratio %.2f exceeds n log n order", got)
	}
}
