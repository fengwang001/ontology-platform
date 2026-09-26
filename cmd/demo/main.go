package main

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
	"os"
	"slices"
	"strings"
	"sync"

	"ontology/api"
	"ontology/elim"
	"ontology/pivot"
)

var failed bool

func check(name string, ok bool) {
	status := "OK"
	if !ok {
		status, failed = "FAIL", true
	}
	fmt.Println(status, name)
}

// eliminate 前向消元；swapB=false 即错误实现(甲)，usePivot=false 即错误实现(乙)。
func eliminate(ac, bc []float64, n int, swapB, usePivot bool, log *strings.Builder) {
	for k := 0; k < n; k++ {
		r := k
		if usePivot {
			r, _ = pivot.Pick(ac, n, k)
		}
		sw := "不换"
		if r != k {
			sw = fmt.Sprintf("换r%d↔r%d", k, r)
			for j := k; j < n; j++ {
				ac[k*n+j], ac[r*n+j] = ac[r*n+j], ac[k*n+j]
			}
			if swapB {
				bc[k], bc[r] = bc[r], bc[k]
			}
		}
		if log != nil {
			fmt.Fprintf(log, "k%d 主元r%d=%g %s; ", k, r, ac[k*n+k], sw)
		}
		for i := k + 1; i < n; i++ {
			m := ac[i*n+k] / ac[k*n+k]
			for j := k; j < n; j++ {
				ac[i*n+j] -= m * ac[k*n+j]
			}
			bc[i] -= m * bc[k]
		}
	}
}

// backSub 逆序回代；plus=true 即错误实现(丙)。
func backSub(ac, bc []float64, n int, plus bool) []float64 {
	x := make([]float64, n)
	for i := n - 1; i >= 0; i-- {
		s := bc[i]
		for j := i + 1; j < n; j++ {
			if plus {
				s += ac[i*n+j] * x[j]
			} else {
				s -= ac[i*n+j] * x[j]
			}
		}
		x[i] = s / ac[i*n+i]
	}
	return x
}

func variant(a, b []float64, n int, swapB, usePivot, plusBack bool) []float64 {
	ac, bc := append([]float64(nil), a...), append([]float64(nil), b...)
	eliminate(ac, bc, n, swapB, usePivot, nil)
	return backSub(ac, bc, n, plusBack)
}

func main() {
	a3 := []float64{0, 1, 1, 1, 0, 1, 1, 1, 0}
	b3 := []float64{5, 4, 3}
	ac, bc := append([]float64(nil), a3...), append([]float64(nil), b3...)
	var sb strings.Builder
	eliminate(ac, bc, 3, true, true, &sb)
	tr := sb.String() + fmt.Sprintf("回代 x=%v", backSub(ac, bc, 3, false))
	check("五步表 "+tr, strings.Contains(tr, "换r0↔r1") && strings.HasSuffix(tr, "x=[1 2 3]"))
	xA := variant(a3, b3, 3, false, true, false)
	check(fmt.Sprintf("(甲) 交换不同步b → 错解 x=%v", xA), xA[0] == 2 && xA[1] == 1 && xA[2] == 3)
	xB := variant(a3, b3, 3, true, false, false)
	check(fmt.Sprintf("(乙) 不选主元 → A[0][0]=0 除零, x=[%g %g %g]", xB[0], xB[1], xB[2]),
		math.IsNaN(xB[0]) && math.IsNaN(xB[1]) && math.IsNaN(xB[2]))
	xC := variant(a3, b3, 3, true, true, true)
	check(fmt.Sprintf("(丙) 回代减号写反 → x1=%g x0=%g", xC[1], xC[0]), xC[1] == 8 && xC[0] == 7)

	rng, n := rand.New(rand.NewSource(1)), 30
	a, b := make([]float64, n*n), make([]float64, n)
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			a[i*n+j] = rng.Float64()*2 - 1
		}
		a[i*n+i] += 3
		b[i] = rng.Float64()*2 - 1
	}
	aCopy, bCopy := append([]float64(nil), a...), append([]float64(nil), b...)
	sv := api.New()
	x, err := sv.Solve(a, b, n)
	maxR := 0.0
	for i := 0; i < n && err == nil; i++ {
		r := -bCopy[i]
		for j := 0; j < n; j++ {
			r += aCopy[i*n+j] * x[j]
		}
		maxR = math.Max(maxR, math.Abs(r))
	}
	check(fmt.Sprintf("残差 |Ax-b|max=%.2e ≤1e-9", maxR), err == nil && maxR <= 1e-9)
	check("输入不被修改", slices.Equal(a, aCopy) && slices.Equal(b, bCopy))

	_, e1 := sv.Solve(a[:0], b[:0], 0)
	_, e2 := sv.Solve(a, b[:2], n)
	_, e3 := sv.Solve(make([]float64, n*n), b, n)
	check("三类可判定错误互不相同", errors.Is(e1, elim.ErrEmpty) && errors.Is(e2, elim.ErrDimension) &&
		errors.Is(e3, elim.ErrSingular) && e1 != e2 && e2 != e3 && e1 != e3)
	_, err = sv.Solve(a, b, n)
	check("被拒后状态不变可继续求解", err == nil && sv.SelfCheck() == nil)
	big := 500
	ba := make([]float64, big*big)
	for i := range big {
		ba[i*big+i] = 1
	}
	_, err = sv.Solve(ba, make([]float64, big), big)
	check("大n奇异判定额外扫描恒0(白盒测试钉住), n=500 求解正常", err == nil)
	const g = 16
	results := make([][]float64, g)
	var wg sync.WaitGroup
	for i := 0; i < g; i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); results[i], _ = sv.Solve(a, b, n) }(i)
	}
	wg.Wait()
	ident := true
	for i := range results {
		ident = ident && slices.Equal(results[i], x)
	}
	check("并发求解结果逐字节一致", ident)
	if failed {
		os.Exit(1)
	}
}
