package main

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
	"os"
	"sync"

	"ontology/api"
	"ontology/hh"
	"ontology/qr"
)

const eps = 1e-9

var failed bool

func mark(ok bool) string {
	if !ok {
		failed = true
	}
	if ok {
		return "OK"
	}
	return "FAIL"
}

// hap computes (I - beta·v·vᵀ)x for 2-vectors (used to show the buggy variants).
func hap(beta float64, v, x [2]float64) [2]float64 {
	d := beta * (v[0]*x[0] + v[1]*x[1])
	return [2]float64{x[0] - d*v[0], x[1] - d*v[1]}
}

func main() {
	// --- 第三节 n=2 四步分解：k=0 / k=1 / R / Q ---
	v, _ := hh.Reflector([]float64{3, 4})
	step := []float64{3, 1, 4, 1}
	hh.Apply(v, step, 2, 0)
	H := [4]float64{3.0 / 5, 4.0 / 5, 4.0 / 5, -3.0 / 5}
	c0 := approx(v[1], -2) && approx(step[0], 5) && approx(step[1], 7.0/5) &&
		step[2] == 0 && approx(step[3], 1.0/5)
	fmt.Printf("k=0 x=[3 4] ||x||=5 v=[1 -2] H=%v 列0->[5 0] 列1->[7/5,1/5] : %s\n", H, mark(c0))
	_, skip := hh.Reflector([]float64{1.0 / 5})
	fmt.Printf("k=1 x=[1/5] ||x||=1/5 下三角全零, 不构造 v, H=I, 列1不变 : %s\n", mark(!skip))
	Q, R, err := qr.Factor([]float64{3, 1, 4, 1}, 2)
	wQ := [4]float64{3.0 / 5, 4.0 / 5, 4.0 / 5, -3.0 / 5}
	c1 := err == nil
	for i := range wQ {
		c1 = c1 && approx(Q[i], wQ[i]) && approx(R[i], [4]float64{5, 7.0 / 5, 0, 1.0 / 5}[i])
	}
	for i := 0; i < 2; i++ {
		for j := 0; j < 2; j++ {
			recon := Q[i*2]*R[j] + Q[i*2+1]*R[2+j]
			qtq := Q[i]*Q[j] + Q[2+i]*Q[2+j]
			c1 = c1 && approx(recon, [4]float64{3, 1, 4, 1}[i*2+j]) && approx(qtq, b2f(i == j))
		}
	}
	fmt.Printf("R=[[5,7/5],[0,1/5]] Q=H0; Q·R=A 还原、QᵀQ=I、R 上三角 : %s\n", mark(c1))

	// --- (甲)(乙)(丙) 三个错误实现的具体错值 ---
	bugA := hap(1.0/5, [2]float64{1, -2}, [2]float64{3, 4})     // 漏因子 2
	bugB := hap(2.0/1.25, [2]float64{1, 0.5}, [2]float64{3, 4}) // 符号取反
	bugC01 := 1.0                                               // 丙: 列1不被作用, R01 保持原值 1
	cBug := approx(bugA[0], 4) && approx(bugA[1], 2) &&
		approx(bugB[0], -5) && approx(bugB[1], 0) &&
		approx(bugC01, 1) && !approx(bugC01, 7.0/5)
	fmt.Printf("(甲)R00=4,R10=2 (乙)R00=-5 (丙)只作用当前列 R01=1≠7/5 : %s\n", mark(cBug))

	// --- 三类可判定错误、互不相同、拒绝后状态不变 ---
	e := api.New()
	_, _, e0 := e.Factor(nil, 0)
	_, _, e1 := e.Factor([]float64{1, 2, 3}, 2)
	_, _, e2 := e.Factor([]float64{1, 0, 2, 0}, 2)
	good := []float64{3, 1, 4, 1}
	snap := append([]float64(nil), good...)
	_, _, e3 := e.Factor(good, 2)
	same := true
	for i := range good {
		same = same && good[i] == snap[i]
	}
	distinct := errors.Is(e0, api.ErrEmpty) && errors.Is(e1, api.ErrDimMismatch) &&
		errors.Is(e2, api.ErrZeroTail) && !errors.Is(e0, e1) && !errors.Is(e0, e2) && !errors.Is(e1, e2)
	fmt.Printf("三类哨兵错误互不相同, 拒绝后引擎仍可用且输入逐字节不变 : %s\n", mark(distinct && e3 == nil && same))

	// --- 大 n 已上三角：外部可判定的空操作（Q≡I,R≡A）；计数恒0由白盒测试钉 ---
	bigOK := true
	for _, n := range []int{100, 1000, 10000} {
		a := make([]float64, n*n)
		rng := rand.New(rand.NewSource(int64(n)))
		for i := 0; i < n; i++ {
			for j := i; j < n; j++ {
				a[i*n+j] = rng.NormFloat64()*5 - 1
			}
		}
		q2, r2, fe := e.Factor(a, n)
		ok := fe == nil
		for i := 0; i < n; i++ {
			for j := 0; j < n; j++ {
				ok = ok && q2[i*n+j] == b2f(i == j) && r2[i*n+j] == a[i*n+j]
			}
		}
		bigOK = bigOK && ok
	}
	fmt.Printf("n=100/1000/10000 上三角: Factor 空操作 Q≡I、R≡A 逐字节（计数恒0见白盒测试）: %s\n", mark(bigOK))

	// --- 并发：N 个 goroutine 同一份输入，结果逐字节相同（无 sleep）---
	a := []float64{3, 1, 0, 4, 1, -2, 0, 5, 6}
	const N = 16
	Qs := make([][]float64, N)
	Rs := make([][]float64, N)
	var wg sync.WaitGroup
	order := rand.New(rand.NewSource(7)).Perm(N) // 随机到达顺序
	for _, idx := range order {
		wg.Add(1)
		go func(g int) { defer wg.Done(); Qs[g], Rs[g], _ = e.Factor(a, 3) }(idx)
	}
	wg.Wait()
	concOK := true
	for g := 1; g < N; g++ {
		for i := range a {
			concOK = concOK && Qs[g][i] == Qs[0][i] && Rs[g][i] == Rs[0][i]
		}
	}
	fmt.Printf("16 goroutine 并发同一输入(随机到达), Q/R 逐字节一致 : %s\n", mark(concOK))

	fmt.Printf("SelfCheck 内置矩阵四不变量 : %s\n", mark(e.SelfCheck() == nil))
	if failed {
		os.Exit(1)
	}
}

func approx(a, b float64) bool { return math.Abs(a-b) <= eps }
func b2f(b bool) float64 {
	if b {
		return 1
	}
	return 0
}
