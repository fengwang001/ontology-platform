package ontology

import (
	"math"
	"math/rand/v2"
	"testing"
)

// statsOf reads the full observable state of an accumulator for
// bitwise comparison.
type statsOf struct {
	n, skipped     int64
	mean, pop, sam float64
}

func readStats(t *testing.T, a *Accumulator) statsOf {
	t.Helper()
	mean, err := a.Mean()
	if err != nil {
		t.Fatalf("Mean: %v", err)
	}
	pop, err := a.PopulationVariance()
	if err != nil {
		t.Fatalf("PopulationVariance: %v", err)
	}
	sam, err := a.SampleVariance()
	if err != nil {
		t.Fatalf("SampleVariance: %v", err)
	}
	return statsOf{n: a.Count(), skipped: a.Skipped(), mean: mean, pop: pop, sam: sam}
}

func TestMergeCommutativeBitwise(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	a, b := New(), New()
	for i := 0; i < 500; i++ {
		a.Add(rng.NormFloat64() * 1e6)
		b.Add(rng.NormFloat64() * 1e-6)
	}
	ab := readStats(t, Merge(a, b))
	ba := readStats(t, Merge(b, a))
	if ab != ba {
		t.Fatalf("Merge(a,b)=%+v != Merge(b,a)=%+v", ab, ba)
	}
}

func TestMergeEmptyIdentity(t *testing.T) {
	empty1, empty2 := New(), New()
	both := Merge(empty1, empty2)
	if got := both.Count(); got != 0 {
		t.Fatalf("Merge(empty,empty).Count = %d, want 0", got)
	}
	if _, err := both.Mean(); err == nil {
		t.Fatal("Merge(empty,empty).Mean should fail")
	}

	a := New()
	for _, x := range []float64{1, 2, 3, 4, 5} {
		a.Add(x)
	}
	before := readStats(t, a)
	if got := readStats(t, Merge(a, New())); got != before {
		t.Fatalf("Merge(a,empty) = %+v, want %+v", got, before)
	}
	if got := readStats(t, Merge(New(), a)); got != before {
		t.Fatalf("Merge(empty,a) = %+v, want %+v", got, before)
	}
}

func TestMergeDoesNotModifySources(t *testing.T) {
	a, b := New(), New()
	for _, x := range []float64{1, 2, 3} {
		a.Add(x)
	}
	for _, x := range []float64{10, 20} {
		b.Add(x)
	}
	aBefore := readStats(t, a)
	bBefore := readStats(t, b)
	_ = Merge(a, b)
	if got := readStats(t, a); got != aBefore {
		t.Fatalf("a changed after Merge: %+v -> %+v", aBefore, got)
	}
	if got := readStats(t, b); got != bBefore {
		t.Fatalf("b changed after Merge: %+v -> %+v", bBefore, got)
	}
}

func TestRandomSegmentMergeMatchesOneShot(t *testing.T) {
	const total = 10000
	rng := rand.New(rand.NewPCG(42, 7))
	samples := make([]float64, total)
	oneShot := New()
	for i := range samples {
		samples[i] = rng.NormFloat64() * 1e3
		oneShot.Add(samples[i])
	}

	// Split into random-sized segments, accumulate each, merge pairwise.
	var segments []*Accumulator
	for i := 0; i < total; {
		size := 1 + rng.IntN(997)
		if i+size > total {
			size = total - i
		}
		seg := New()
		for _, x := range samples[i : i+size] {
			seg.Add(x)
		}
		segments = append(segments, seg)
		i += size
	}
	merged := segments[0]
	for _, seg := range segments[1:] {
		merged = Merge(merged, seg)
	}

	if got := merged.Count(); got != total {
		t.Fatalf("merged Count = %d, want %d", got, total)
	}
	checkClose(t, "mean", merged, oneShot)
	checkClose(t, "population variance", merged, oneShot)
	checkClose(t, "sample variance", merged, oneShot)
}

func checkClose(t *testing.T, what string, got, want *Accumulator) {
	t.Helper()
	var g, w float64
	var err error
	switch what {
	case "mean":
		g, err = got.Mean()
		w, _ = want.Mean()
	case "population variance":
		g, err = got.PopulationVariance()
		w, _ = want.PopulationVariance()
	default:
		g, err = got.SampleVariance()
		w, _ = want.SampleVariance()
	}
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
	if rel := math.Abs(g-w) / math.Abs(w); rel > 1e-12 {
		t.Fatalf("%s: merged = %v, one-shot = %v (rel err %g)", what, g, w, rel)
	}
}

func TestMergeOffsetSegmentsAccurate(t *testing.T) {
	// Merge two halves of the 1e9-offset data; the merged sample
	// variance must still match the true value 2.5.
	a, b := New(), New()
	a.Add(1e9 + 0)
	a.Add(1e9 + 1)
	b.Add(1e9 + 2)
	b.Add(1e9 + 3)
	b.Add(1e9 + 4)
	got, err := Merge(a, b).SampleVariance()
	if err != nil {
		t.Fatalf("SampleVariance: %v", err)
	}
	if rel := math.Abs(got-2.5) / 2.5; rel > 1e-12 {
		t.Fatalf("merged sample variance = %v, want 2.5 (rel err %g)", got, rel)
	}
}
