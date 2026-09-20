// Command demo exercises the quantile package and prints one OK/FAIL
// judgement line per behavior, plus a final tally. It takes no arguments and
// performs no network access.
package main

import (
	"errors"
	"fmt"
	"math"
	"math/rand/v2"

	"ontology"
)

var passed, failed int

func check(name string, ok bool, detail string) {
	if ok {
		passed++
		fmt.Printf("OK   %s %s\n", name, detail)
	} else {
		failed++
		fmt.Printf("FAIL %s %s\n", name, detail)
	}
}

func bits(v float64) uint64 { return math.Float64bits(v) }

func main() {
	s := quantile.NewSketch()
	for _, v := range []float64{10, 20, 30, 40} {
		_ = s.Add(v)
	}

	// 1. Definitions differ strictly between samples.
	nr, _ := s.QuantileNearestRank(0.5)
	lin, _ := s.QuantileLinear(0.5)
	check("definitions differ between samples",
		nr == 20 && lin == 25 && bits(nr) != bits(lin),
		fmt.Sprintf("p=0.5 nearest=%v linear=%v", nr, lin))

	// 2. Definitions agree bit-for-bit on a sample point (h=2 exactly).
	onNR, _ := s.QuantileNearestRank(1.0 / 3.0)
	onLin, _ := s.QuantileLinear(1.0 / 3.0)
	check("definitions agree on a sample point",
		onNR == 20 && bits(onNR) == bits(onLin),
		fmt.Sprintf("p=1/3 both=%v", onNR))

	// 3. Boundaries return real min/max under both definitions.
	minNR, _ := s.QuantileNearestRank(0)
	maxNR, _ := s.QuantileNearestRank(1)
	minL, _ := s.QuantileLinear(0)
	maxL, _ := s.QuantileLinear(1)
	check("p=0 min and p=1 max are real samples",
		minNR == 10 && maxNR == 40 && bits(minL) == bits(minNR) && bits(maxL) == bits(maxNR),
		fmt.Sprintf("min=%v max=%v", minNR, maxNR))

	// 4. Two distinguishable error classes.
	empty := quantile.NewSketch()
	_, badP := s.QuantileLinear(math.NaN())
	_, emptyErr := empty.QuantileLinear(0.5)
	check("invalid p vs empty data are distinct errors",
		errors.Is(badP, quantile.ErrInvalidProbability) &&
			errors.Is(emptyErr, quantile.ErrEmpty) &&
			!errors.Is(badP, quantile.ErrEmpty),
		fmt.Sprintf("invalid=%v empty=%v", badP, emptyErr))

	// 5. Weight 3 is bit-identical to three unit inserts.
	weighted := quantile.NewSketch()
	_ = weighted.Add(7.5, 3)
	repeated := quantile.NewSketch()
	for i := 0; i < 3; i++ {
		_ = repeated.Add(7.5)
	}
	wNR, _ := weighted.QuantileNearestRank(0.42)
	rNR, _ := repeated.QuantileNearestRank(0.42)
	wL, _ := weighted.QuantileLinear(0.42)
	rL, _ := repeated.QuantileLinear(0.42)
	check("weight 3 equals three repeats bit-for-bit",
		bits(wNR) == bits(rNR) && bits(wL) == bits(rL),
		fmt.Sprintf("nearest bits=%d linear bits=%d", bits(wNR), bits(wL)))

	// 6. >1e6 total weight with only a handful of unique values.
	big := quantile.NewSketch()
	weights := []struct {
		v float64
		w uint64
	}{{0, 800000}, {1, 250000}, {2, 150000}, {3, 49999}, {4, 1}}
	for _, e := range weights {
		_ = big.Add(e.v, e.w)
	}
	check("million total weight, tiny unique count",
		big.TotalWeight() > 1_000_000 && big.UniqueCount() == 5,
		fmt.Sprintf("total=%d unique=%d", big.TotalWeight(), big.UniqueCount()))

	// 7. +0.0 / -0.0 merge into one +0.0 bucket.
	z := quantile.NewSketch()
	_ = z.Add(math.Copysign(0, -1), 2)
	_ = z.Add(0, 3)
	zv, _ := z.QuantileLinear(0.5)
	check("+0.0 and -0.0 merge as +0.0",
		z.UniqueCount() == 1 && z.TotalWeight() == 5 && bits(zv) == bits(0.0),
		fmt.Sprintf("unique=%d total=%d value=%v", z.UniqueCount(), z.TotalWeight(), zv))

	// 8. NaN samples rejected and counted.
	n := quantile.NewSketch()
	nanErrs := 0
	for i := 0; i < 3; i++ {
		if errors.Is(n.Add(math.NaN()), quantile.ErrNaNValue) {
			nanErrs++
		}
	}
	_ = n.Add(2)
	nv, _ := n.QuantileNearestRank(0.5)
	check("NaN rejected, counted and never used",
		nanErrs == 3 && n.SkippedNaN() == 3 && n.UniqueCount() == 1 && nv == 2,
		fmt.Sprintf("skipped=%d value=%v", n.SkippedNaN(), nv))

	// 9. Infinities sort and interpolate correctly.
	inf := quantile.NewSketch()
	for _, v := range []float64{math.Inf(-1), 1, math.Inf(1)} {
		_ = inf.Add(v)
	}
	between, _ := inf.QuantileLinear(0.9)
	exact, _ := inf.QuantileLinear(1)
	check("infinities participate in sorting and interpolation",
		math.IsInf(between, 1) && math.IsInf(exact, 1),
		fmt.Sprintf("between=%v exact=%v (no NaN)", between, exact))

	// 10. Insertion order independence, bit-for-bit.
	base := []struct {
		v float64
		w uint64
	}{{1, 4}, {2, 1}, {3, 9}, {4, 2}, {5, 5}}
	makeSketch := func(perm []int) *quantile.Sketch {
		q := quantile.NewSketch()
		for _, idx := range perm {
			_ = q.Add(base[idx].v, base[idx].w)
		}
		return q
	}
	rng := rand.New(rand.NewPCG(42, 7))
	perm := []int{0, 1, 2, 3, 4}
	rng.Shuffle(len(perm), func(i, j int) { perm[i], perm[j] = perm[j], perm[i] })
	a := makeSketch([]int{0, 1, 2, 3, 4})
	b := makeSketch(perm)
	orderOK := true
	for i := 0; i <= 100; i++ {
		p := float64(i) / 100
		a1, _ := a.QuantileNearestRank(p)
		b1, _ := b.QuantileNearestRank(p)
		a2, _ := a.QuantileLinear(p)
		b2, _ := b.QuantileLinear(p)
		if bits(a1) != bits(b1) || bits(a2) != bits(b2) {
			orderOK = false
		}
	}
	check("shuffled insertion order gives identical bits", orderOK,
		fmt.Sprintf("perm=%v", perm))

	fmt.Printf("TOTAL %d passed, %d failed\n", passed, failed)
	if failed != 0 {
		panic("demo checks failed")
	}
}
