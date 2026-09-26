// 演示程序：逐条打印蒙特卡洛积分各项判定的 OK/FAIL，全部通过退出码为 0。
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
)

var failed bool

func report(name string, ok bool, detail string) {
	if !ok {
		failed = true
		fmt.Printf("FAIL %s %s\n", name, detail)
		return
	}
	fmt.Printf("OK %s %s\n", name, detail)
}

func main() {
	est, err := api.New(func(x float64) float64 { return x * x }, 0, 2)
	if err != nil {
		report("setup", false, err.Error())
		os.Exit(1)
	}

	// 第三节四步序列：每步之后核对累计 sum、n 与当时估计值。
	xs := []float64{0.5, 1.5, 1.0, 0.0}
	wantSum := []float64{0.25, 2.5, 3.5, 3.5}
	var sum float64
	var n int64
	stepsOK := true
	for i, x := range xs {
		if est.Add(x) != nil {
			stepsOK = false
		}
		sum += x * x
		n++
		v, err := est.Estimate()
		if err != nil || sum != wantSum[i] || est.Samples() != n || v != 2*sum/float64(n) {
			stepsOK = false
		}
	}
	report("steps", stepsOK, "sum/n: (0.25,1) (2.5,2) (3.5,3) (3.5,4)")
	v, _ := est.Estimate()
	report("estimate", v == 1.75, "Estimate=1.75 (错值: 甲7 乙0.875 丙1.09375)")

	// 常数函数精确。
	ce, _ := api.New(func(float64) float64 { return 3.75 }, 0, 2)
	for i := 0; i < 7; i++ {
		_ = ce.Add(1.0)
	}
	cv, _ := ce.Estimate()
	report("const-exact", cv == 7.5, "c=3.75 -> 7.5")

	// 与朴素参照一致（确定性采样点）。
	re, _ := api.New(func(x float64) float64 { return x*x - x }, -1, 3)
	var rsum float64
	for i := 0; i < 500; i++ {
		x := -1 + 4*float64(i)/499
		_ = re.Add(x)
		rsum += x*x - x
	}
	rv, _ := re.Estimate()
	report("naive-replay", rv == 4*rsum/500, "500 pts")

	// 零宽区间。
	ze, _ := api.New(func(x float64) float64 { return x * x }, 1.5, 1.5)
	_ = ze.Add(1.5)
	zv, _ := ze.Estimate()
	report("zero-width", zv == 0, "a=b=1.5 -> 0")

	// 四类可判定错误，互不相同。
	_, e1 := api.New(func(x float64) float64 { return x }, 2, 1)
	_, e2 := api.New(nil, 0, 1)
	ne, _ := api.New(func(x float64) float64 { return x }, 0, 1)
	_, e4 := ne.Estimate()
	e3 := ne.Add(1.1)
	sents := []error{api.ErrInvalidInterval, api.ErrNilFunc, api.ErrOutOfRange, api.ErrNoSamples}
	got := []error{e1, e2, e3, e4}
	distinct := true
	for i := range sents {
		if !errors.Is(got[i], sents[i]) {
			distinct = false
		}
		for j := range sents {
			if i != j && errors.Is(sents[i], sents[j]) {
				distinct = false
			}
		}
	}
	report("sentinel-errors", distinct, "4 distinct")

	// 被拒后状态不变。
	_ = ne.Add(0.5)
	before, _ := ne.Estimate()
	_ = ne.Add(-0.1)
	_ = ne.Add(2.0)
	after, _ := ne.Estimate()
	report("no-trace", ne.Samples() == 1 && before == after, "samples=1")

	// 自检：四条不变量 + 大 m 下保留采样点个数恒为 0（O(1)）。
	report("selfcheck", est.SelfCheck() == nil, "invariants + retained==0 @m<=10000")

	// 并发估计结果一致。
	want, _ := est.Estimate()
	var wg sync.WaitGroup
	same := true
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				if v, err := est.Estimate(); err != nil || v != want {
					same = false
				}
			}
		}()
	}
	wg.Wait()
	report("concurrent", same, "16x50 identical")

	if failed {
		os.Exit(1)
	}
}
