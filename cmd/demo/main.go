package main

import (
	"errors"
	"fmt"
	"math"
	"os"
	"slices"
	"sync"

	"ontology/api"
	"ontology/pivot"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
	}
	fmt.Printf("%s: %s\n", name, map[bool]string{true: "OK", false: "FAIL"}[ok])
}

// elim 对增广对 [L|R] 做 Gauss-Jordan 消元。pivoting/up/syncR 开关用于复现 NOTES.md 的 (甲)(乙)(丙)。
func elim(L, R []float64, n int, pivoting, up, syncR bool, rec *[][]float64) {
	snap := func() {
		if rec != nil {
			*rec = append(*rec, append(append([]float64{}, L...), R...))
		}
	}
	rowop := func(i, k int, f float64) { // 行变换须同时作用左右半
		for c := 0; c < n; c++ {
			L[i*n+c] -= f * L[k*n+c]
			if syncR {
				R[i*n+c] -= f * R[k*n+c]
			}
		}
	}
	for k := 0; k < n; k++ {
		if pivoting {
			if p, ok := pivot.Pick(L, n, k); ok && p != k {
				for c := 0; c < n; c++ {
					L[k*n+c], L[p*n+c] = L[p*n+c], L[k*n+c]
					if syncR {
						R[k*n+c], R[p*n+c] = R[p*n+c], R[k*n+c]
					}
				}
			}
		}
		d := L[k*n+k]
		for c := 0; c < n; c++ {
			L[k*n+c] /= d
			if syncR {
				R[k*n+c] /= d
			}
		}
		snap()
		for i := k + 1; i < n; i++ { // 先下消元
			if f := L[i*n+k]; f != 0 {
				rowop(i, k, f)
			}
		}
		for i := 0; i < k && up; i++ { // 后上消元
			if f := L[i*n+k]; f != 0 {
				rowop(i, k, f)
			}
		}
		snap()
	}
}

func main() {
	h := api.New()
	// 第三节五行分步表：A=[[0,2],[1,1]]，核验每步主元、交换与增广矩阵变化。
	L, R := []float64{0, 2, 1, 1}, []float64{1, 0, 0, 1}
	rec := [][]float64{append(append([]float64{}, L...), R...)}
	elim(L, R, 2, true, true, true, &rec)
	want := [][]float64{
		{0, 2, 1, 1, 1, 0, 0, 1},      // 初始
		{1, 1, 0, 2, 0, 1, 1, 0},      // k=0 交换+归一(÷1)
		{1, 1, 0, 2, 0, 1, 1, 0},      // k=0 下/上消元（无操作）
		{1, 1, 0, 1, 0, 1, 0.5, 0},    // k=1 归一 r1/2
		{1, 0, 0, 1, -0.5, 1, 0.5, 0}, // k=1 上消元 r0-=r1
	}
	traceOK := len(rec) == len(want) // 状态序列本身即主元与交换的证明（s1 已换行）
	for i := range want {
		traceOK = traceOK && slices.Equal(rec[i], want[i])
	}
	check("5-step trace pivot/swap/aug states", traceOK)

	fresh := func() ([]float64, []float64) { return []float64{0, 2, 1, 1}, []float64{1, 0, 0, 1} }
	La, Ra := fresh()
	elim(La, Ra, 2, true, true, false, nil) // (甲) 行变换不同步右半
	check("(甲) unsynced right half => inv[0][1]=0 (want 1)", slices.Equal(Ra, []float64{1, 0, 0, 1}))
	Lb, Rb := fresh()
	elim(Lb, Rb, 2, false, true, true, nil) // (乙) 不带主元，主元为 0
	nan := slices.ContainsFunc(Rb, func(v float64) bool { return math.IsNaN(v) || math.IsInf(v, 0) })
	check("(乙) no pivot => NaN/Inf, not ErrSingular", nan)
	Lc, Rc := fresh()
	elim(Lc, Rc, 2, true, false, true, nil) // (丙) 漏做上消元
	check("(丙) no up-elim => inv[0][0]=0 (want -0.5)", slices.Equal(Rc, []float64{0, 1, 0.5, 0}))
	inv2, err2 := h.Inv([]float64{0, 2, 1, 1}, 2) // 精确逆即 A·A⁻¹==I 的核验
	check("A*inv(A)==I (exact 2x2 inverse)", err2 == nil && slices.Equal(inv2, []float64{-0.5, 1, 0.5, 0}))
	in := []float64{0, 2, 1, 1}
	before := slices.Clone(in)
	invA, _ := h.Inv(in, 2)
	_, e1 := h.Inv(make([]float64, 3), 2)
	_, e2 := h.Inv(nil, 0)
	_, e3 := h.Inv([]float64{1, 2, 2, 4}, 2)
	distinct := errors.Is(e1, api.ErrDim) && errors.Is(e2, api.ErrEmpty) && errors.Is(e3, api.ErrSingular)
	invB, errB := h.Inv(in, 2) // 被拒后仍正常、结果不变
	check("input unmodified + 3 distinct errors + no trace after reject",
		slices.Equal(in, before) && distinct && errB == nil && slices.Equal(invA, invB))

	diagOK := true
	for _, n := range []int{100, 1000, 10000} { // 对角矩阵只做逐元素倒数
		d, w := make([]float64, n*n), make([]float64, n*n)
		for i := 0; i < n; i++ {
			d[i*n+i] = float64(i) + 2
			w[i*n+i] = 1 / (float64(i) + 2)
		}
		inv, err := h.Inv(d, n)
		diagOK = diagOK && err == nil && slices.Equal(inv, w)
	}
	check("diagonal n=100/1000/10000 exact reciprocal (mulsub==0: TestDiagZeroMulSub)", diagOK)

	shared := make([]float64, 30*30)
	for i := range shared {
		shared[i] = float64((i*7)%13) - 6
		if i/30 == i%30 {
			shared[i] += 30
		}
	}
	results := make([][]float64, 32)
	var wg sync.WaitGroup
	for w := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[w], _ = h.Inv(shared, 30)
		}()
	}
	wg.Wait()
	concOK := !slices.ContainsFunc(results[1:], func(r []float64) bool { return !slices.Equal(r, results[0]) })
	check("32 goroutines concurrent Inv byte-identical", concOK)
	check("api.SelfCheck (4 invariants)", h.SelfCheck() == nil)
	if failed {
		os.Exit(1)
	}
}
