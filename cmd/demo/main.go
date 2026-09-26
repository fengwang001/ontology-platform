// Command demo 逐项核验 G 计数器的语义并打印 OK/FAIL，全部通过退出码为 0。
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/gc"
	"ontology/reg"
)

var failed bool

func report(name string, ok bool, detail string) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed = true
	}
	fmt.Printf("%s %s %s\n", status, name, detail)
}

func equal(x, y gc.Counter) bool {
	if len(x) != len(y) {
		return false
	}
	for k, v := range x {
		if y[k] != v {
			return false
		}
	}
	return true
}

func main() {
	a := api.New()
	must := func(err error) {
		if err != nil {
			report("六步脚本", false, "意外错误: "+err.Error())
		}
	}
	// 第三节六步脚本，逐步核验 Value("b") 与 a/b 内容
	steps := []struct {
		name string
		run  func()
		want int // Value("b")，-1 表示 b 尚不存在
	}{
		{"S1", func() { must(a.Set("a", nil)); must(a.Inc("a", 0, 5)) }, -1},
		{"S2", func() { must(a.Set("b", nil)); must(a.Inc("b", 1, 3)) }, 3},
		{"S3", func() { _, e := a.MergeInto("b", "a"); must(e) }, 8},
		{"S4", func() { must(a.Inc("a", 0, 2)) }, 8},
		{"S5", func() { _, e := a.MergeInto("b", "a"); must(e) }, 10},
		{"S6", func() {
			must(a.Set("c", nil))
			must(a.Inc("c", 0, 1))
			must(a.Set("d", nil))
			must(a.Inc("d", 1, 1))
		}, 10},
	}
	for _, s := range steps {
		s.run()
		sa, _ := a.Snapshot("a")
		sb, _ := a.Snapshot("b")
		vb, err := a.Value("b")
		ok := (s.want < 0 && err != nil) || (s.want >= 0 && err == nil && vb == s.want)
		report(s.name, ok, fmt.Sprintf("a=%v b=%v Value(b)=%d", sa, sb, vb))
	}

	// 合并交换律与幂等 + 值单调
	x := gc.Counter{0: 7, 1: 3}
	y := gc.Counter{0: 5, 2: 9}
	xy, _ := gc.Merge(x, y)
	yx, _ := gc.Merge(y, x)
	xx, _ := gc.Merge(x, x)
	mono := true
	prev := 0
	must(a.Set("m", nil))
	for i := 1; i <= 5; i++ {
		must(a.Inc("m", i, 1))
		v, _ := a.Value("m")
		if v < prev {
			mono = false
		}
		prev = v
	}
	report("交换律+幂等+值单调", equal(xy, yx) && equal(xx, x) && mono, fmt.Sprintf("a∨b=%v", xy))

	// 三类可判定错误互不相同，被拒后状态不变
	e1 := a.Inc("m", 0, 0)
	e2 := a.Inc("m", -1, 1)
	e3 := a.Set("bad", map[int]int{0: -1})
	distinct := !errors.Is(e1, e2) && !errors.Is(e2, e3) && !errors.Is(e1, e3)
	okErr := errors.Is(e1, reg.ErrNonPositiveIncrement) &&
		errors.Is(e2, reg.ErrNegativeNode) && errors.Is(e3, gc.ErrNegativeEntry)
	vAfter, _ := a.Value("m")
	report("三类错误+状态不变", distinct && okErr && vAfter == 5, fmt.Sprintf("Value(m)=%d", vAfter))

	// 大 node id 空间下 Merge 不整表扫描：1<<30 的节点号在稀疏映射下瞬间完成
	m, err := gc.Merge(gc.Counter{0: 2}, gc.Counter{1 << 30: 1})
	report("大m稀疏读取", err == nil && gc.Value(m) == 3, fmt.Sprintf("值=%d", gc.Value(m)))

	// 并发：M 个 goroutine 对不同 node 做 Inc，总值等于各增量之和；SelfCheck 四不变量
	must(a.Set("cc", nil))
	const M = 64
	var wg sync.WaitGroup
	for i := 0; i < M; i++ {
		wg.Add(1)
		go func(node int) { defer wg.Done(); _ = a.Inc("cc", node, 1) }(i)
	}
	wg.Wait()
	vc, _ := a.Value("cc")
	report("并发总值+SelfCheck", vc == M && a.SelfCheck() == nil, fmt.Sprintf("Value(cc)=%d", vc))

	if failed {
		os.Exit(1)
	}
}
