package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"ontology/api"
	"ontology/route"
	"ontology/shard"
)

var fails int

func ck(name string, cond bool) {
	if cond {
		fmt.Println("OK   " + name)
	} else {
		fmt.Println("FAIL " + name)
		fails++
	}
}

func main() {
	// 1) 五行每步迁移方向：用 route 算术核对，并验证 Rebalance 后实际落点。
	a, _ := api.New(2)
	wantDir := map[string]string{"a": "1->1", "b": "0->2", "c": "1->0", "d": "0->1", "e": "1->2"}
	gotDir := map[string]string{}
	for _, k := range []string{"a", "b", "c", "d", "e"} {
		_ = a.Put(k, strings.ToUpper(k))
		h := route.Hash(k)
		gotDir[k] = fmt.Sprintf("%d->%d", h%2, h%3)
	}
	_ = a.Rebalance(3)
	dumpMatch := true
	for k, w := range wantDir {
		if gotDir[k] != w {
			dumpMatch = false
		}
	}
	// 实际落点：每个 key 必须恰在 h%3 的分区里。
	dp := a.Dump()
	for _, k := range []string{"a", "b", "c", "d", "e"} {
		h := route.Hash(k)
		if _, ok := dp[h%3][k]; !ok {
			dumpMatch = false
		}
	}
	ck("migrate directions a..e: "+strings.Join([]string{gotDir["a"], gotDir["b"], gotDir["c"], gotDir["d"], gotDir["e"]}, " "), dumpMatch)

	// 2) 与朴素重建一致。
	naive, _ := api.New(3)
	for _, k := range []string{"a", "b", "c", "d", "e"} {
		_ = naive.Put(k, strings.ToUpper(k))
	}
	ck("post-Rebalance(3) equals naive rebuild", eqDump(a.Dump(), naive.Dump()))

	// 3) Get("c") 与 GetPartition(0,"b")。
	cv, cok, _ := a.Get("c")
	_, _, bmoved, bhome, _ := a.GetPartition(0, "b")
	ck(`Get("c")=="C" & GetPartition(0,"b") movedTo=2`, cok && cv == "C" && bmoved && bhome == 2)

	// 4) 三类可判定、互不相同的错误。
	_, eNew := api.New(0)
	eReb := a.Rebalance(0)
	eKey := a.Put("", "z")
	_, _, _, _, eRange := a.GetPartition(99, "a")
	distinct := errors.Is(eNew, api.ErrInvalidN) && errors.Is(eReb, api.ErrInvalidN) &&
		errors.Is(eKey, api.ErrEmptyKey) && errors.Is(eRange, api.ErrPartitionOutOfRange) &&
		api.ErrInvalidN != api.ErrEmptyKey && api.ErrEmptyKey != api.ErrPartitionOutOfRange && api.ErrInvalidN != api.ErrPartitionOutOfRange
	ck("three distinct sentinel errors", distinct)

	// 5) 被拒后状态不变。
	ck("state unchanged after rejected ops", eqDump(a.Dump(), naive.Dump()))

	// 6) 大 m 下定位检查分区数不随 m 增长（只取布尔结论，不读计数数值）。
	ck("probes stay constant as m grows", shard.ProbeBoundConstant())

	// 7) 并发只读，逐字段一致（无 sleep）。
	ck("concurrent reads field-identical", concurrentSame(a))

	// 8) 包内置自检。
	ck("SelfCheck", a.SelfCheck() == nil)

	if fails > 0 {
		os.Exit(1)
	}
}

func eqDump(x, y []map[string]string) bool {
	if len(x) != len(y) {
		return false
	}
	for i := range x {
		if len(x[i]) != len(y[i]) {
			return false
		}
		for k, v := range x[i] {
			if y[i][k] != v {
				return false
			}
		}
	}
	return true
}

type res struct {
	val      string
	found    bool
	moved    bool
	home     int
	err      error
	getVal   string
	getFound bool
}

func concurrentSame(a *api.API) bool {
	const G = 32
	var wg sync.WaitGroup
	out := make([]res, G)
	wg.Add(G)
	for g := 0; g < G; g++ {
		go func(i int) {
			defer wg.Done()
			v, f, _ := a.Get("b")
			pv, pf, pm, ph, pe := a.GetPartition(0, "b")
			out[i] = res{pv, pf, pm, ph, pe, v, f}
		}(g)
	}
	wg.Wait()
	for i := 1; i < G; i++ {
		if out[i] != out[0] {
			return false
		}
	}
	return true
}
