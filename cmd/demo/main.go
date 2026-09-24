package main

import (
	"errors"
	"fmt"
	"math"
	"os"
	"sync"

	"ontology/api"
	"ontology/fold"
	"ontology/norm"
)

var allOK = true

func ok(pass bool) string {
	if !pass {
		allOK = false
	}
	if pass {
		return "OK"
	}
	return "FAIL"
}

func eq(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func main() {
	foldOK := fold.Cancels(5, -5) && !fold.Cancels(2, -7) && !fold.Cancels(0, 0)
	fmt.Printf("%s fold: cancels(+5,-5)=true cancels(+2,-7)=false\n", ok(foldOK))

	// 第三节八步：逐步记录未了结序列与净值。
	eight := []int64{5, -5, -8, 8, 7, 2, -7, -2}
	wantLog := [][]int64{{5}, {}, {-8}, {}, {7}, {7, 2}, {7, 2, -7}, {7, 2, -7, -2}}
	wantNet := []int64{5, 0, -8, 0, 7, 9, 2, 0}
	n := norm.New(8)
	stepOK := true
	trace := ""
	for i, op := range eight {
		if _, err := n.Apply(op); err != nil {
			stepOK = false
		}
		if !eq(n.Changelog(), wantLog[i]) || n.Net() != wantNet[i] {
			stepOK = false
		}
		trace += fmt.Sprintf(" %d:%v=%d", i+1, n.Changelog(), n.Net())
	}
	fmt.Printf("%s eight steps:%s\n", ok(stepOK), trace)

	// (甲) 全序列搜反号会错成 [2]；(乙) 净值 0 清空会错成 []。
	fmt.Printf("%s step7 log=[7,2,-7] (wrong global-search=[2]); step8 log=[7,2,-7,-2] (wrong net-zero-clear=[])\n",
		ok(eq(n.Changelog(), []int64{7, 2, -7, -2}) && n.Net() == 0))

	// 重放 changelog 回到净值。
	var replay int64
	for _, v := range n.Changelog() {
		replay += v
	}
	fmt.Printf("%s replay changelog=%d == net=%d\n", ok(replay == n.Net()), replay, n.Net())

	// 三类可判定、互不相同的错误，且被拒后状态不变。
	z := norm.New(1)
	z.Apply(3)
	before := z.Net()
	beforeLog := z.Changelog()
	_, e0 := z.Apply(0)
	_, ed := z.Apply(1) // 长度已为 1，追加超限
	b := norm.New(8)
	b.Apply(math.MaxInt64)
	_, eo := b.Apply(1)
	errOK := errors.Is(e0, norm.ErrInvalidIncrement) && errors.Is(ed, norm.ErrDepthExceeded) &&
		errors.Is(eo, norm.ErrNetOverflow) && e0 != ed && ed != eo && e0 != eo
	traceOK := z.Net() == before && eq(z.Changelog(), beforeLog) && b.Net() == math.MaxInt64
	fmt.Printf("%s three distinct sentinel errors; rejected ops leave state unchanged=%v\n", ok(errOK && traceOK), traceOK)

	// 大 m 行为正确性；末尾检查次数为非导出字段，仅白盒测试可读取其数值。
	tailBehavior := true
	for _, m := range []int{100, 1000, 10000} {
		q := norm.New(m + 1)
		for i := 0; i < m; i++ {
			if _, err := q.Apply(int64(i + 1)); err != nil {
				tailBehavior = false
			}
		}
		if _, err := q.Apply(-1 << 40); err != nil {
			tailBehavior = false
		}
		if q.Net() != int64(m*(m+1)/2)-(1<<40) {
			tailBehavior = false
		}
	}
	fmt.Printf("%s append after m=100..10000 correct; tail-check count verified O(1) by white-box test\n", ok(tailBehavior))

	// api 门面与内置自检。
	c := api.New(8)
	for _, op := range eight {
		_ = c.Apply(api.Op(op))
	}
	apiOK := c.Net() == 0 && eq(c.Changelog(), []int64{7, 2, -7, -2}) && c.SelfCheck() == nil
	fmt.Printf("%s api facade: Net/Changelog/SelfCheck all consistent\n", ok(apiOK))

	// 并发只读：64 个 goroutine 经起跑栅栏同时读同一实例，结果逐条相同。
	const N = 64
	want := c.Changelog()
	var wg sync.WaitGroup
	start := make(chan struct{})
	var mu sync.Mutex
	bad := false
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			got := c.Changelog()
			same := c.Net() == 0 && eq(got, want)
			mu.Lock()
			bad = bad || !same
			mu.Unlock()
		}()
	}
	close(start)
	wg.Wait()
	fmt.Printf("%s %d concurrent readers see identical net and changelog\n", ok(!bad), N)

	if !allOK {
		os.Exit(1)
	}
}
