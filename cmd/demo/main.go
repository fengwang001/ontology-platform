// Command demo runs in-process self-checks of the least-squares packages.
package main

import (
	"errors"
	"fmt"
	"math"
	"os"
	"reflect"
	"sync"

	"ontology/api"
	"ontology/gram"
	"ontology/lsq"
)

var failed int

func report(name string, ok bool) {
	if ok {
		fmt.Println("OK  " + name)
	} else {
		failed++
		fmt.Println("FAIL " + name)
	}
}

func eq(x, y []float64, tol float64) bool {
	if len(x) != len(y) {
		return false
	}
	for i := range x {
		if math.Abs(x[i]-y[i]) > tol {
			return false
		}
	}
	return true
}

func main() {
	a := []float64{1, 1, 1, 2, 1, 3}
	b := []float64{2, 3, 5}
	m, n := 3, 2
	g := gram.Gram(a, m, n)
	c := gram.AtB(a, b, m, n)
	x, err := lsq.SolveG(g, c, n)
	axb := []float64{
		a[0]*x[0] + a[1]*x[1] - b[0],
		a[2]*x[0] + a[3]*x[1] - b[1],
		a[4]*x[0] + a[5]*x[1] - b[2],
	}
	resid := []float64{-axb[0], -axb[1], -axb[2]} // b−Ax
	report("Gram,AtB,eliminate,backsolve,residual",
		err == nil && eq(g, []float64{3, 6, 6, 14}, 0) && eq(c, []float64{10, 23}, 0) &&
			eq(x, []float64{1.0 / 3, 1.5}, 1e-12) &&
			eq(resid, []float64{1.0 / 6, -1.0 / 3, 1.0 / 6}, 1e-12))

	xa, _ := lsq.SolveG([]float64{3, 6, 0, 14}, c, n) // (甲) lower triangle zeroed
	missC0 := a[0]*b[0] + a[2]*b[1]                   // (乙) AtB[0] sums k=0..m-2 only
	missX0 := c[0] / g[0]                             // (丙) backsolve skips G[0][1]·x[1]
	report("(甲)[1/21,23/14] (乙)AtB[0]=5 (丙)x0=10/3",
		eq(xa, []float64{1.0 / 21, 23.0 / 14}, 1e-12) && missC0 == 5 && missX0 == 10.0/3.0)

	atr := gram.AtB(a, axb, m, n)
	report("optimal: Aᵀ(Ax−b)==0", math.Abs(atr[0]) <= 1e-9 && math.Abs(atr[1]) <= 1e-9)
	report("Gram symmetric bit-for-bit", g[1] == g[2])

	// The Gram counter is unexported; its "exactly once across many
	// Solves, including m=10000" guarantee is surfaced only through the
	// same-package SelfCheck (no exported accessor reads the count).
	report("multi-Solve reuse; m=10000 Gram count==1", api.New().SelfCheck() == nil)

	bad := api.New()
	report("four distinct decidable errors",
		errors.Is(bad.Factor(a[:5], 3, 2), api.ErrDimMismatch) &&
			errors.Is(bad.Factor(a, 2, 3), api.ErrUnderdetermined) &&
			errors.Is(bad.Factor(a, 0, 2), api.ErrEmptySystem) &&
			errors.Is(bad.Factor([]float64{1, 1, 1, 1, 1, 1}, 3, 2), api.ErrRankDeficient))

	good := api.New()
	if e := good.Factor(a, m, n); e != nil {
		report("state unchanged after rejection", false)
	} else {
		x0, _ := good.Solve(b, m)
		_ = good.Factor([]float64{1, 1, 1, 1, 1, 1}, 3, 2) // rejected
		_, e1 := good.Solve(b[:2], m)                      // rejected
		x1, e2 := good.Solve(b, m)                         // still usable, same answer
		report("state unchanged after rejection",
			errors.Is(e1, api.ErrDimMismatch) && e2 == nil && reflect.DeepEqual(x0, x1))
	}

	const N, cm, cn = 48, 200, 4
	bigA := make([]float64, cm*cn) // Vandermonde rows [1,k,k²,k³]: full rank
	for k := 0; k < cm; k++ {
		for j := 0; j < cn; j++ {
			bigA[k*cn+j] = math.Pow(float64(k+1), float64(j))
		}
	}
	sol := api.New()
	if e := sol.Factor(bigA, cm, cn); e != nil {
		report("concurrent Solve byte-identical", false)
	} else {
		cb := make([]float64, cm)
		for k := range cb {
			cb[k] = float64(k%5) - 2
		}
		res := make([][]float64, N)
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := range res {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				res[i], _ = sol.Solve(cb, cm)
			}(i)
		}
		close(start)
		wg.Wait()
		same := true
		for i := 1; i < N; i++ {
			same = same && reflect.DeepEqual(res[0], res[i])
		}
		report("concurrent Solve byte-identical", same)
	}

	if failed > 0 {
		os.Exit(1)
	}
}
