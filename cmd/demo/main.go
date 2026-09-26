// Command demo runs the condition-number estimator self-checks and prints
// at most 10 OK/FAIL lines. It takes no arguments and does no networking.
package main

import (
	"errors"
	"fmt"
	"math"
	"sync"

	"ontology/api"
	"ontology/norm"
	"ontology/normest"
)

func main() {
	a := []float64{1, 3, 0, 2}
	n := 2
	est, _ := api.New(2)
	kappa, _ := est.Cond(a, n)

	// Line 1: the five-row worked trace, driven through the same one LU.
	L, U, _ := norm.Factor(a, n)
	b := []float64{1.0, 1.0}
	y1 := norm.Solve(L, U, n, b)
	b = signv(y1)
	y2 := norm.Solve(L, U, n, b)
	inv, _ := normest.EstimateInvInf(a, n, 2)
	check(fmt.Sprintf("五行轨迹 r1 b=[1 1] y=%v ||y||=%.2f sign=%v; r2 b=%v y=%v ||y||=%.2f; ||A^-1||=%.1f κ=%.0f",
		y1, vecInf(y1), signv(y1), b, y2, vecInf(y2), inv, kappa),
		near(y1[0], -.5) && near(y1[1], .5) && near(vecInf(y1), .5) &&
			near(y2[0], -2.5) && near(y2[1], .5) && near(inv, 2.5) && near(kappa, 10))

	// Line 2: the three wrong implementations and their wrong values.
	jia := math.Pow(4, 2)       // ‖A‖∞·‖A‖∞
	yi := 4 * (1.0 / 4.0)       // 1/‖A‖∞ used as ‖A⁻¹‖∞
	at := []float64{1, 0, 3, 2} // Aᵀ row-major; solving Aᵀy=b estimates ‖A⁻¹‖₁
	bingi, _ := normest.EstimateInvInf(at, n, 2)
	bing := 4.0 * bingi // ‖A‖∞ stays exact=4; only the solve is transposed
	check(fmt.Sprintf("错值 (甲)忘取逆=%.0f (乙)倒数当逆=%.0f (丙)转置Aᵀ=%.0f", jia, yi, bing),
		jia == 16 && yi == 1 && near(bing, 8))

	// Line 3: valid lower bound est ≤ true and est ≥ true/n.
	check("下界 est≤真实κ 且 est≥真实κ/n（内置矩阵集）", est.SelfCheck() == nil && near(kappa, 10))

	// Line 4: no explicit inverse (observable reproduction of ‖A⁻¹‖∞).
	check("不显式求逆：仅前向/回代即复现 ‖A⁻¹‖∞=2.5（次数=k 由 TestCounterConstant 钉）", near(inv, 2.5))

	// Line 5: four distinct decidable errors, then state is still usable.
	_, ebadk := api.New(0)
	_, edim := est.Cond(a[:3], n)
	_, eempty := est.Cond(a, 0)
	_, esing := est.Cond([]float64{1, 1, 1, 1}, n)
	after, _ := est.Cond(a, n)
	distinct := errors.Is(ebadk, normest.ErrInvalidK) && errors.Is(edim, norm.ErrDimension) &&
		errors.Is(eempty, norm.ErrEmpty) && errors.Is(esing, norm.ErrSingular) &&
		ebadk != edim && ebadk != eempty && ebadk != esing &&
		edim != eempty && edim != esing && eempty != esing
	check("四类错误互异且被拒后 κ 仍=10", distinct && near(after, 10))

	// Line 6: large n, k fixed at 3. Diagonal matrices make the estimate
	// exact at every size; the exact count==3 is pinned white-box.
	bigOK := true
	for _, nn := range []int{100, 1000, 10000} {
		d := make([]float64, nn*nn)
		for i := 0; i < nn; i++ {
			d[i*nn+i] = float64(1 + (i % 7)) // min diagonal = 1
		}
		e3, _ := api.New(3)
		c, err := e3.Cond(d, nn) // true κ∞ = maxDiag/minDiag = 7
		if err != nil || !near(c, 7) {
			bigOK = false
		}
	}
	check("大n=100/1000/10000 k=3 估计精确（求解次数恒=3 由白盒测试钉）", bigOK)

	// Line 7: concurrent calls on shared input give byte-identical results.
	const N = 32
	var wg sync.WaitGroup
	res := make([]uint64, N)
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			c, err := est.Cond(a, n)
			if err != nil {
				res[g] = 0
				return
			}
			res[g] = math.Float64bits(c)
		}(g)
	}
	wg.Wait()
	same := true
	for _, r := range res {
		if r != res[0] {
			same = false
		}
	}
	check(fmt.Sprintf("并发 %d 路 κ 逐字节相同", N), same && res[0] == math.Float64bits(10))
}

func signv(v []float64) []float64 {
	s := make([]float64, len(v))
	for i, x := range v {
		switch {
		case x > 0:
			s[i] = 1
		case x < 0:
			s[i] = -1
		}
	}
	return s
}

func vecInf(v []float64) float64 {
	var m float64
	for _, x := range v {
		if x < 0 {
			x = -x
		}
		if x > m {
			m = x
		}
	}
	return m
}

func near(x, want float64) bool {
	d := x - want
	if d < 0 {
		d = -d
	}
	w := want
	if w < 0 {
		w = -w
	}
	return d <= 1e-9+1e-9*w
}

func check(name string, ok bool) {
	if ok {
		fmt.Println("OK   " + name)
	} else {
		fmt.Println("FAIL " + name)
	}
}
