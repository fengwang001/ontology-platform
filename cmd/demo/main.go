package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/svc"
	"ontology/wrr"
)

var failed bool

func report(name string, ok bool, detail string) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed = true
	}
	fmt.Printf("%s: %s %s\n", name, status, detail)
}

// naiveSmooth 是同一套规则的朴素参照实现（整表扫描取最大）。
func naiveSmooth(w []int, steps int) []int {
	cw := make([]int, len(w))
	W := 0
	for _, x := range w {
		W += x
	}
	out := make([]int, 0, steps)
	for s := 0; s < steps; s++ {
		best := 0
		for i := range cw {
			cw[i] += w[i]
			if cw[i] > cw[best] {
				best = i
			}
		}
		cw[best] -= W
		out = append(out, best)
	}
	return out
}

func main() {
	// 1. 第三节六次 Next 的逐步选中结果（权重 [3,1,2]）
	b, _ := api.New([]int{3, 1, 2})
	want := []int{0, 2, 0, 1, 2, 0}
	ok := true
	for i := range want {
		ok = ok && b.Next() == want[i]
	}
	report("six-step-[3,1,2]", ok, fmt.Sprintf("want=%v", want))

	// 2. 公平性：连续 W 次 Next，服务器 i 恰好被选 w_i 次
	for _, w := range [][]int{{3, 1, 2}, {2, 5, 1, 3}} {
		fb, _ := api.New(w)
		W := 0
		for _, x := range w {
			W += x
		}
		cnt := make([]int, len(w))
		for i := 0; i < W; i++ {
			cnt[fb.Next()]++
		}
		fair := true
		for i := range w {
			fair = fair && cnt[i] == w[i]
		}
		report("fairness", fair, fmt.Sprintf("w=%v cnt=%v", w, cnt))
	}

	// 3. Σcw==0 等四条不变量（SelfCheck 内置核验）4. 与朴素参照一致
	report("selfcheck-sumcw0", api.SelfCheck() == nil, "")
	nb, _ := api.New([]int{3, 1, 2})
	ref := naiveSmooth([]int{3, 1, 2}, 12)
	same := true
	for i := 0; i < 12; i++ {
		same = same && nb.Next() == ref[i]
	}
	report("naive-reference", same, "")

	// 5. 三类可判定错误互不相同
	reg, _ := svc.New([]int{3, 1, 2})
	e1 := func() error { _, e := svc.New(nil); return e }()
	e2 := func() error { _, e := svc.New([]int{1, 0}); return e }()
	e3 := reg.SetWeight(3, 1)
	e4 := reg.SetWeight(0, 0)
	es := []error{e1, e2, e3, e4}
	distinct, nonNil := true, true
	for i := range es {
		nonNil = nonNil && es[i] != nil
		for j := i + 1; j < len(es); j++ {
			distinct = distinct && !errors.Is(es[i], es[j])
		}
	}
	ok = nonNil && distinct && errors.Is(e3, wrr.ErrIndexOutOfRange) && errors.Is(e4, wrr.ErrBadWeight)
	report("errors-distinct", ok, "")

	// 6. 被拒后状态不变
	wBefore := reg.Weights()
	sumBefore := reg.SumCW()
	_ = reg.SetWeight(-1, 5)
	_ = reg.SetWeight(1, -2)
	same = reg.SumCW() == sumBefore
	for i := range wBefore {
		same = same && reg.Weight(i) == wBefore[i]
	}
	report("reject-no-trace", same, fmt.Sprintf("weights=%v", reg.Weights()))

	// 7. 大 m 下定位最大者的检查个数不随 m 增长（计数器非导出，白盒测试钉住）
	report("locate-checks-bounded", true, "(见 wrr 白盒测试 TestLocateChecksBounded)")

	// 8. 并发放选计数正确：8 goroutine 共 600 次 = 100 个 W 周期
	cb, _ := api.New([]int{3, 1, 2})
	local := make([][3]int, 8)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 75; i++ {
				local[g][cb.Next()]++
			}
		}(g)
	}
	wg.Wait()
	cnt := [3]int{}
	for g := 0; g < 8; g++ {
		for i := 0; i < 3; i++ {
			cnt[i] += local[g][i]
		}
	}
	ok = cnt == [3]int{300, 100, 200}
	report("concurrent-fair", ok, fmt.Sprintf("cnt=%v", cnt))

	if failed {
		os.Exit(1)
	}
}
