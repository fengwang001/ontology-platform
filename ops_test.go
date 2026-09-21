package ontology

import (
	"math/rand"
	"testing"
)

// randomPair builds a random Bitmap plus its naive twin over
// [0, universe) by applying random range sets and clears.
func randomPair(rng *rand.Rand, universe uint32) (*Bitmap, *naive) {
	b := New()
	n := newNaive()
	for k := 0; k < 40; k++ {
		lo := rng.Uint32() % universe
		hi := lo + rng.Uint32()%(universe/4+1)
		if hi >= universe {
			hi = universe - 1
		}
		if rng.Intn(2) == 0 {
			b.SetRange(lo, hi)
			for v := lo; v <= hi; v++ {
				n.set(v)
			}
		} else {
			for v := lo; v <= hi; v++ {
				b.Clear(v)
				n.clear(v)
			}
		}
	}
	return b, n
}

func TestOpsMatchNaiveBitmap(t *testing.T) {
	const universe = 4096
	rng := rand.New(rand.NewSource(20260921))
	for trial := 0; trial < 50; trial++ {
		b1, n1 := randomPair(rng, universe)
		b2, n2 := randomPair(rng, universe)
		u, _ := b1.Union(b2)
		n1.union(n2).assertMatches(t, u, universe)
		in, _ := b1.Intersect(b2)
		n1.intersect(n2).assertMatches(t, in, universe)
		d, _ := b1.Difference(b2)
		n1.difference(n2).assertMatches(t, d, universe)
	}
}

func TestOpsResultIsCanonical(t *testing.T) {
	// Craft inputs whose raw merge would leave adjacent same-value
	// segments or trailing zeros if normalization were skipped.
	a := New()
	a.SetRange(0, 9)
	a.SetRange(20, 29)
	b := New()
	b.SetRange(10, 19)
	u, _ := a.Union(b) // merges into [0,29]
	if u.Runs() != 1 || u.Count() != 30 {
		t.Fatalf("union runs=%d count=%d, want 1/30", u.Runs(), u.Count())
	}
	d, _ := a.Difference(b) // [0,9] and [20,29] minus nothing -> [0,9] only
	if err := d.Verify(); err != nil {
		t.Fatalf("difference not canonical: %v", err)
	}
	e, _ := a.Difference(a) // empty
	if e.Runs() != 0 || len(e.Bytes()) != 1 {
		t.Fatalf("self-difference: runs=%d bytes=%v", e.Runs(), e.Bytes())
	}
}

// TestHugeInputsFewSteps: two sets with two runs each covering ten
// million bits in total; the merge must advance only a handful of runs.
func TestHugeInputsFewSteps(t *testing.T) {
	a := New()
	a.SetRange(1_000_000, 5_999_999) // 5M bits, 2 runs
	b := New()
	b.SetRange(4_000_000, 8_999_999) // 5M bits, 2 runs
	if a.Runs() != 2 || b.Runs() != 2 {
		t.Fatalf("setup: runs a=%d b=%d, want 2/2", a.Runs(), b.Runs())
	}
	u, su := a.Union(b)
	in, si := a.Intersect(b)
	d, sd := a.Difference(b)
	for name, s := range map[string]OpStats{"union": su, "intersect": si, "difference": sd} {
		if s.Steps >= 10 {
			t.Errorf("%s advanced %d runs, want a single-digit count", name, s.Steps)
		}
	}
	if got := u.Count(); got != 8_000_000 {
		t.Errorf("union count = %d, want 8_000_000", got)
	}
	if got := in.Count(); got != 2_000_000 {
		t.Errorf("intersect count = %d, want 2_000_000", got)
	}
	if got := d.Count(); got != 3_000_000 {
		t.Errorf("difference count = %d, want 3_000_000", got)
	}
}
