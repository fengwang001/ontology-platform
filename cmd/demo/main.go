// Command demo runs the blocked matrix multiplication self-checks.
package main

import (
	"errors"
	"fmt"
	"math"
	"sync"

	"ontology/api"
	"ontology/blk"
	"ontology/mul"
)

func report(name string, ok bool, detail string) {
	if ok {
		fmt.Printf("OK %s %s\n", name, detail)
	} else {
		fmt.Printf("FAIL %s %s\n", name, detail)
	}
}

func sameBits(x, y []float64) bool {
	if len(x) != len(y) {
		return false
	}
	for i := range x {
		if math.Float64bits(x[i]) != math.Float64bits(y[i]) {
			return false
		}
	}
	return true
}

func main() {
	// Built-in n=5 matrices: A[i][j]=i*5+j+1, B[i][j]=i-j.
	a, bm := make([]float64, 25), make([]float64, 25)
	for i := 0; i < 5; i++ {
		for j := 0; j < 5; j++ {
			a[i*5+j], bm[i*5+j] = float64(i*5+j+1), float64(i-j)
		}
	}
	good := mul.Blocked(a, bm, 5, 2)
	part := make([]float64, 0, 5)
	var s float64
	for k := 0; k < 5; k++ {
		s += a[15+k] * bm[k*5+2]
		part = append(part, s)
	}
	report("partial sums -> C[3][2]",
		sameBits(part, []float64{-32, -49, -49, -30, 10}) && good[17] == 10,
		fmt.Sprintf("%v -> %v", part, good[17]))

	// (jia) skip tail [4,5); (yi) inclusive bound doubles k=2,4; (bing) B[j][k].
	var skip, incl, trans float64
	for q := 0; q*2 < 5; q++ {
		lo, hi := q*2, (q+1)*2
		if hi <= 5 { // full blocks only: tail [4,5) dropped
			for k := lo; k < hi; k++ {
				skip += a[15+k] * bm[k*5+2]
			}
		}
		t := hi // buggy: k <= (q+1)*b, capped at n-1 for in-bounds access
		if t > 4 {
			t = 4
		}
		for k := lo; k <= t; k++ {
			incl += a[15+k] * bm[k*5+2]
		}
	}
	for k := 0; k < 5; k++ {
		trans += a[15+k] * bm[10+k]
	}
	report("wrong variants skip/<=/transpose", skip == -30 && incl == 50 && trans == -10,
		fmt.Sprintf("%v %v %v", skip, incl, trans))

	allEq := true
	for _, c := range []struct{ n, b int }{{1, 1}, {5, 2}, {6, 3}, {7, 4}, {13, 5}} {
		x, y := make([]float64, c.n*c.n), make([]float64, c.n*c.n)
		for i := range x {
			x[i] = float64(i) - float64(c.n*c.n)/2
		}
		for i := 0; i < c.n; i++ {
			for j := 0; j < c.n; j++ {
				y[i*c.n+j] = float64(i - j)
			}
		}
		allEq = allEq && sameBits(mul.Blocked(x, y, c.n, c.b), mul.Naive(x, y, c.n))
	}
	report("Blocked == Naive bitwise", allEq, "(tails, signs, zeros)")

	cover := true
	for _, c := range []struct{ n, b int }{{5, 2}, {6, 3}, {10, 3}, {17, 17}} {
		ps := blk.Parts(c.n, c.b)
		if ps[0].Start != 0 || ps[len(ps)-1].End != c.n {
			cover = false
		}
		for i := 1; i < len(ps); i++ {
			if ps[i].Start != ps[i-1].End {
				cover = false
			}
		}
		if c.n%c.b != 0 && ps[len(ps)-1].End-ps[len(ps)-1].Start != c.n%c.b {
			cover = false
		}
	}
	report("parts cover [0,n), tail=n%b", cover, "")

	bad := api.New(0)
	_, e0 := bad.Mul(nil, nil, 0)
	_, e1 := bad.Mul([]float64{1}, []float64{1}, 2)
	_, e2 := bad.Mul(make([]float64, 4), make([]float64, 4), 2)
	report("three distinct sentinel errors",
		errors.Is(e0, api.ErrEmpty) && errors.Is(e1, api.ErrDim) &&
			errors.Is(e2, api.ErrBlockSize) && e0 != e1 && e1 != e2, "")

	eng := api.New(2)
	r1, err1 := eng.Mul(a, bm, 5)
	eng.Mul(nil, nil, 0)
	eng.Mul([]float64{1}, []float64{1}, 2)
	r2, err2 := eng.Mul(a, bm, 5)
	report("rejections leave no trace", err1 == nil && err2 == nil && sameBits(r1, r2), "")
	report("tail-lookup probe stays 0 (n=100/1000/10000)", blk.SelfCheck() == nil, "")

	const G = 16
	var wg sync.WaitGroup
	start := make(chan struct{})
	res := make([][]float64, G)
	for g := 0; g < G; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			<-start
			res[g], _ = eng.Mul(a, bm, 5)
		}(g)
	}
	close(start)
	wg.Wait()
	concOK := true
	for g := 1; g < G; g++ {
		concOK = concOK && sameBits(res[0], res[g])
	}
	report("concurrent read-only Mul identical", concOK, fmt.Sprintf("(%d goroutines)", G))
}
