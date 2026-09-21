package ontology

import (
	"errors"
	"math"
	"testing"
)

// offsetSamples returns 1e9 + {0,1,2,3,4}, whose true sample variance
// is exactly 2.5 and whose naive two-pass variance collapses in
// float64 arithmetic.
func offsetSamples() []float64 {
	base := 1e9
	return []float64{base + 0, base + 1, base + 2, base + 3, base + 4}
}

// naiveSampleVariance uses the textbook sum-of-squares formula and is
// expected to fail on offsetSamples: it exists to prove the test data
// actually breaks the naive approach.
func naiveSampleVariance(xs []float64) float64 {
	var sum, sumSq float64
	for _, x := range xs {
		sum += x
		sumSq += x * x
	}
	n := float64(len(xs))
	return (sumSq - sum*sum/n) / (n - 1)
}

func TestOffsetSamplesSampleVariance(t *testing.T) {
	a := New()
	for _, x := range offsetSamples() {
		a.Add(x)
	}
	got, err := a.SampleVariance()
	if err != nil {
		t.Fatalf("SampleVariance: %v", err)
	}
	const want = 2.5
	if rel := math.Abs(got-want) / want; rel > 1e-12 {
		t.Fatalf("sample variance = %v, want %v (rel err %g)", got, want, rel)
	}
}

func TestOffsetSamplesPopulationVariance(t *testing.T) {
	a := New()
	for _, x := range offsetSamples() {
		a.Add(x)
	}
	got, err := a.PopulationVariance()
	if err != nil {
		t.Fatalf("PopulationVariance: %v", err)
	}
	const want = 2.0
	if rel := math.Abs(got-want) / want; rel > 1e-12 {
		t.Fatalf("population variance = %v, want %v (rel err %g)", got, want, rel)
	}
}

func TestNaiveFormulaFailsOnOffsetSamples(t *testing.T) {
	// Guard the premise of the stability tests: the naive formula must
	// be visibly wrong on this data, otherwise the tests above would
	// not distinguish a stable algorithm from a naive one.
	got := naiveSampleVariance(offsetSamples())
	if rel := math.Abs(got-2.5) / 2.5; rel <= 1e-12 {
		t.Fatalf("naive formula unexpectedly accurate: got %v", got)
	}
}

func TestIdenticalSamplesVarianceExactlyZero(t *testing.T) {
	a := New()
	for i := 0; i < 1000; i++ {
		a.Add(3.14159)
	}
	pop, err := a.PopulationVariance()
	if err != nil {
		t.Fatalf("PopulationVariance: %v", err)
	}
	if pop != 0 {
		t.Fatalf("population variance = %v, want exact 0", pop)
	}
	samp, err := a.SampleVariance()
	if err != nil {
		t.Fatalf("SampleVariance: %v", err)
	}
	if samp != 0 {
		t.Fatalf("sample variance = %v, want exact 0", samp)
	}
}

func TestVarianceNeverNegative(t *testing.T) {
	a := New()
	for i := 0; i < 10000; i++ {
		a.Add(1e9)
	}
	pop, err := a.PopulationVariance()
	if err != nil {
		t.Fatalf("PopulationVariance: %v", err)
	}
	if pop < 0 || math.Signbit(pop) {
		t.Fatalf("population variance = %v, must not be negative", pop)
	}
}

func TestZeroSamplesErrors(t *testing.T) {
	a := New()
	if _, err := a.Mean(); !errors.Is(err, ErrNoSamples) {
		t.Fatalf("Mean err = %v, want ErrNoSamples", err)
	}
	if _, err := a.PopulationVariance(); !errors.Is(err, ErrNoSamples) {
		t.Fatalf("PopulationVariance err = %v, want ErrNoSamples", err)
	}
	if _, err := a.SampleVariance(); !errors.Is(err, ErrNoSamples) {
		t.Fatalf("SampleVariance err = %v, want ErrNoSamples", err)
	}
}

func TestSingleSample(t *testing.T) {
	a := New()
	a.Add(42.5)
	if got := a.Count(); got != 1 {
		t.Fatalf("Count = %d, want 1", got)
	}
	mean, err := a.Mean()
	if err != nil {
		t.Fatalf("Mean: %v", err)
	}
	if mean != 42.5 {
		t.Fatalf("Mean = %v, want 42.5", mean)
	}
	pop, err := a.PopulationVariance()
	if err != nil {
		t.Fatalf("PopulationVariance: %v", err)
	}
	if pop != 0 {
		t.Fatalf("PopulationVariance = %v, want exact 0", pop)
	}
	if _, err := a.SampleVariance(); !errors.Is(err, ErrDegenerateFreedom) {
		t.Fatalf("SampleVariance err = %v, want ErrDegenerateFreedom", err)
	}
}

func TestZeroAndSingleSampleErrorsDistinguishable(t *testing.T) {
	empty := New()
	one := New()
	one.Add(1)
	_, errEmpty := empty.SampleVariance()
	_, errOne := one.SampleVariance()
	if !errors.Is(errEmpty, ErrNoSamples) || errors.Is(errEmpty, ErrDegenerateFreedom) {
		t.Fatalf("empty SampleVariance err = %v, want pure ErrNoSamples", errEmpty)
	}
	if !errors.Is(errOne, ErrDegenerateFreedom) || errors.Is(errOne, ErrNoSamples) {
		t.Fatalf("single SampleVariance err = %v, want pure ErrDegenerateFreedom", errOne)
	}
}
