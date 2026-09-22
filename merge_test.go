package ontology

import (
	"errors"
	"math"
	"math/rand/v2"
	"testing"
)

func statBits(t *testing.T, a *Accumulator) (mean, pv, sv uint64) {
	t.Helper()
	m, err := a.Mean()
	if err != nil {
		t.Fatalf("Mean: %v", err)
	}
	p, err := a.Variance()
	if err != nil {
		t.Fatalf("Variance: %v", err)
	}
	s, err := a.SampleVariance()
	if err != nil {
		t.Fatalf("SampleVariance: %v", err)
	}
	return math.Float64bits(m), math.Float64bits(p), math.Float64bits(s)
}

func fillAcc(rng *rand.Rand, n int, lo, hi float64) *Accumulator {
	a := New()
	for i := 0; i < n; i++ {
		_ = a.Add(lo + rng.Float64()*(hi-lo))
	}
	return a
}

func TestMergeCommutativeBitwise(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	for trial := 0; trial < 50; trial++ {
		a := fillAcc(rng, 1+rng.IntN(200), -1e7, 1e7)
		b := fillAcc(rng, 1+rng.IntN(200), -1e7, 1e7)
		ab := Merge(a, b)
		ba := Merge(b, a)
		m1, p1, s1 := statBits(t, ab)
		m2, p2, s2 := statBits(t, ba)
		if m1 != m2 || p1 != p2 || s1 != s2 {
			t.Fatalf("trial %d: Merge(a,b) != Merge(b,a) bitwise: (%x,%x,%x) vs (%x,%x,%x)",
				trial, m1, p1, s1, m2, p2, s2)
		}
		if ab.Count() != ba.Count() {
			t.Fatalf("trial %d: count mismatch %d vs %d", trial, ab.Count(), ba.Count())
		}
	}
}

func TestMergeRandomSplitsMatchesOneShot(t *testing.T) {
	rng := rand.New(rand.NewPCG(99, 7))
	const total = 10000
	oneShot := New()
	segments := make([]*Accumulator, 9)
	for i := range segments {
		segments[i] = New()
	}
	for i := 0; i < total; i++ {
		x := rng.NormFloat64()*1e5 + 3e8
		_ = oneShot.Add(x)
		_ = segments[rng.IntN(len(segments))].Add(x)
	}
	merged := New()
	for _, seg := range segments {
		merged = Merge(merged, seg)
	}
	if merged.Count() != total {
		t.Fatalf("merged Count = %d, want %d", merged.Count(), total)
	}
	mWant := mustMean(t, oneShot)
	mGot := mustMean(t, merged)
	if relErr(mGot, mWant) > 1e-12 {
		t.Fatalf("merged Mean = %v, one-shot %v (rel err %g)", mGot, mWant, relErr(mGot, mWant))
	}
	svWant, _ := oneShot.SampleVariance()
	svGot, _ := merged.SampleVariance()
	if relErr(svGot, svWant) > 1e-12 {
		t.Fatalf("merged SampleVariance = %v, one-shot %v (rel err %g)", svGot, svWant, relErr(svGot, svWant))
	}
	pvWant, _ := oneShot.Variance()
	pvGot, _ := merged.Variance()
	if relErr(pvGot, pvWant) > 1e-12 {
		t.Fatalf("merged Variance = %v, one-shot %v (rel err %g)", pvGot, pvWant, relErr(pvGot, pvWant))
	}
}

func TestMergeEmptyIdentity(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4))
	a := fillAcc(rng, 500, -100, 100)
	mWant, pWant, sWant := statBits(t, a)
	for name, merged := range map[string]*Accumulator{
		"Merge(a, empty)": Merge(a, New()),
		"Merge(empty, a)": Merge(New(), a),
	} {
		m, p, s := statBits(t, merged)
		if m != mWant || p != pWant || s != sWant {
			t.Fatalf("%s changed statistics bitwise: (%x,%x,%x) vs (%x,%x,%x)",
				name, m, p, s, mWant, pWant, sWant)
		}
		if merged.Count() != a.Count() {
			t.Fatalf("%s: Count = %d, want %d", name, merged.Count(), a.Count())
		}
	}
	both := Merge(New(), New())
	if both.Count() != 0 {
		t.Fatalf("Merge(empty, empty).Count = %d, want 0", both.Count())
	}
	if _, err := both.Mean(); !errors.Is(err, ErrNoSamples) {
		t.Fatalf("Merge(empty, empty).Mean err = %v, want ErrNoSamples", err)
	}
}

func TestMergeDoesNotModifySources(t *testing.T) {
	rng := rand.New(rand.NewPCG(5, 6))
	a := fillAcc(rng, 300, 0, 1000)
	b := fillAcc(rng, 200, -50, 50)
	am, ap, as := statBits(t, a)
	bm, bp, bs := statBits(t, b)
	an, bn := a.Count(), b.Count()
	_ = Merge(a, b)
	am2, ap2, as2 := statBits(t, a)
	bm2, bp2, bs2 := statBits(t, b)
	if am != am2 || ap != ap2 || as != as2 || a.Count() != an {
		t.Fatal("Merge modified its first source accumulator")
	}
	if bm != bm2 || bp != bp2 || bs != bs2 || b.Count() != bn {
		t.Fatal("Merge modified its second source accumulator")
	}
}
