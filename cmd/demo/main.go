// Command demo 校验并打印幂迭代求最大特征值的各条性质。
// 不读参数、不联网；任一条失败即以非零码退出，输出不超过 10 行。
package main

import (
	"errors"
	"fmt"
	"math"
	"os"
	"slices"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/eigen"
	"ontology/power"
)

var failed bool

func ok(name string, cond bool, detail string) {
	if !cond {
		failed = true
	}
	fmt.Printf("%s %s %s\n", map[bool]string{true: "OK", false: "FAIL"}[cond], name, detail)
}

func main() {
	a := []float64{3, 1, 1, 3}

	// power 包：单步与商。
	w := power.MatVec(a, []float64{1, 0}, 2)
	ok("power", w[0] == 3 && w[1] == 1 && power.InfNorm([]float64{3, -5}) == 5 &&
		near(power.Rayleigh(a, []float64{1, 1.0 / 3}, 2), 3.6), "MatVec w=[3 1], InfNorm=5, Rayleigh λ1=3.6")

	// eigen 包：第三节四轮分步表，每步 w、归一化因子、v'、λ。
	wantLam := []float64{3.6, 66.0 / 17, 258.0 / 65, 1026.0 / 257}
	v := []float64{1.0, 0}
	tableOK, detail := true, ""
	for k := 0; k < 4; k++ {
		w := power.MatVec(a, v, 2)
		m := w[0]
		if math.Abs(w[1]) > math.Abs(m) {
			m = w[1]
		}
		nv := []float64{w[0] / m, w[1] / m}
		lam := rq(a, nv)
		tableOK = tableOK && near(lam, wantLam[k]) && nv[0] == 1
		detail += fmt.Sprintf("#%d w=%v m=%.4f v'=%v λ=%.6f; ", k+1, w, m, nv, lam)
		v = nv
	}
	ok("eigen 4-step table", tableOK, detail)

	// (甲)(乙)(丙)：第 1 步三个错误实现的错值。正确 λ1=3.6。
	nv1 := []float64{1.0, 1.0 / 3}
	num, den := rqParts(a, nv1)
	ok("eigen wrong variants", near(num, 4) && near(den/num, 5.0/18),
		fmt.Sprintf("(甲)忘除vᵀv=%.4f (乙)颠倒=%.6f (丙)取max|w|=3", num, den/num))

	// eigen 结果：残差 ≤1e-9、归一化、输入不被修改、确定性。
	ac, vc := slices.Clone(a), []float64{1.0, 0}
	v0c := slices.Clone(vc)
	lam, ev, _, err := eigen.Iterate(a, vc, 2, 1e-9, 100)
	aw := power.MatVec(a, ev, 2)
	resOK := err == nil && near(lam, 4) && ev[0] == 1 &&
		math.Abs(aw[0]-lam*ev[0]) <= 1e-9 && math.Abs(aw[1]-lam*ev[1]) <= 1e-9
	lam2, ev2, _, _ := eigen.Iterate(a, []float64{1, 0}, 2, 1e-9, 100)
	ok("eigen residual|norm|readonly", resOK && slices.Equal(a, ac) && slices.Equal(vc, v0c) &&
		lam2 == lam && slices.Equal(ev2, ev), fmt.Sprintf("λ=%.10f v=%v", lam, ev))

	// 四类错误互不相同；不收敛；被拒后仍可正常使用。
	bad := []error{
		pickErr(eigen.Iterate([]float64{1, 2, 3}, []float64{1, 0}, 2, 1e-9, 10)),
		pickErr(eigen.Iterate(nil, nil, 0, 1e-9, 10)),
		pickErr(eigen.Iterate(a, []float64{0, 0}, 2, 1e-9, 10)),
		pickErr(eigen.Iterate(a, []float64{1, 0}, 2, 1e-12, 1)),
	}
	seen := map[error]bool{}
	distinct := true
	for _, e := range bad {
		distinct = distinct && e != nil && !seen[e]
		seen[e] = true
	}
	_, _, _, afterOK := eigen.Iterate(a, []float64{1, 0}, 2, 1e-9, 50)
	ok("eigen 4 distinct errors, reusable", distinct && errors.Is(bad[3], eigen.ErrNoConvergence) && afterOK == nil,
		fmt.Sprintf("%v|%v|%v|%v", bad[0], bad[1], bad[2], bad[3]))

	// api 包：SelfCheck 覆盖四条不变量。
	s, errN := api.New(1e-12, 500)
	ok("api.New+SelfCheck", errN == nil && s.SelfCheck() == nil, "四条不变量内置核验通过")

	// 大 n 下迭代轮数不随 n 增长（diag(3,1,0,…)，间隙固定 1/3）。
	iters, scaleOK := 0, true
	for i, n := range []int{100, 1000, 10000} {
		da, dv := make([]float64, n*n), make([]float64, n)
		da[0], da[n+1] = 3, 1
		for j := range dv {
			dv[j] = 1
		}
		_, _, it, err := eigen.Iterate(da, dv, n, 1e-9, 500)
		if err != nil || (i > 0 && it != iters) || it > 120 {
			scaleOK = false
		}
		iters = it
	}
	ok("eigen iters independent of n", scaleOK, fmt.Sprintf("n=100/1000/10000 均 %d 轮", iters))

	// 并发：32 个 goroutine 对同一输入调 Eigen，结果逐字节一致。
	lamC, vC, _ := s.Eigen(a, []float64{1, 0}, 2)
	start := make(chan struct{})
	var mismatch atomic.Bool
	var wg sync.WaitGroup
	for k := 0; k < 32; k++ {
		wg.Go(func() {
			<-start
			l, v, err := s.Eigen(a, []float64{1, 0}, 2)
			if err != nil || l != lamC || !slices.Equal(v, vC) {
				mismatch.Store(true)
			}
		})
	}
	close(start)
	wg.Wait()
	ok("api concurrent identical", !mismatch.Load(), "32 goroutines 逐字节一致")

	if failed {
		os.Exit(1)
	}
}

func pickErr(_ float64, _ []float64, _ int, err error) error { return err }

func rqParts(a, v []float64) (num, den float64) {
	av := power.MatVec(a, v, 2)
	for i := range v {
		num += v[i] * av[i]
		den += v[i] * v[i]
	}
	return
}

func rq(a, v []float64) float64 {
	num, den := rqParts(a, v)
	return num / den
}

func near(x, y float64) bool { return math.Abs(x-y) < 1e-9 }
