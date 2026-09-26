package main

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"sync"

	"ontology/api"
	"ontology/lufact"
	"ontology/solve"
)

func near(x, y float64) bool { return math.Abs(x-y) <= 1e-9 }

func main() {
	a := []float64{2, 1, 1, 4, 3, 3, 8, 7, 9}
	b := []float64{7, 19, 49}
	L, U, _ := lufact.Factor(a, 3)
	ok := near(U[0], 2) && near(L[3], 2) && near(L[6], 4) && near(U[4], 1) && near(L[7], 3)
	for i := 0; i < 9; i++ { // L*U == A and unit lower triangular
		s := 0.0
		for k := 0; k < 3; k++ {
			s += L[(i/3)*3+k] * U[k*3+i%3]
		}
		ok = ok && near(s, a[i])
	}
	ok = ok && L[0] == 1 && L[4] == 1 && L[8] == 1 && L[1] == 0 && L[2] == 0 && L[5] == 0 &&
		U[3] == 0 && U[6] == 0 && U[7] == 0
	status(ok, fmt.Sprintf("factor steps L=%v U=%v, LU=A, unit-L", L, U))

	y := solve.Forward(L, b, 3)
	x := solve.Backward(U, y, 3)
	jia, yi := b[0]/2.0, a[0]/a[3] // (甲) 7/2=3.5, (乙) 2/4=0.5
	p := 1
	y1plus := 0.0
	{ // (丙) sign flipped: y1 = 19 + 2*7 = 33
		yy := make([]float64, 3)
		for i := 0; i < 3; i++ {
			s := b[i]
			for j := 0; j < i; j++ {
				s += L[i*3+j] * yy[j]
			}
			yy[i] = s
		}
		y1plus = yy[1]
	}
	status(near(y[1], 5) && near(x[0], 1) && near(x[2], 3) && near(jia, 3.5) &&
		near(yi, 0.5) && near(y1plus, 33),
		fmt.Sprintf("solve y=%v x=%v; jia=%.1f yi=%.1f bing=%.0f", y, x, jia, yi, y1plus))

	g := api.New()
	_ = g.Factor(a, 3)
	reuse := true
	for m := 0; m < 10000; m++ { // one factor, 10000 different RHS
		p = (p*1103515245 + 12345) & 0x7fffffff
		bb := []float64{float64(p % 13), float64((p / 13) % 13), float64((p / 169) % 13)}
		xx, err := g.Solve(bb, 3)
		reuse = reuse && err == nil && residual(a, xx, bb)
	}
	status(reuse, "reuse: factor once, 10000 solves correct (count==1 checked in SelfCheck)")

	distinct := errors.Is(g.Factor(nil, 0), solve.ErrEmpty) &&
		errors.Is(g.Factor([]float64{1, 2, 3}, 2), solve.ErrDimension) &&
		errors.Is(g.Factor([]float64{0, 1, 1, 0}, 2), solve.ErrZeroPivot)
	_, eNot := api.New().Solve([]float64{1}, 1)
	xx, _ := g.Solve(b, 3) // state intact: old cache still solves correctly
	status(distinct && errors.Is(eNot, api.ErrNotFactored) && near(xx[0], 1) && near(xx[2], 3),
		"four distinct sentinel errors; rejected calls leave state intact")

	var wg sync.WaitGroup
	got := make([][]byte, 16)
	for i := range got { // N goroutines, same RHS, byte-identical solutions
		wg.Add(1)
		go func(i int) { defer wg.Done(); r, _ := g.Solve(b, 3); got[i] = []byte(fmt.Sprintf("%v", r)) }(i)
	}
	wg.Wait()
	same := true
	for _, r := range got {
		same = same && bytes.Equal(r, got[0])
	}
	status(same, "16 concurrent Solve calls return byte-identical results")

	status(g.SelfCheck() == nil, "SelfCheck: all four invariants hold")
}

func residual(a, x, b []float64) bool {
	for i := 0; i < 3; i++ {
		s := 0.0
		for j := 0; j < 3; j++ {
			s += a[i*3+j] * x[j]
		}
		if math.Abs(s-b[i]) > 1e-9 {
			return false
		}
	}
	return true
}

func status(ok bool, msg string) {
	if ok {
		fmt.Println("OK   " + msg)
	} else {
		fmt.Println("FAIL " + msg)
	}
}
