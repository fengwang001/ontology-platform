package ontology

import (
	"math/rand"
	"testing"
)

// fromRuns builds a set directly from a normalized run list (test-only
// shortcut so we can construct huge sets without per-bit inserts).
func fromRuns(runs ...run) *Set {
	return &Set{runs: normalize(append([]run(nil), runs...))}
}

func TestMergeStepsTinyForHugeRuns(t *testing.T) {
	// Two sets, each with exactly two runs, together covering ten
	// million bits.
	a := fromRuns(run{0, 1_000_000}, run{1, 4_000_000}) // [1e6, 5e6)
	b := fromRuns(run{0, 3_000_000}, run{1, 4_000_000}) // [3e6, 7e6)
	if a.Runs() != 2 || b.Runs() != 2 {
		t.Fatalf("runs: a=%d b=%d", a.Runs(), b.Runs())
	}
	inter := a.Intersect(b)
	if got := inter.LastMergeSteps(); got >= 10 {
		t.Fatalf("intersect steps = %d, want single digit", got)
	}
	if got := inter.Count(); got != 2_000_000 { // [3e6, 5e6)
		t.Fatalf("intersect count = %d", got)
	}
	union := a.Union(b)
	if got := union.LastMergeSteps(); got >= 10 {
		t.Fatalf("union steps = %d, want single digit", got)
	}
	if got := union.Count(); got != 6_000_000 { // [1e6, 7e6)
		t.Fatalf("union count = %d", got)
	}
	diff := a.Difference(b)
	if got := diff.LastMergeSteps(); got >= 10 {
		t.Fatalf("difference steps = %d, want single digit", got)
	}
	if got := diff.Count(); got != 2_000_000 { // [1e6, 3e6)
		t.Fatalf("difference count = %d", got)
	}
	for _, s := range []*Set{inter, union, diff} {
		if !s.Verify() {
			t.Fatal("merge result not normalized")
		}
	}
}

func TestMergeEquivalentToNaive(t *testing.T) {
	const limit = 4096
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 200; trial++ {
		a, b := New(), New()
		na, nb := newNaive(), newNaive()
		for _, p := range randRuns(rng, limit) {
			a.Set(p)
			na.set(p)
		}
		for _, p := range randRuns(rng, limit) {
			b.Set(p)
			nb.set(p)
		}
		cases := []struct {
			name string
			got  *Set
			want *naiveBitmap
		}{
			{"union", a.Union(b), na.union(nb)},
			{"intersect", a.Intersect(b), na.intersect(nb)},
			{"difference", a.Difference(b), na.difference(nb)},
		}
		for _, c := range cases {
			if !equalSet(c.got, c.want, limit) {
				t.Fatalf("trial %d %s mismatch", trial, c.name)
			}
			if !c.got.Verify() {
				t.Fatalf("trial %d %s: result not normalized", trial, c.name)
			}
		}
	}
}

// randRuns returns positions clustered into random runs, so the
// compressed representation has realistic structure.
func randRuns(rng *rand.Rand, limit uint32) []uint32 {
	var out []uint32
	for i := 0; i < 8; i++ {
		start := rng.Intn(int(limit))
		length := rng.Intn(200)
		for j := 0; j < length && uint32(start+j) < limit; j++ {
			out = append(out, uint32(start+j))
		}
	}
	return out
}

func TestMergeSelfOperands(t *testing.T) {
	s := New()
	s.Set(1)
	s.Set(100)
	if got := s.Union(s); got.Count() != 2 {
		t.Fatalf("self union count = %d", got.Count())
	}
	if got := s.Intersect(s); got.Count() != 2 {
		t.Fatalf("self intersect count = %d", got.Count())
	}
	if got := s.Difference(s); got.Count() != 0 || len(got.Bytes()) != 1 {
		t.Fatalf("self difference count = %d", got.Count())
	}
}

func TestMergeEmptyOperands(t *testing.T) {
	e, s := New(), New()
	s.Set(7)
	if got := e.Union(s); got.Count() != 1 {
		t.Fatal("empty union failed")
	}
	if got := e.Intersect(s); got.Count() != 0 {
		t.Fatal("empty intersect failed")
	}
	if got := s.Difference(e); got.Count() != 1 {
		t.Fatal("difference with empty failed")
	}
	if got := e.Difference(s); got.Count() != 0 {
		t.Fatal("empty difference failed")
	}
}
