package main

import (
	"errors"
	"fmt"
	"math"
	"os"
	"sync"

	"ontology/api"
	"ontology/gram"
	"ontology/lsq"
)

var failed bool

func report(name string, cond bool) {
	if cond {
		fmt.Println("OK  " + name)
	} else {
		fmt.Println("FAIL " + name)
		failed = true
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

func bitsEq(x, y []float64) bool {
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
	a := []float64{1, 1, 1, 2, 1, 3} // A=[[1,1],[1,2],[1,3]]
	b := []float64{2, 3, 5}
	m, n := 3, 2
	g := gram.Gram(a, m, n)
	c := gram.AtB(a, b, m, n)
	report("① Gram=[[3,6],[6,14]] ② AtB=[10,23]",
		eq(g, []float64{3, 6, 6, 14}, 1e-12) && eq(c, []float64{10, 23}, 1e-12))
	u11 := g[3] - g[2]/g[0]*g[1]
	d1 := c[1] - g[2]/g[0]*c[0]
	x := lsq.SolveG(g, c, n)
	report("③ 消元 U=[[3,6],[0,2]],d=[10,3] ④ 回代 x=[1/3,3/2]",
		math.Abs(u11-2) < 1e-12 && math.Abs(d1-3) < 1e-12 && eq(x, []float64{1.0 / 3, 1.5}, 1e-12))
	r := make([]float64, m)
	for k := 0; k < m; k++ {
		r[k] = b[k] - (a[k*n]*x[0] + a[k*n+1]*x[1])
	}
	report("⑤ 残差 b−Ax=[1/6,-1/3,1/6]", eq(r, []float64{1.0 / 6, -1.0 / 3, 1.0 / 6}, 1e-12))
	xA := lsq.SolveG([]float64{3, 6, 0, 14}, c, n)
	report("(甲)[1/21,23/14] (乙)AtB0=5 (丙)x0=10/3",
		eq(xA, []float64{1.0 / 21, 23.0 / 14}, 1e-12) &&
			math.Abs(a[0]*b[0]+a[2]*b[1]-5) < 1e-12 && math.Abs(c[0]/g[0]-10.0/3) < 1e-12)

	atr0 := a[0]*r[0] + a[2]*r[1] + a[4]*r[2]
	atr1 := a[1]*r[0] + a[3]*r[1] + a[5]*r[2]
	report("残差最小 Aᵀ(Ax−b)==0（逐项≤1e-9）", math.Abs(atr0) <= 1e-9 && math.Abs(atr1) <= 1e-9)
	report("Gram 对称 G[i][j]==G[j][i] 逐位相等", g[1] == g[2])

	sc := api.New().SelfCheck() // 白盒内含「计数恒 1」断言，不暴露计数数值
	report("多次Solve不重算Gram、大m(10000)计数恒1（SelfCheck）", sc == nil)

	s := api.New()
	if err := s.Factor(a, m, n); err != nil {
		panic(err)
	}
	bad := []error{s.Factor([]float64{1}, 3, 2), s.Factor([]float64{1, 2}, 1, 2),
		s.Factor(nil, 0, 0), s.Factor([]float64{1, 1, 2, 2, 3, 3}, 3, 2)}
	distinct := bad[0] != bad[1] && bad[0] != bad[2] && bad[0] != bad[3] &&
		bad[1] != bad[2] && bad[1] != bad[3] && bad[2] != bad[3]
	report("四类哨兵错误可判定且互不相同",
		errors.Is(bad[0], api.ErrDimMismatch) && errors.Is(bad[1], api.ErrUnderdetermined) &&
			errors.Is(bad[2], api.ErrEmpty) && errors.Is(bad[3], api.ErrRankDeficient) && distinct)
	xs, _ := s.Solve(b, m)
	fresh := api.New()
	fErr := fresh.Factor([]float64{1, 1, 2, 2, 3, 3}, 3, 2)
	_, nf := fresh.Solve(b, m)
	report("被拒后状态不变且仍可继续使用", bitsEq(xs, x) && errors.Is(fErr, api.ErrRankDeficient) &&
		errors.Is(nf, api.ErrNotFactored))

	const N = 32
	res := make([][]float64, N)
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(idx int) { defer wg.Done(); res[idx], _ = s.Solve(b, m) }(i)
	}
	wg.Wait()
	same := true
	for i := 1; i < N; i++ {
		same = same && bitsEq(res[0], res[i])
	}
	report("并发Solve同一右端，结果逐字节一致", same && sc == nil)

	if failed {
		os.Exit(1)
	}
}
