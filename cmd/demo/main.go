// Command demo 逐条打印增量 COUNT DISTINCT 的自检判定（OK/FAIL），
// 不读参数、不联网，退出码 0 表示全部通过。
package main

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"sync"

	"ontology/api"
)

func main() {
	fails := 0
	check := func(name string, ok bool) {
		if ok {
			fmt.Printf("OK   %s\n", name)
		} else {
			fmt.Printf("FAIL %s\n", name)
			fails++
		}
	}

	// 九批逐批日志、第 6 批拒绝不留痕、颠倒第 7 批拒绝、随机批次与批量重算一致、
	// 日志每个前缀自洽且无新旧相等对、多重性非负无零条目——全部在 SelfCheck 内核验。
	check("selfcheck: nine batches, rejects, random, log prefixes, invariants", api.New(10).SelfCheck() == nil)

	// 三类可判定错误互不相同。
	e := api.New(1)
	_, errW := e.Feed([]api.Change{{Group: "g", Val: "x", Sign: -1}}) // 撤回不存在
	_, errI := e.Feed([]api.Change{{Group: "g", Val: "x", Sign: 3}})  // 非法 Sign
	_, _ = e.Feed([]api.Change{{Group: "g", Val: "a", Sign: 1}})
	_, errL := e.Feed([]api.Change{{Group: "g", Val: "b", Sign: 1}}) // 超 maxEntries=1
	check("three distinct sentinel errors",
		errors.Is(errW, api.ErrWithdraw) && errors.Is(errI, api.ErrInvalid) && errors.Is(errL, api.ErrLimit) &&
			!errors.Is(errW, api.ErrInvalid) && !errors.Is(errI, api.ErrLimit) && !errors.Is(errL, api.ErrWithdraw))

	// 拒绝后仍可正常使用。
	outs, err := e.Feed([]api.Change{{Group: "g", Val: "a", Sign: -1}})
	check("rejected batch leaves no trace, engine still usable",
		err == nil && len(outs) == 1 && outs[0] == api.Out{Group: "g", N: 1, Sign: -1} && len(e.View()) == 0)

	// 大 m 下单条变更批的检查条数不随 m 增长。
	check("check count independent of group size m", api.New(0).ScalingOK())

	// 并发：N 个 goroutine 只读同一实例视图一致；N 个各喂互不相交的组，结果等于批量重算。
	c := api.New(0)
	for i := 0; i < 50; i++ {
		_, _ = c.Feed([]api.Change{{Group: "g", Val: string(rune('a' + i)), Sign: 1}})
	}
	var wg sync.WaitGroup
	views := make([]map[string]int, 16)
	for i := range views {
		wg.Add(1)
		go func(k int) { defer wg.Done(); views[k] = c.View() }(i)
	}
	wg.Wait()
	same := true
	for _, v := range views[1:] {
		same = same && maps.Equal(v, views[0])
	}
	d := api.New(0)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				_, _ = d.Feed([]api.Change{{Group: string(rune('A' + k)), Val: string(rune('a' + j)), Sign: 1}})
			}
		}(i)
	}
	wg.Wait()
	check("concurrent readers identical, disjoint writers match recompute",
		same && len(views[0]) == 1 && views[0]["g"] == 50 && len(d.View()) == 8)

	if fails != 0 {
		os.Exit(1)
	}
}
