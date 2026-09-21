package ontology

import (
	"math/rand/v2"
	"testing"
)

// TestOpsMatchNaive differential-tests Union/Intersect/Difference on
// compressed runs against a per-bit oracle, on random inputs.
func TestOpsMatchNaive(t *testing.T) {
	const limit = 4096
	rng := rand.New(rand.NewPCG(1, 2))
	for trial := range 200 {
		s1, n1 := randomSet(rng, limit, 12)
		s2, n2 := randomSet(rng, limit, 12)
		cases := []struct {
			name string
			got  *Set
			want naive
		}{
			{"union", mustOp(s1.Union(s2)), n1.union(n2)},
			{"intersect", mustOp(s1.Intersect(s2)), n1.intersect(n2)},
			{"difference", mustOp(s1.Difference(s2)), n1.difference(n2)},
			{"difference-rev", mustOp(s2.Difference(s1)), n2.difference(n1)},
		}
		for _, c := range cases {
			if !sameBits(c.got, c.want, limit) {
				t.Fatalf("trial %d %s: compressed result disagrees with naive", trial, c.name)
			}
			if err := c.got.Verify(); err != nil {
				t.Fatalf("trial %d %s: result not canonical: %v", trial, c.name, err)
			}
		}
	}
}

func mustOp(s *Set, _ int) *Set { return s }

// TestSetRangeMatchNaive differential-tests bulk range inserts,
// including the domain edges 0 and MaxUint32.
func TestSetRangeMatchNaive(t *testing.T) {
	const limit = 4096
	rng := rand.New(rand.NewPCG(3, 5))
	s, n := New(), naive{}
	for range 300 {
		lo := rng.Uint32N(limit)
		hi := lo + rng.Uint32N(limit/4+1)
		if hi >= limit {
			hi = limit - 1
		}
		s.SetRange(lo, hi)
		n.setRange(lo, hi)
	}
	if !sameBits(s, n, limit) {
		t.Fatal("SetRange disagrees with naive")
	}
	if err := s.Verify(); err != nil {
		t.Fatal(err)
	}

	full := New()
	full.SetRange(0, ^uint32(0))
	if full.Count() != 1<<32 || full.RunCount() != 1 {
		t.Fatal("SetRange full domain failed")
	}
	if err := full.Verify(); err != nil {
		t.Fatal(err)
	}
}

// TestMutationsMatchNaive differential-tests Set/Clear sequences.
func TestMutationsMatchNaive(t *testing.T) {
	const limit = 2048
	rng := rand.New(rand.NewPCG(7, 9))
	s, n := New(), naive{}
	for range 3000 {
		bit := rng.Uint32N(limit)
		if rng.IntN(2) == 0 {
			s.Set(bit)
			n.set(bit)
		} else {
			s.Clear(bit)
			n.clear(bit)
		}
	}
	if !sameBits(s, n, limit) {
		t.Fatal("Set/Clear sequence disagrees with naive")
	}
	if err := s.Verify(); err != nil {
		t.Fatal(err)
	}
}

// TestHugeRunsFewSteps: two sets of two runs each, together covering
// tens of millions of bits, combine in a single-digit number of
// run-advance steps — not ten million.
func TestHugeRunsFewSteps(t *testing.T) {
	a := &Set{runs: []interval{{0, 4_999_999}, {6_000_000, 10_999_999}}}
	b := &Set{runs: []interval{{2_500_000, 7_499_999}, {8_500_000, 13_499_999}}}
	if got := a.Count() + b.Count(); got != 20_000_000 {
		t.Fatalf("input covers %d bits, want 20M", got)
	}
	inter, steps := a.Intersect(b)
	if steps >= 10 {
		t.Fatalf("intersect took %d run steps, want single digit", steps)
	}
	want := []interval{{2_500_000, 4_999_999}, {6_000_000, 7_499_999}, {8_500_000, 10_999_999}}
	if inter.Count() != 6_500_000 {
		t.Fatalf("intersection count = %d, want 6_500_000", inter.Count())
	}
	if err := inter.Verify(); err != nil {
		t.Fatal(err)
	}
	runs := inter.snapshot()
	if len(runs) != len(want) {
		t.Fatalf("intersection has %d runs, want %d", len(runs), len(want))
	}
	for i, r := range runs {
		if r != want[i] {
			t.Fatalf("run %d = %v, want %v", i, r, want[i])
		}
	}
	if _, uSteps := a.Union(b); uSteps >= 10 {
		t.Fatalf("union took %d run steps, want single digit", uSteps)
	}
	if _, dSteps := a.Difference(b); dSteps >= 10 {
		t.Fatalf("difference took %d run steps, want single digit", dSteps)
	}
}

// TestOpsEdgeCases: empty/full-domain operands stay canonical.
func TestOpsEdgeCases(t *testing.T) {
	empty := New()
	full := &Set{runs: []interval{{0, ^uint32(0)}}}
	one := New()
	one.Set(12345)
	if got, _ := empty.Union(one); got.Count() != 1 {
		t.Fatal("empty ∪ {12345} != {12345}")
	}
	if got, _ := one.Intersect(empty); got.Count() != 0 {
		t.Fatal("{12345} ∩ ∅ != ∅")
	}
	got, _ := full.Difference(one)
	if got.Count() != 1<<32-1 {
		t.Fatal("full \\ {12345} has wrong count")
	}
	if err := got.Verify(); err != nil {
		t.Fatal(err)
	}
	if got.RunCount() != 2 {
		t.Fatalf("full \\ {12345} has %d runs, want 2", got.RunCount())
	}
	if u, _ := full.Union(one); u.Count() != 1<<32 {
		t.Fatal("full ∪ anything != full")
	}
}
