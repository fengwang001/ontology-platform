package main

import (
	"errors"
	"fmt"
	"math"
	"os"
	"slices"
	"sync"

	"ontology/api"
	"ontology/eigen"
	"ontology/power"
)

const eps = 1e-12

func aeq(x, y, tol float64) bool { return math.Abs(x-y) < tol }

func bits(x []float64) []uint64 {
	b := make([]uint64, len(x))
	for i, f := range x {
		b[i] = math.Float64bits(f)
	}
	return b
}

func maxResid(a, v []float64, n int, lam float64) float64 {
	w, r := power.MatVec(a, v, n), 0.0
	for i := range w {
		if d := math.Abs(w[i] - lam*v[i]); d > r {
			r = d
		}
	}
	return r
}

func main() {
	fail := false
	check := func(ok bool, f string, args ...any) {
		if !ok {
			fail = true
		}
		fmt.Printf("%s %s\n", map[bool]string{true: "OK", false: "FAIL"}[ok], fmt.Sprintf(f, args...))
	}

	a := []float64{3, 1, 1, 3}
	v := []float64{1.0, 0}
	wantW := [][]float64{{3, 1}, {10.0 / 3, 2}, {18.0 / 5, 14.0 / 5}, {34.0 / 9, 10.0 / 3}}
	wantM := []float64{3, 10.0 / 3, 18.0 / 5, 34.0 / 9}
	wantV := [][]float64{{1, 1.0 / 3}, {1, 3.0 / 5}, {1, 7.0 / 9}, {1, 15.0 / 17}}
	wantL := []float64{3.6, 66.0 / 17, 258.0 / 65, 1026.0 / 257}
	for k := 0; k < 4; k++ { // 第三节四步表，全部由 power 包原语驱动
		w := power.MatVec(a, v, 2)
		m := power.InfNorm(w)
		nv := []float64{w[0] / m, w[1] / m}
		lam := power.Rayleigh(a, nv, 2)
		good := aeq(m, wantM[k], eps) && aeq(lam, wantL[k], eps)
		for i := 0; i < 2; i++ {
			good = good && aeq(w[i], wantW[k][i], eps) && aeq(nv[i], wantV[k][i], eps)
		}
		check(good, "step%d w=[%.4f,%.4f] m=%.4f v=[%.4f,%.4f] λ=%.5f", k+1, w[0], w[1], m, nv[0], nv[1], lam)
		v = nv
	}
	v1 := []float64{1.0, 1.0 / 3}
	av1 := power.MatVec(a, v1, 2)
	num, den := v1[0]*av1[0]+v1[1]*av1[1], v1[0]*v1[0]+v1[1]*v1[1] // 4, 10/9
	check(aeq(num, 4, eps) && aeq(den/num, 5.0/18, eps) &&
		aeq(power.InfNorm(power.MatVec(a, []float64{1, 0}, 2)), 3, eps),
		"mutations 甲=%.4f 乙=%.5f 丙=%.4f", num, den/num, 3.0)

	eng, _ := api.New(1e-13, 100000)
	rOK, infOK := true, true
	for _, ea := range [][]float64{{1, 1, 1, 1}, {-2, 2, 2, -2}} { // 一步精确收敛，残差严格 0
		l, vv, err := eng.Eigen(ea, []float64{1, 0}, 2)
		rOK = rOK && err == nil && maxResid(ea, vv, 2, l) <= 1e-9
		infOK = infOK && aeq(power.InfNorm(vv), 1, eps)
	}
	l3, _, _ := eng.Eigen(a, []float64{1, 0}, 2)
	check(rOK && infOK && aeq(l3, 4, 1e-9), "resid<=1e-9 infnorm=1 |4-λ|=%.2e", math.Abs(4-l3))

	ba, vArg := bits(a), []float64{1.0, 0}
	bv := bits(vArg)
	eng.Eigen(a, vArg, 2)
	check(slices.Equal(bits(a), ba) && slices.Equal(bits(vArg), bv), "inputs byte-unchanged after Eigen")

	id := []float64{1, 0, 0, 1}
	_, _, e0 := eng.Eigen(make([]float64, 3), []float64{1, 0}, 2)
	_, _, e1 := eng.Eigen(id, nil, 0)
	_, _, e2 := eng.Eigen(id, []float64{0, 0}, 2)
	strict, _ := api.New(1e-30, 1)
	_, _, e3 := strict.Eigen(a, []float64{1, 0}, 2)
	want := []error{api.ErrDimensionMismatch, api.ErrEmptySystem, api.ErrZeroStartVector, api.ErrNotConverged}
	dOK := len(map[error]bool{e0: true, e1: true, e2: true, e3: true}) == 4
	for i, e := range []error{e0, e1, e2, e3} {
		dOK = dOK && errors.Is(e, want[i])
	}
	_, _, stillErr := eng.Eigen([]float64{1, 1, 1, 1}, []float64{1, 0}, 2)
	check(dOK && stillErr == nil && eng.SelfCheck() == nil, "4 distinct errors; reusable; SelfCheck")

	best, sOK := 0, true
	for _, nn := range []int{100, 1000, 10000} { // diag(3,1,0,..)：固定间隙，轮数与 n 无关
		d := make([]float64, nn*nn)
		d[0], d[nn+1] = 3, 1
		v0 := make([]float64, nn)
		for i := range v0 {
			v0[i] = 1
		}
		l, vv, it, e := eigen.Iterate(d, v0, nn, 1e-9, 100000)
		best, sOK = it, sOK && e == nil && aeq(l, 3, 1e-9) && it <= 20 && power.InfNorm(vv) == 1
	}
	check(sOK, "iters(n=100..10000)<=20, n=10000: %d", best)

	const N = 64
	var wg sync.WaitGroup
	lams := make([]uint64, N)
	vecs := make([][]float64, N)
	wg.Add(N)
	for g := 0; g < N; g++ { // 并发读同一份输入，不用 sleep 制造时序
		go func(g int) {
			defer wg.Done()
			l, vv, _ := eng.Eigen(a, []float64{1, 0}, 2)
			lams[g], vecs[g] = math.Float64bits(l), vv
		}(g)
	}
	wg.Wait()
	cOK := true
	for g := 1; g < N; g++ { // 各自结果逐字节相同
		cOK = cOK && lams[0] == lams[g] && slices.Equal(bits(vecs[0]), bits(vecs[g]))
	}
	check(cOK, "%d goroutines byte-identical results", N)

	if fail {
		os.Exit(1)
	}
}
