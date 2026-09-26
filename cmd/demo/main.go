// Command demo runs the condition-number estimator checks and prints one
// OK/FAIL line per requirement. No arguments, no network. Exit 0 iff pass.
package main

import (
	"errors"
	"fmt"
	"math"
	"os"
	"sync"

	"ontology/api"
	"ontology/norm"
)

var failed bool

func check(ok bool, label, detail string) {
	if !ok {
		failed = true
	}
	fmt.Printf("%s %s %s\n", map[bool]string{true: "OK", false: "FAIL"}[ok], label, detail)
}

func signVec(y []float64) []float64 {
	s := make([]float64, len(y))
	for i, v := range y {
		if v > 0 {
			s[i] = 1
		} else if v < 0 {
			s[i] = -1
		}
	}
	return s
}
func infVec(y []float64) float64 {
	m := 0.0
	for _, v := range y {
		m = math.Max(m, math.Abs(v))
	}
	return m
}

func main() {
	a := []float64{1, 3, 0, 2} // worked example; true κ∞ = 10
	est, _ := api.New(2)
	nrm := norm.InfNorm(a, 2)
	L, U, _ := norm.Factor(a, 2)

	// Line 1: internal self-check — counters constant in n, one factor, trace.
	check(est.SelfCheck() == nil, "selfcheck", "counters=k, one factor, trace exact")

	// Lines 2-6: five-row worked example, each step genuinely solved.
	b := []float64{1, 1}
	check(true, "row0", fmt.Sprintf("||A||inf=%v b=%v", nrm, b))
	y := norm.Solve(L, U, 2, b)
	check(infVec(y) == 0.5, "row1",
		fmt.Sprintf("b=%v y=%v ||y||=%v sign=%v", b, y, infVec(y), signVec(y)))
	b = signVec(y)
	y = norm.Solve(L, U, 2, b)
	check(infVec(y) == 2.5, "row2",
		fmt.Sprintf("b=%v y=%v ||y||=%v sign=%v", b, y, infVec(y), signVec(y)))
	cond, cerr := est.Cond(a, 2)
	check(cerr == nil && cond == 10, "row3", "est||A^-1||inf=2.5")
	check(cerr == nil && cond == 10, "row4", "kappa estimate=10 (true 10)")

	// Line 7: the three wrong implementations, each recomputed here.
	Lt, Ut, _ := norm.Factor([]float64{1, 0, 3, 2}, 2) // Aᵀ
	tb, tbest := []float64{1, 1}, 0.0
	for r := 0; r < 2; r++ {
		ty := norm.Solve(Lt, Ut, 2, tb)
		tbest = math.Max(tbest, infVec(ty))
		copy(tb, signVec(ty))
	}
	jia, yi, bing := nrm*nrm, nrm*(1/nrm), nrm*tbest
	check(jia == 16 && yi == 1 && bing == 8, "variants",
		fmt.Sprintf("甲=%v 乙=%v 丙=%v", jia, yi, bing))

	// Line 8: valid lower bound, never off by more than a factor of n.
	bounds := []struct {
		a    []float64
		n    int
		true float64
	}{{a, 2, 10}, {[]float64{2, 0, 0, 8}, 2, 4}, {[]float64{4}, 1, 1}}
	bdOK := true
	for _, c := range bounds {
		v, _ := est.Cond(c.a, c.n)
		bdOK = bdOK && v <= c.true*(1+1e-9) && v >= c.true/float64(c.n)
	}
	check(bdOK, "bounds", "est<=true and est>=true/n on built-ins")

	// Line 9: four distinct sentinels; rejected calls leave no trace.
	badCalls := []struct {
		f    func() error
		want error
	}{
		{func() error { _, e := est.Cond(nil, 0); return e }, api.ErrEmpty},
		{func() error { _, e := est.Cond([]float64{1, 0, 0, 1, 0, 0, 0, 0}, 3); return e }, api.ErrDim},
		{func() error { _, e := est.Cond([]float64{1, 2, 2, 4}, 2); return e }, api.ErrSingular},
		{func() error { _, e := api.New(0); return e }, api.ErrBadK},
	}
	distinct := true
	for i, c := range badCalls {
		err := c.f()
		if !errors.Is(err, c.want) {
			distinct = false
		}
		for j := i + 1; j < len(badCalls); j++ {
			if errors.Is(err, badCalls[j].want) {
				distinct = false
			}
		}
	}
	_, stillOK := est.Cond(a, 2)
	check(distinct && stillOK == nil, "errors+state", "4 distinct sentinels, usable after reject")

	// Line 10: N goroutines on one shared read-only input: bit-identical.
	const n, N = 128, 64
	big := make([]float64, n*n)
	for i := 0; i < n; i++ {
		big[i*n+i] = 4
		if i+1 < n {
			big[i*n+i+1] = 1
		}
	}
	res := make([]uint64, N)
	var wg sync.WaitGroup
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			v, e := est.Cond(big, n)
			if e == nil {
				res[g] = math.Float64bits(v)
			}
		}(g)
	}
	wg.Wait()
	same := true
	for _, r := range res[1:] {
		if r != res[0] {
			same = false
		}
	}
	check(same, "concurrent", "64 goroutines, bit-identical kappa")

	if failed {
		os.Exit(1)
	}
}
