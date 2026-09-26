package main

import (
	"errors"
	"fmt"
	"math"
	"os"
	"reflect"
	"sync"

	"ontology/api"
	"ontology/lufact"
	"ontology/solve"
)

var fails int

func mark(ok bool) string {
	if !ok {
		fails++
	}
	return map[bool]string{true: "OK", false: "FAIL"}[ok]
}

func approxEq(p, q []float64, tol float64) bool {
	if len(p) != len(q) {
		return false
	}
	for i := range p {
		if d := p[i] - q[i]; d > tol || d < -tol {
			return false
		}
	}
	return true
}

func resid(a, x, b []float64, n int) (m float64) {
	for i := 0; i < n; i++ {
		s := 0.0
		for j := 0; j < n; j++ {
			s += a[i*n+j] * x[j]
		}
		if d := math.Abs(s - b[i]); d > m {
			m = d
		}
	}
	return
}

func main() {
	n := 3
	a := []float64{2, 1, 1, 4, 3, 3, 8, 7, 9}
	b := []float64{7, 19, 49}
	L, U, ferr := lufact.Factor(a, n)
	if ferr != nil {
		fmt.Println("FAIL factor:", ferr)
		os.Exit(1)
	}
	steps := ""
	for k := 0; k < n-1; k++ {
		steps += fmt.Sprintf("k=%d:pivot=%.0f,mult=[", k, U[k*n+k])
		for i := k + 1; i < n; i++ {
			if i > k+1 {
				steps += " "
			}
			steps += fmt.Sprintf("%.0f", L[i*n+k])
		}
		steps += "] "
	}
	fmt.Printf("%s steps %s L=%s U=%s\n",
		mark(steps == "k=0:pivot=2,mult=[2 4] k=1:pivot=1,mult=[3] "), steps, fmt.Sprint(L), fmt.Sprint(U))

	y := solve.Forward(L, b, n)
	x := solve.Backward(U, y, n)
	bugY0, bugMult, bugY1 := b[0]/U[0], a[0]/a[3], b[1]+L[1*n]*y[0] // 甲/乙/丙
	fmt.Printf("%s y=%s x=%s | 甲y0=%.1f 乙L10=%.1f 丙y1=%.0f\n",
		mark(approxEq(y, []float64{7, 5, 6}, 0) && approxEq(x, []float64{1, 2, 3}, 0) &&
			bugY0 == 3.5 && bugMult == 0.5 && bugY1 == 33), fmt.Sprint(y), fmt.Sprint(x), bugY0, bugMult, bugY1)

	prod := make([]float64, n*n)
	shape := true
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			sum := 0.0
			for t := 0; t < n; t++ {
				sum += L[i*n+t] * U[t*n+j]
			}
			prod[i*n+j] = sum
			if (i == j && L[i*n+j] != 1) || (i < j && L[i*n+j] != 0) || (i > j && U[i*n+j] != 0) {
				shape = false
			}
		}
	}
	fmt.Printf("%s L*U==A (|err|<=1e-9) and unit-lower/upper shape\n", mark(approxEq(prod, a, 1e-9) && shape))

	e := api.New()
	if err := e.Factor(a, n); err != nil {
		fmt.Println("FAIL engine factor:", err)
		os.Exit(1)
	}
	multiOK := true
	for r := 0; r < 50; r++ { // factor once, solve many distinct RHS
		rhs := []float64{b[0] + float64(r)*0.11, b[1] - float64(r)*0.07, b[2] + float64(r)*0.03}
		xr, err := e.Solve(rhs, n)
		if err != nil || resid(a, xr, rhs, n) > 1e-9 {
			multiOK = false
		}
	}
	x3, _ := e.Solve(b, n)
	fmt.Printf("%s factor-once/solve-many correct; x=%s\n",
		mark(multiOK && approxEq(x3, []float64{1, 2, 3}, 1e-9)), fmt.Sprint(x3))

	h := api.New()
	_, e1 := h.Solve([]float64{1}, 1)
	errs := []error{h.Factor(nil, 0), h.Factor([]float64{1, 2, 3}, 2),
		h.Factor([]float64{1, 2, 3, 2, 4, 6, 7, 8, 9}, 3), e1}
	wants := []error{api.ErrEmpty, api.ErrDimension, api.ErrZeroPivot, api.ErrNotFactored}
	distinctOK := errors.Is(errs[0], wants[0]) && errors.Is(errs[1], wants[1]) &&
		errors.Is(errs[2], wants[2]) && errors.Is(errs[3], wants[3]) &&
		!errors.Is(errs[0], wants[1]) && !errors.Is(errs[1], wants[0]) &&
		!errors.Is(errs[2], wants[0]) && !errors.Is(errs[3], wants[0])
	fmt.Printf("%s four distinct decidable errors (empty/dim/zero-pivot/not-factored)\n", mark(distinctOK))

	// Rejected calls left h empty: still not-factored, then it works normally.
	_, nf := h.Solve(b, n)
	usable := errors.Is(nf, api.ErrNotFactored) && h.Factor(a, n) == nil
	xh, err := h.Solve(b, n)
	fmt.Printf("%s state unchanged after rejection; engine still usable x=%s\n",
		mark(usable && err == nil && approxEq(xh, []float64{1, 2, 3}, 1e-9)), fmt.Sprint(xh))

	fmt.Printf("%s SelfCheck incl. large-m (10000 solves) factor count stays 1\n", mark(api.New().SelfCheck() == nil))
	const G = 32
	res := make([][]float64, G)
	var wg sync.WaitGroup
	for g := 0; g < G; g++ {
		wg.Add(1)
		go func(g int) { defer wg.Done(); res[g], _ = e.Solve(b, n) }(g)
	}
	wg.Wait()
	same := true
	for g := 1; g < G; g++ {
		if !reflect.DeepEqual(res[g], res[0]) {
			same = false
		}
	}
	fmt.Printf("%s %d concurrent solves of one RHS are byte-identical\n", mark(same), G)
	if fails > 0 {
		os.Exit(1)
	}
}
