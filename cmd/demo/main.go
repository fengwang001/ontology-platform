// Command demo exercises the online statistics accumulator and prints
// one OK/FAIL verdict line per check. It takes no arguments, uses no
// network, and exits 0 when every check passes.
package main

import (
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"os"

	"ontology"
)

var passed, total int

func check(ok bool, format string, args ...any) {
	total++
	verdict := "FAIL"
	if ok {
		passed++
		verdict = "OK  "
	}
	fmt.Printf("%s %s\n", verdict, fmt.Sprintf(format, args...))
}

func relErr(got, want float64) float64 {
	return math.Abs(got-want) / math.Abs(want)
}

// naiveSampleVariance uses the catastrophic sum-of-squares formula.
func naiveSampleVariance(xs []float64) float64 {
	var sum, sumSq float64
	for _, x := range xs {
		sum += x
		sumSq += x * x
	}
	n := float64(len(xs))
	return (sumSq - sum*sum/n) / (n - 1)
}

func main() {
	offset := []float64{1e9, 1e9 + 1, 1e9 + 2, 1e9 + 3, 1e9 + 4}
	acc := ontology.New()
	for _, x := range offset {
		_ = acc.Add(x)
	}
	sv, err := acc.SampleVariance()
	check(err == nil && relErr(sv, 2.5) < 1e-12,
		"1e9-offset sample variance: got %.15g want 2.5 (naive formula: %.6g)", sv, naiveSampleVariance(offset))

	same := ontology.New()
	for i := 0; i < 1000; i++ {
		_ = same.Add(3.14)
	}
	pv, _ := same.Variance()
	sv2, _ := same.SampleVariance()
	check(pv == 0 && sv2 == 0, "identical samples: population=%g sample=%g (exact zero)", pv, sv2)

	rng := rand.New(rand.NewPCG(42, 7))
	oneShot := ontology.New()
	segments := make([]*ontology.Accumulator, 7)
	for i := range segments {
		segments[i] = ontology.New()
	}
	for i := 0; i < 10000; i++ {
		x := rng.NormFloat64()*1e6 + 5e8
		_ = oneShot.Add(x)
		_ = segments[rng.IntN(len(segments))].Add(x)
	}
	merged := ontology.New()
	for _, seg := range segments {
		merged = ontology.Merge(merged, seg)
	}
	m1, _ := oneShot.Mean()
	s1, _ := oneShot.SampleVariance()
	m2, _ := merged.Mean()
	s2, _ := merged.SampleVariance()
	check(merged.Count() == oneShot.Count() && relErr(m2, m1) < 1e-12 && relErr(s2, s1) < 1e-12,
		"random-split merge == one-shot: mean %.12g vs %.12g, sample var %.12g vs %.12g", m2, m1, s2, s1)

	srcA, srcB := ontology.New(), ontology.New()
	_ = srcA.Add(1)
	_ = srcA.Add(2)
	_ = srcB.Add(10)
	beforeA, _ := srcA.Mean()
	beforeB, _ := srcB.Mean()
	_ = ontology.Merge(srcA, srcB)
	afterA, _ := srcA.Mean()
	afterB, _ := srcB.Mean()
	check(math.Float64bits(beforeA) == math.Float64bits(afterA) &&
		math.Float64bits(beforeB) == math.Float64bits(afterB) &&
		srcA.Count() == 2 && srcB.Count() == 1,
		"Merge leaves sources untouched: A mean %g count %d, B mean %g count %d", afterA, srcA.Count(), afterB, srcB.Count())

	empty := ontology.New()
	_, errMean := empty.Mean()
	_, errSV := empty.SampleVariance()
	one := ontology.New()
	_ = one.Add(42)
	oneMean, _ := one.Mean()
	onePV, _ := one.Variance()
	_, errOneSV := one.SampleVariance()
	check(errors.Is(errMean, ontology.ErrNoSamples) && errors.Is(errSV, ontology.ErrNoSamples),
		"zero samples: Mean and SampleVariance fail with ErrNoSamples")
	check(oneMean == 42 && onePV == 0 &&
		errors.Is(errOneSV, ontology.ErrTooFewSamples) && !errors.Is(errOneSV, ontology.ErrNoSamples),
		"one sample: mean=42, population variance=0, sample variance fails with ErrTooFewSamples")

	nanAcc := ontology.New()
	nanErr := nanAcc.Add(math.NaN())
	_ = nanAcc.Add(1)
	_ = nanAcc.Add(3)
	nanMean, _ := nanAcc.Mean()
	check(errors.Is(nanErr, ontology.ErrNaN) && nanAcc.Skipped() == 1 && nanAcc.Count() == 2 && nanMean == 2,
		"NaN rejected: skipped=%d count=%d mean=%g (unpolluted)", nanAcc.Skipped(), nanAcc.Count(), nanMean)

	infAcc := ontology.New()
	_ = infAcc.Add(1)
	_ = infAcc.Add(math.Inf(1))
	_, errInf := infAcc.Mean()
	_, errInfV := infAcc.Variance()
	check(errors.Is(errInf, ontology.ErrStatsUnavailable) && errors.Is(errInfV, ontology.ErrStatsUnavailable),
		"infinity poisons statistics: Mean/Variance fail with ErrStatsUnavailable")

	fmt.Printf("TOTAL %d/%d checks passed\n", passed, total)
	if passed != total {
		os.Exit(1)
	}
}
