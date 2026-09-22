package ontology

import (
	"math"
	"math/rand/v2"
	"testing"
)

func feed(xs []float64) *Accumulator {
	a := New()
	for _, x := range xs {
		if err := a.Add(x); err != nil {
			panic(err)
		}
	}
	return a
}

func mustMean(t *testing.T, a *Accumulator) float64 {
	t.Helper()
	m, err := a.Mean()
	if err != nil {
		t.Fatalf("Mean: %v", err)
	}
	return m
}

func mustSampleVariance(t *testing.T, a *Accumulator) float64 {
	t.Helper()
	v, err := a.SampleVariance()
	if err != nil {
		t.Fatalf("SampleVariance: %v", err)
	}
	return v
}

// Merge(a, b) 与 Merge(b, a) 必须逐位相同。
func TestMergeCommutativeBitwise(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	for trial := 0; trial < 200; trial++ {
		na := 1 + rng.IntN(50)
		nb := 1 + rng.IntN(50)
		xs := make([]float64, na)
		ys := make([]float64, nb)
		for i := range xs {
			xs[i] = rng.NormFloat64() * 1e6
		}
		for i := range ys {
			ys[i] = rng.NormFloat64() * 1e-3
		}
		ab := Merge(feed(xs), feed(ys))
		ba := Merge(feed(ys), feed(xs))
		if ab.Count() != ba.Count() {
			t.Fatalf("count mismatch: %d vs %d", ab.Count(), ba.Count())
		}
		if got, want := mustMean(t, ab), mustMean(t, ba); math.Float64bits(got) != math.Float64bits(want) {
			t.Fatalf("trial %d: mean not bitwise commutative: %v vs %v", trial, got, want)
		}
		if got, want := mustSampleVariance(t, ab), mustSampleVariance(t, ba); math.Float64bits(got) != math.Float64bits(want) {
			t.Fatalf("trial %d: sample variance not bitwise commutative: %v vs %v", trial, got, want)
		}
	}
}

// 一万个样本随机切段、各自累加、两两合并，与一次性喂入相对误差 < 1e-12。
func TestMergeRandomSegmentsMatchesOneShot(t *testing.T) {
	rng := rand.New(rand.NewPCG(42, 7))
	const total = 10000
	xs := make([]float64, total)
	for i := range xs {
		xs[i] = rng.NormFloat64()*100 + 5
	}

	var segments []*Accumulator
	for i := 0; i < total; {
		size := 1 + rng.IntN(97)
		if i+size > total {
			size = total - i
		}
		segments = append(segments, feed(xs[i:i+size]))
		i += size
	}

	merged := segments[0]
	for _, seg := range segments[1:] {
		merged = Merge(merged, seg)
	}
	oneShot := feed(xs)

	if merged.Count() != total {
		t.Fatalf("merged count = %d, want %d", merged.Count(), total)
	}
	if got, want := mustMean(t, merged), mustMean(t, oneShot); relErr(got, want) > 1e-12 {
		t.Fatalf("mean rel err too large: merged %v, one-shot %v", got, want)
	}
	if got, want := mustSampleVariance(t, merged), mustSampleVariance(t, oneShot); relErr(got, want) > 1e-12 {
		t.Fatalf("sample variance rel err too large: merged %v, one-shot %v", got, want)
	}
}

// 合并空累加器是恒等操作；两个空的合并仍是空。
func TestMergeEmptyIdentity(t *testing.T) {
	a := feed([]float64{1e9, 1e9 + 1, 1e9 + 2})
	empty := New()

	for name, m := range map[string]*Accumulator{
		"Merge(a, empty)": Merge(a, empty),
		"Merge(empty, a)": Merge(empty, a),
	} {
		if m.Count() != a.Count() {
			t.Fatalf("%s: count = %d, want %d", name, m.Count(), a.Count())
		}
		if got, want := mustMean(t, m), mustMean(t, a); math.Float64bits(got) != math.Float64bits(want) {
			t.Fatalf("%s: mean not bitwise identical: %v vs %v", name, got, want)
		}
		if got, want := mustSampleVariance(t, m), mustSampleVariance(t, a); math.Float64bits(got) != math.Float64bits(want) {
			t.Fatalf("%s: sample variance not bitwise identical: %v vs %v", name, got, want)
		}
	}

	both := Merge(New(), New())
	if both.Count() != 0 {
		t.Fatalf("merge of two empties: count = %d, want 0", both.Count())
	}
	if _, err := both.Mean(); err == nil {
		t.Fatal("merge of two empties: Mean should still report ErrNoSamples")
	}
}
