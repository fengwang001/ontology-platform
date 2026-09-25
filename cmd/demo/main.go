// Command demo 逐条打印去重器关键判定，全部 OK 时退出码 0。
package main

import (
	"errors"
	"fmt"
	"ontology/api"
	"ontology/dedup"
	"os"
	"reflect"
	"sync"
)

func report(name string, ok bool) bool {
	if ok {
		fmt.Printf("OK: %s\n", name)
	} else {
		fmt.Printf("FAIL: %s\n", name)
	}
	return ok
}

func main() {
	pass := true

	// 1. 六步序列：每步之后的 state 与已应用集均与 NOTES 表一致。
	s := api.New()
	ops := []struct {
		txid      int64
		key       string
		delta     int
		wantState map[string]int
		wantSet   []int64
	}{
		{1, "k", 5, map[string]int{"k": 5}, []int64{1}},
		{2, "k", 3, map[string]int{"k": 8}, []int64{1, 2}},
		{1, "k", 5, map[string]int{"k": 8}, []int64{1, 2}},
		{3, "k", 5, map[string]int{"k": 13}, []int64{1, 2, 3}},
		{2, "k", 3, map[string]int{"k": 13}, []int64{1, 2, 3}},
		{4, "m", 2, map[string]int{"k": 13, "m": 2}, []int64{1, 2, 3, 4}},
	}
	sixOK := true
	for i, o := range ops {
		if err := s.Apply(o.txid, o.key, o.delta); err != nil {
			sixOK = false
		}
		st, set := s.Snapshot()
		if !reflect.DeepEqual(st, o.wantState) || !reflect.DeepEqual(set, o.wantSet) {
			sixOK = false
		}
		_ = i
	}
	pass = report("six-step state & applied-set per step", sixOK) && pass

	// 2. 第4步同内容不同 txid 被应用（state[k]=13，不是按内容去重的 8）。
	st, _ := s.Snapshot()
	pass = report("step4 same content/different txid applied (k=13 not 8)", st["k"] == 13) && pass

	// 3. 重复 Apply 幂等：相同内容、甚至不同内容重投都不改状态。
	before, beforeSet := s.Snapshot()
	_ = s.Apply(1, "k", 5)
	_ = s.Apply(1, "z", 77)
	after, afterSet := s.Snapshot()
	pass = report("duplicate txid idempotent (even with changed payload)",
		reflect.DeepEqual(before, after) && reflect.DeepEqual(beforeSet, afterSet)) && pass

	// 4. Restore 后已恢复 txid 的重复到达仍被跳过。
	r := api.New()
	_ = r.Restore([]int64{1, 2, 3})
	rb, rbSet := r.Snapshot()
	_ = r.Apply(2, "k", 9)
	ra, raSet := r.Snapshot()
	pass = report("restore: redelivery of restored txid skipped",
		reflect.DeepEqual(rb, ra) && reflect.DeepEqual(rbSet, raSet)) && pass

	// 5. 三类可判定、互不相同的哨兵错误。
	var e1, e2, e3 error = api.ErrInvalidTxID, api.ErrEmptyKey, api.ErrZeroDelta
	distinct := errors.Is(s.Apply(0, "k", 1), api.ErrInvalidTxID) &&
		errors.Is(s.Apply(9, "", 1), api.ErrEmptyKey) &&
		errors.Is(s.Apply(9, "k", 0), api.ErrZeroDelta) &&
		e1 != e2 && e2 != e3
	pass = report("three distinct decidable sentinel errors", distinct) && pass

	// 6. 被拒后 state 与已应用集完全不变，服务仍可正常使用。
	post, postSet := s.Snapshot()
	traceFree := reflect.DeepEqual(after, post) && reflect.DeepEqual(afterSet, postSet) && s.Apply(5, "n", 1) == nil
	pass = report("rejected ops leave no trace; service still usable", traceFree) && pass

	// 7. 大 m 下查重探查个数不随集合规模增长（判定形式，不读计数值）。
	pass = report("dedup probe count constant at m=100..10000", dedup.CheckProbeComplexity() == nil) && pass

	// 8. 并发：不同 txid 结果同朴素参照；同一 txid 恰好应用一次。
	c := api.New()
	const N = 64
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func(i int) { defer wg.Done(); _ = c.Apply(int64(100+i), "d", 1) }(i)
	}
	wg.Wait()
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() { defer wg.Done(); _ = c.Apply(999, "s", 5) }()
	}
	wg.Wait()
	cst, cset := c.Snapshot()
	concOK := cst["d"] == N && cst["s"] == 5 && len(cset) == N+1
	pass = report("concurrent: distinct txids match naive; same txid once", concOK) && pass

	// 9. SelfCheck 四条不变量。
	pass = report("api SelfCheck all four invariants", api.New().SelfCheck() == nil) && pass

	if !pass {
		fmt.Println("FAIL: demo")
		os.Exit(1)
	}
}
