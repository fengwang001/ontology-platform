// Command demo prints one OK/FAIL line per required check. Exit code 0 only
// if every line is OK.
package main

import (
	"fmt"
	"math"
	"os"
	"sync"

	"ontology/api"
	"ontology/hh"
	"ontology/qr"
)

var failed bool

func ok(format string, args ...any)  { fmt.Printf("OK   "+format+"\n", args...) }
func bad(format string, args ...any) { failed = true; fmt.Printf("FAIL "+format+"\n", args...) }

func near(a, b float64) bool { return math.Abs(a-b) <= 1e-9 }

func main() {
	a2 := []float64{3, 1, 4, 1}

	// Line 1: step k=0 — v, H, both columns after application.
	v, isRef := hh.Reflector([]float64{3, 4})
	if !isRef || !near(v[0], 1) || !near(v[1], -2) {
		bad("k=0 reflector v=%v", v)
	} else {
		ok("k=0: v=[1 -2], H=[[3/5 4/5][4/5 -3/5]], col0->[5 0], col1->[7/5 1/5]")
	}

	// Line 2: step k=1 (skipped) and final R, Q.
	Q, R, err := api.New().Factor(a2, 2)
	if err != nil || !near(R[0], 5) || !near(R[1], 1.4) || !near(R[2], 0) || !near(R[3], 0.2) ||
		!near(Q[0], 0.6) || !near(Q[1], 0.8) || !near(Q[2], 0.8) || !near(Q[3], -0.6) {
		bad("k=1/final: R=%v Q=%v err=%v", R, Q, err)
	} else {
		ok("k=1: x[1:] empty, skipped; R=[[5 7/5][0 1/5]], Q=[[3/5 4/5][4/5 -3/5]]")
	}

	// Line 3: (甲)(乙)(丙) wrong values, each computed by the flawed variant.
	jia := hTimes2([]float64{1, -2}, []float64{3, 4}, 1) // missing factor 2
	yi := hTimes2([]float64{1, 0.5}, []float64{3, 4}, 2) // flipped sign: v=[1,1/2]
	bing := a2[1]                                        // col1 never touched
	if !near(jia[0], 4) || !near(jia[1], 2) || !near(yi[0], -5) || !near(bing, 1) {
		bad("甲乙丙: jia=%v yi=%v bing=%v", jia, yi, bing)
	} else {
		ok("甲: R[0][0]=4,R[1][0]=2; 乙: R[0][0]=-5; 丙: R[0][1]=1")
	}

	// Line 4: invariants 1-4 over built-in matrices (Q*R==A, Q^T Q==I, R
	// upper, earlier columns untouched, rejections leave state usable).
	if err := api.New().SelfCheck(); err != nil {
		bad("api.SelfCheck: %v", err)
	} else {
		ok("Q*R==A, Q^T Q==I, R upper, cols untouched, rejections clean")
	}

	// Line 5: reflector count stays 0 for triangular n=100/1000/10000.
	if err := qr.SelfCheck(); err != nil {
		bad("qr.SelfCheck: %v", err)
	} else {
		ok("triangular n=100/1000/10000: reflectors built == 0")
	}

	// Line 6: concurrent Factor on shared input is byte-identical.
	const g = 16
	in := []float64{2, -1, 0, 3, 1, 0, 1, -2, 0, 4, 2, 1, -3, 1, 2, 1}
	qs, rs := make([][]float64, g), make([][]float64, g)
	var wg sync.WaitGroup
	for i := 0; i < g; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			qs[i], rs[i], _ = api.New().Factor(in, 4)
		}(i)
	}
	wg.Wait()
	same := true
	for i := 1; i < g && same; i++ {
		for j := range qs[0] {
			if math.Float64bits(qs[i][j]) != math.Float64bits(qs[0][j]) ||
				math.Float64bits(rs[i][j]) != math.Float64bits(rs[0][j]) {
				same = false
				break
			}
		}
	}
	if !same {
		bad("concurrent Factor results differ")
	} else {
		ok("16 goroutines x shared input: Q,R byte-identical")
	}

	if failed {
		os.Exit(1)
	}
}

// hTimes2 applies H = I - c*v*v^T/(v^T v) to a 2-vector.
func hTimes2(v, x []float64, c float64) []float64 {
	vv := v[0]*v[0] + v[1]*v[1]
	d := (v[0]*x[0] + v[1]*x[1]) * c / vv
	return []float64{x[0] - d*v[0], x[1] - d*v[1]}
}
