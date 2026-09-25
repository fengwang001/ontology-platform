package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
		fmt.Println("FAIL", name)
	} else {
		fmt.Println("OK  ", name)
	}
}

func main() {
	// 1. 第三节八步序列：逐步返回值与第 4/6/8 步判定
	e := api.New()
	must := func(err error) {
		if err != nil {
			failed = true
			fmt.Println("FAIL write:", err)
		}
	}
	must(e.Write("g0", "a", 5))  // 1
	g2, _ := e.ReadG("g0")       // 2
	must(e.Write("g0", "b", 3))  // 3
	g4, _ := e.ReadG("g0")       // 4
	must(e.Write("g1", "c", 7))  // 5
	t6 := e.ReadTotal()          // 6
	must(e.Write("g0", "b", 10)) // 7
	t8 := e.ReadTotal()          // 8
	check("eight-step returns", g2 == 5 && g4 == 8 && t6 == 15 && t8 == 22)

	// 2. 三类可判定错误，互不相同
	e1, e2, e3 := e.Write("", "x", 1), e.Write("g0", "", 1), e.Write("g1", "a", 1)
	check("distinguishable errors",
		errors.Is(e1, api.ErrEmptyGroup) && errors.Is(e2, api.ErrEmptyKey) &&
			errors.Is(e3, api.ErrGroupConflict) &&
			!errors.Is(e1, api.ErrEmptyKey) && !errors.Is(e2, api.ErrGroupConflict) &&
			!errors.Is(e3, api.ErrEmptyGroup))

	// 3. 被拒后状态不变
	g9, _ := e.ReadG("g0")
	t9 := e.ReadTotal()
	check("reject leaves no trace", g9 == 15 && t9 == 22)

	// 4. View 与串行批量重算一致（确定性伪随机写序列）
	e2x := api.New()
	seed := int64(7)
	rnd := func(n int64) int64 { seed = (seed*6364136223846793005 + 1442695040888963407) >> 33; return seed % n }
	model := map[string]int64{}
	var tot int64
	for i := 0; i < 500; i++ {
		g := fmt.Sprintf("g%d", rnd(5))
		k := fmt.Sprintf("k%d", rnd(60))
		v := rnd(100)
		if e2x.Write(g, k, v) == nil {
			delta := v - lastVal(k, v) // 模型按 key 最后写入值累计
			model[g] += delta
			tot += delta
		}
	}
	gv, gt := e2x.View()
	eq := gt == tot && len(gv) == len(model)
	for g, s := range model {
		eq = eq && gv[g] == s
	}
	check("view matches batch recompute", eq)

	// 5. 大 m 下判新鲜 O(1)（行为验证；遍历计数断言见 vcache 包测试）
	e3x := api.New()
	for i := 0; i < 10000; i++ {
		must(e3x.Write("g0", fmt.Sprintf("k%d", i), int64(i)))
	}
	_, _ = e3x.ReadG("g0")
	must(e3x.Write("g0", "k0", 1))
	gm, _ := e3x.ReadG("g0")
	check("big-m invalidate+recompute", gm == 49995001)

	// 6. 并发只读结果逐字段一致
	views := make([]int64, 64)
	var wg sync.WaitGroup
	for i := range views {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, t := e3x.View()
			views[i] = t
		}(i)
	}
	wg.Wait()
	same := true
	for _, v := range views {
		same = same && v == views[0]
	}
	check("concurrent views identical", same)

	// 7. 自检
	check("SelfCheck", e.SelfCheck() == nil)

	if failed {
		os.Exit(1)
	}
}

// lastVal 记录每个 key 上一次写入的值（演示用模型）。
var last = map[string]int64{}

func lastVal(k string, v int64) int64 {
	old := last[k]
	last[k] = v
	return old
}
