package ontology

import (
	"math"
	"math/rand/v2"
	"testing"
)

// Merge 不得修改两个源累加器：合并前后读数逐位相同。
func TestMergeDoesNotModifySources(t *testing.T) {
	a := feed([]float64{1, 2, 3, 4})
	b := feed([]float64{10, 20, 30})

	aMean, aVar := mustMean(t, a), mustSampleVariance(t, a)
	bMean, bVar := mustMean(t, b), mustSampleVariance(t, b)
	aCount, bCount := a.Count(), b.Count()

	_ = Merge(a, b)
	_ = Merge(b, a)

	if got := mustMean(t, a); math.Float64bits(got) != math.Float64bits(aMean) {
		t.Fatalf("source a mean changed: %v -> %v", aMean, got)
	}
	if got := mustSampleVariance(t, a); math.Float64bits(got) != math.Float64bits(aVar) {
		t.Fatalf("source a sample variance changed: %v -> %v", aVar, got)
	}
	if got := mustMean(t, b); math.Float64bits(got) != math.Float64bits(bMean) {
		t.Fatalf("source b mean changed: %v -> %v", bMean, got)
	}
	if got := mustSampleVariance(t, b); math.Float64bits(got) != math.Float64bits(bVar) {
		t.Fatalf("source b sample variance changed: %v -> %v", bVar, got)
	}
	if a.Count() != aCount || b.Count() != bCount {
		t.Fatalf("source counts changed: %d/%d -> %d/%d", aCount, bCount, a.Count(), b.Count())
	}
}

// 合并结果必须与「两批样本按任意顺序喂进同一个累加器」一致。
func TestMergeMatchesInterleavedFeeding(t *testing.T) {
	rng := rand.New(rand.NewPCG(9, 9))
	xs := make([]float64, 500)
	ys := make([]float64, 700)
	for i := range xs {
		xs[i] = rng.NormFloat64()
	}
	for i := range ys {
		ys[i] = rng.NormFloat64()
	}

	merged := Merge(feed(xs), feed(ys))

	interleaved := New()
	i, j := 0, 0
	for i < len(xs) || j < len(ys) {
		if j >= len(ys) || (i < len(xs) && rng.IntN(2) == 0) {
			if err := interleaved.Add(xs[i]); err != nil {
				t.Fatal(err)
			}
			i++
		} else {
			if err := interleaved.Add(ys[j]); err != nil {
				t.Fatal(err)
			}
			j++
		}
	}

	if got, want := mustMean(t, merged), mustMean(t, interleaved); relErr(got, want) > 1e-12 {
		t.Fatalf("mean rel err too large: merged %v, interleaved %v", got, want)
	}
	if got, want := mustSampleVariance(t, merged), mustSampleVariance(t, interleaved); relErr(got, want) > 1e-12 {
		t.Fatalf("sample variance rel err too large: merged %v, interleaved %v", got, want)
	}
}
