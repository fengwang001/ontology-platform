// demo 演示蒙特卡洛积分：逐项打印 OK/FAIL，任一失败退出码非 0。
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
)

var failed bool

func ok(name string, cond bool) {
	if cond {
		fmt.Println("OK " + name)
	} else {
		fmt.Println("FAIL " + name)
		failed = true
	}
}

func main() {
	f := func(x float64) float64 { return x * x }

	// 第三节：四步累加与最终估计（四步的 sum/n 与估计值同行打印）。
	in, err := api.New(f, 0, 2)
	ok("new", err == nil)
	xs := []float64{0.5, 1.5, 1.0, 0.0}
	var sum float64
	steps := ""
	nOK := true
	for _, x := range xs {
		sum += f(x)
		if err := in.Add(x); err != nil {
			nOK = false
		}
		steps += fmt.Sprintf(" (x=%.1f,sum=%.2f,n=%d)", x, sum, in.Samples())
	}
	est, err := in.Estimate()
	ok(fmt.Sprintf("steps%s estimate=%.2f", steps, est),
		nOK && err == nil && est == 1.75)

	// 常数函数精确。
	c, _ := api.New(func(float64) float64 { return 3.25 }, -1, 4)
	for _, x := range []float64{-1, 0, 4, 2.5} {
		_ = c.Add(x)
	}
	cest, _ := c.Estimate()
	ok("constant-exact", cest == 3.25*5)

	// 与朴素参照一致（多组随机序列）。
	naive := true
	for seed := int64(1); seed <= 20 && naive; seed++ {
		m, _ := api.New(f, 0, 2)
		var s float64
		var cnt int64
		for i := int64(0); i < 50; i++ {
			x := float64((seed*31+i*17)%200) / 100
			s += float64(f(x)) // 显式转换阻断 FMA 融合，与 mc 的逐点求值一致
			cnt++
			if err := m.Add(x); err != nil {
				naive = false
			}
		}
		e, _ := m.Estimate()
		naive = naive && e == 2*s/float64(cnt)
	}
	ok("naive-replay-match", naive)

	// 零宽区间。
	z, _ := api.New(f, 1.5, 1.5)
	_ = z.Add(1.5)
	zest, _ := z.Estimate()
	ok("zero-width=0", zest == 0)

	// 四类可判定错误互不相同。
	_, e1 := api.New(f, 2, 1)
	_, e2 := api.New(nil, 0, 1)
	e3 := in.Add(3.0)
	e4 := error(nil)
	empty, _ := api.New(f, 0, 1)
	_, e4 = empty.Estimate()
	ok("4-distinct-errors",
		errors.Is(e1, api.ErrInvalidInterval) && errors.Is(e2, api.ErrNilFunc) &&
			errors.Is(e3, api.ErrOutOfRange) && errors.Is(e4, api.ErrNoSamples) &&
			e1 != e2 && e1 != e3 && e1 != e4 && e2 != e3 && e2 != e4 && e3 != e4)

	// 被拒后状态不变，且可继续正常使用。
	before := in.Samples()
	estBefore, _ := in.Estimate()
	_ = in.Add(-1)
	_ = in.Add(99)
	estAfter, _ := in.Estimate()
	cont := in.Add(2.0) == nil
	ok("rejection-leaves-no-trace", in.Samples() == before+1 && estBefore == estAfter && cont)

	// 大 m 下保留采样点个数恒为 0（非导出计数器，由 mc 包内测试钉住）。
	big, _ := api.New(f, 0, 2)
	for _, m := range []int{100, 1000, 10000} {
		for i := 0; i < m; i++ {
			_ = big.Add(float64(i%201) / 100)
		}
	}
	ok("retained=0-O(1)-memory", big.Samples() == 11100)

	// 并发估计结果一致。
	conc, _ := api.New(f, 0, 2)
	for i := 0; i < 1000; i++ {
		_ = conc.Add(float64(i%201) / 100)
	}
	want, _ := conc.Estimate()
	var wg sync.WaitGroup
	same := true
	var mu sync.Mutex
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				e, err := conc.Estimate()
				_ = conc.Samples()
				_ = conc.SelfCheck()
				mu.Lock()
				same = same && err == nil && e == want
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	ok("concurrent-estimates-identical", same)

	// 自检。
	ok("selfcheck", in.SelfCheck() == nil)

	if failed {
		os.Exit(1)
	}
}
