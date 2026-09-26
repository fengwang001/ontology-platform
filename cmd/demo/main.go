package main

import (
	"fmt"
	"math"
	"math/rand"
	"ontology/api"
	"ontology/elim"
	"ontology/pivot"
	"os"
	"slices"
	"sync"
)

// gauss carries the NOTES section-3 bug switches and reports the trace.
func gauss(a, b []float64, n int, swapB, usePivot, plusBack bool) (x, pivs []float64, rows, swaps []int) {
	u, y := append([]float64(nil), a...), append([]float64(nil), b...)
	rows, swaps, pivs = []int{}, []int{}, []float64{}
	for k := 0; k < n; k++ {
		if usePivot {
			p, _ := pivot.Pick(u, n, k)
			rows = append(rows, p)
			if p != k {
				swaps = append(swaps, k, p)
				for j := 0; j < n; j++ {
					u[k*n+j], u[p*n+j] = u[p*n+j], u[k*n+j]
				}
				if swapB {
					y[k], y[p] = y[p], y[k]
				}
			}
			pivs = append(pivs, u[k*n+k])
		}
		for i := k + 1; i < n; i++ {
			m := u[i*n+k] / u[k*n+k] // x/0 = Inf, no panic
			for j := k; j < n; j++ {
				u[i*n+j] -= m * u[k*n+j]
			}
			y[i] -= m * y[k]
		}
	}
	x = make([]float64, n)
	for i := n - 1; i >= 0; i-- {
		s := y[i]
		for j := i + 1; j < n; j++ {
			s += u[i*n+j] * x[j]
		}
		if !plusBack {
			s = 2*y[i] - s // y-sum == 2*y-(y+sum)
		}
		x[i] = s / u[i*n+i]
	}
	return x, pivs, rows, swaps
}
func dominant(r *rand.Rand, n int) []float64 {
	a := make([]float64, n*n)
	for i := 0; i < n; i++ {
		sum := 1.0
		for j := 0; j < n; j++ {
			v := r.Float64()*2 - 1
			a[i*n+j], sum = v, sum+math.Abs(v)
		}
		a[i*n+i] = sum + r.Float64()
	}
	return a
}
func resid(s *api.API, a, b []float64, n int) bool {
	x, err := s.Solve(a, b, n)
	if err != nil {
		return false
	}
	for i := 0; i < n; i++ {
		d := -b[i]
		for j := 0; j < n; j++ {
			d += a[i*n+j] * x[j]
		}
		if math.Abs(d) > 1e-9 {
			return false
		}
	}
	return true
}
func main() {
	s, fails := api.New(), 0
	check := func(name string, ok bool) {
		if !ok {
			fmt.Println("FAIL " + name)
			fails++
		} else {
			fmt.Println("OK  " + name)
		}
	}
	a0 := []float64{0, 1, 1, 1, 0, 1, 1, 1, 0}
	b0 := []float64{5, 4, 3}
	_, pivs, rows, swaps := gauss(a0, b0, 3, true, true, false)
	x0, _ := s.Solve(a0, b0, 3)
	check(fmt.Sprintf("trace rows=%v swap=%v pivs=%v x=%v", rows, swaps, pivs, x0), slices.Equal(rows, []int{1, 1, 2}) && slices.Equal(swaps, []int{0, 1}) && slices.Equal(pivs, []float64{1, 1, -2}) && slices.Equal(x0, []float64{1, 2, 3}))
	jia, _, _, _ := gauss(a0, b0, 3, false, true, false)
	yi, _, _, _ := gauss(a0, b0, 3, true, false, false)
	bing, _, _, _ := gauss(a0, b0, 3, true, true, true)
	nan3 := math.IsNaN(yi[0]) && math.IsNaN(yi[1]) && math.IsNaN(yi[2])
	check(fmt.Sprintf("(甲)=%v (乙)allNaN=%v (丙)x0,x1=%v,%v", jia, nan3, bing[0], bing[1]), slices.Equal(jia, []float64{2, 1, 3}) && nan3 && bing[1] == 8 && bing[0] == 7)
	r, resOK := rand.New(rand.NewSource(1)), resid(s, a0, b0, 3)
	for _, n := range []int{1, 2, 3, 7, 20} {
		b := make([]float64, n)
		for i := range b {
			b[i] = r.Float64()*4 - 2
		}
		resOK = resOK && resid(s, dominant(r, n), b, n)
	}
	check("residual |A*x-b| <= 1e-9", resOK)
	ca, cb := append([]float64(nil), a0...), append([]float64(nil), b0...)
	_, _ = s.Solve(a0, b0, 3)
	_, _ = elim.Solve(a0, []float64{1, 2}, 3)
	check("inputs byte-identical after Solve", slices.Equal(a0, ca) && slices.Equal(b0, cb))
	_, e1 := s.Solve(nil, nil, 0)
	_, e2 := s.Solve([]float64{1, 2}, []float64{1}, 1)
	_, e3 := s.Solve([]float64{1, 0, 0, 0}, []float64{1, 2}, 2)
	again, _ := s.Solve(a0, b0, 3)
	check("3 distinct errors; no trace after reject; SelfCheck nil", e1 == elim.ErrEmpty && e2 == elim.ErrDimension && e3 == elim.ErrSingular && e1 != e2 && e2 != e3 && slices.Equal(again, []float64{1, 2, 3}) && s.SelfCheck() == nil)
	bigOK := true
	for _, n := range []int{100, 1000, 10000} {
		a := dominant(rand.New(rand.NewSource(int64(n))), n)
		pivot.ResetExtraScans()
		for k := 0; k < n; k++ {
			if _, ok := pivot.Pick(a, n, k); !ok {
				bigOK = false
			}
		}
		bigOK = bigOK && pivot.ExtraScansZero()
	}
	check("extra singular-scan 0 n=100/1000/10000 (real solve@1000 in tests)", bigOK)
	const N = 32
	var wg sync.WaitGroup
	res := make([][]float64, N)
	wg.Add(N)
	for g := 0; g < N; g++ {
		go func(g int) { defer wg.Done(); res[g], _ = s.Solve(a0, b0, 3) }(g)
	}
	wg.Wait()
	concOK := true
	for g := 1; g < N; g++ {
		concOK = concOK && slices.Equal(res[0], res[g])
	}
	check("concurrent solves byte-identical", concOK && slices.Equal(a0, ca))
	if fails > 0 {
		fmt.Printf("demo: %d check(s) failed\n", fails)
		os.Exit(1)
	}
}
