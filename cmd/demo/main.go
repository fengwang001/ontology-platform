// demo 演示增量快照同步：按序应用、幂等去重、可判定错误、并发只读。
package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"

	"ontology/api"
	"ontology/delta"
	"ontology/replica"
)

var failed bool

func ok(name string, cond bool) {
	if cond {
		fmt.Println("OK  " + name)
	} else {
		failed = true
		fmt.Println("FAIL " + name)
	}
}

func main() {
	a := api.New()
	seq := []delta.Delta{
		{From: 0, To: 1, Changes: []delta.Change{{Kind: delta.Set, Key: "a", Val: 1}}},
		{From: 1, To: 2, Changes: []delta.Change{{Kind: delta.Set, Key: "b", Val: 2}}},
		{From: 2, To: 3, Changes: []delta.Change{
			{Kind: delta.Set, Key: "c", Val: 3}, {Kind: delta.Del, Key: "b"}}},
		{From: 3, To: 4, Changes: []delta.Change{{Kind: delta.Set, Key: "d", Val: 4}}},
		{From: 0, To: 1, Changes: []delta.Change{{Kind: delta.Set, Key: "a", Val: 100}}},
	}
	for i, d := range seq {
		err := a.Apply(d)
		fmt.Printf("OK  D%d 后 Version=%d State=%v (err=%v)\n", i+1, a.Version(), a.State(), err)
	}
	dup := delta.Delta{From: 0, To: 1, Changes: []delta.Change{{Kind: delta.Set, Key: "a", Val: 100}}}
	before := a.State()
	ok("重复 delta 幂等", a.Apply(dup) == nil && a.Version() == 4 && reflect.DeepEqual(a.State(), before))
	e1 := a.Apply(delta.Delta{From: 9, To: 10})
	e2 := a.Apply(delta.Delta{From: 5, To: 5})
	e3 := a.Apply(delta.Delta{From: -1, To: 0})
	ok("三类错误可判定互不相同且被拒后状态不变", errors.Is(e1, replica.ErrGap) &&
		errors.Is(e2, replica.ErrInvalidRange) && errors.Is(e3, replica.ErrNegativeVersion) &&
		e1 != e2 && e2 != e3 && e1 != e3 &&
		a.Version() == 4 && reflect.DeepEqual(a.State(), before))
	big := api.New()
	for m := 0; m < 10000; m++ {
		_ = big.Apply(delta.Delta{From: m, To: m + 1,
			Changes: []delta.Change{{Kind: delta.Set, Key: "k", Val: m}}})
	}
	ok("大 m 后 From==m 立即应用（O(1) 判定由测试断言）",
		big.Apply(delta.Delta{From: 10000, To: 10001}) == nil && big.Version() == 10001)
	ready := api.New()
	_ = ready.Apply(delta.Delta{From: 0, To: 1, Changes: []delta.Change{{Kind: delta.Set, Key: "x", Val: 7}}})
	wantV, wantS := ready.Version(), ready.State()
	var wg sync.WaitGroup
	consistent := true
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ready.Version() != wantV || !reflect.DeepEqual(ready.State(), wantS) {
				consistent = false
			}
		}()
	}
	wg.Wait()
	ok("并发只读结果一致", consistent)
	ok("SelfCheck", api.New().SelfCheck() == nil)
	if failed {
		os.Exit(1)
	}
}
