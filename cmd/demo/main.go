package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"

	"ontology/api"
	"ontology/delta"
)

var failed bool

func check(name string, ok bool) {
	if ok {
		fmt.Println("OK: " + name)
	} else {
		fmt.Println("FAIL: " + name)
		failed = true
	}
}

func main() {
	// delta 包：Set 覆盖、Del 删除（不存在无操作）、空 key 可判定错误。
	m := map[string]int{}
	_ = delta.ApplyChange(m, delta.Set("a", 1))
	_ = delta.ApplyChange(m, delta.Del("a"))
	_ = delta.ApplyChange(m, delta.Del("ghost"))
	check("delta set/del/no-op", len(m) == 0)
	check("delta empty-key error", errors.Is(delta.ApplyChange(m, delta.Set("", 1)), delta.ErrEmptyKey))

	// api 门面（内含 replica）：第三节五个 delta，逐步打印 Version 与 State。
	a := api.New()
	seq := []delta.Delta{
		{From: 0, To: 1, Changes: []delta.Change{delta.Set("a", 1)}},
		{From: 1, To: 2, Changes: []delta.Change{delta.Set("b", 2)}},
		{From: 2, To: 3, Changes: []delta.Change{delta.Set("c", 3), delta.Del("b")}},
		{From: 3, To: 4, Changes: []delta.Change{delta.Set("d", 4)}},
		{From: 0, To: 1, Changes: []delta.Change{delta.Set("a", 100)}}, // 重复，幂等跳过
	}
	want := []map[string]int{
		{"a": 1}, {"a": 1, "b": 2}, {"a": 1, "c": 3},
		{"a": 1, "c": 3, "d": 4}, {"a": 1, "c": 3, "d": 4},
	}
	wantVer := []int{1, 2, 3, 4, 4}
	for i, d := range seq {
		err := a.Apply(d)
		check(fmt.Sprintf("D%d v=%d %v", i+1, a.Version(), a.State()),
			err == nil && a.Version() == wantVer[i] && reflect.DeepEqual(a.State(), want[i]))
	}

	// 三类哨兵错误互不相同，被拒后状态不变；再验重复幂等。
	before, bv := a.State(), a.Version()
	errs := []error{
		a.Apply(delta.Delta{From: 9, To: 10}),
		a.Apply(delta.Delta{From: 0, To: 0}),
		a.Apply(delta.Delta{From: -1, To: 1}),
	}
	dupOK := a.Apply(seq[0]) == nil && a.Version() == bv && reflect.DeepEqual(a.State(), before)
	check("dup idempotent; gap/range/negative distinct, no trace",
		dupOK && errors.Is(errs[0], api.ErrGap) && errors.Is(errs[1], api.ErrInvalidRange) &&
			errors.Is(errs[2], api.ErrNegativeVersion) && errs[0] != errs[1] && errs[1] != errs[2] &&
			a.Version() == bv && reflect.DeepEqual(a.State(), before))
	// SelfCheck 含四不变量与大 m（100/1000/10000）下 probe 读取恒为 1。
	check("self-check (four invariants + O(1) probe)", a.SelfCheck() == nil)

	// 并发只读：N 个 goroutine 读到的 (Version, State) 逐字段相同。
	var wg sync.WaitGroup
	base := a.State()
	agree := true
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if a.Version() != bv || !reflect.DeepEqual(a.State(), base) {
				agree = false
			}
		}()
	}
	wg.Wait()
	check("concurrent readers agree", agree)

	if failed {
		os.Exit(1)
	}
}
