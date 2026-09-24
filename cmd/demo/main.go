// Command demo 是增量检查点能力的离线判定演示：不读参数、不联网。
package main

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"reflect"
	"sync"

	"ontology/api"
	"ontology/snap"
	"ontology/state"
)

var failed bool

func report(name string, cond bool) {
	if !cond {
		failed = true
		name = "FAIL: " + name
	} else {
		name = "OK: " + name
	}
	fmt.Println(name)
}

func main() {
	// 第三节脚本（maxKeys=8）：四个检查点。
	st := state.New(8)
	cp := snap.New(st)
	set := func(k string, v int64) {
		if err := st.Set(k, v); err != nil {
			panic(err)
		}
	}
	set("a", 1)
	set("b", 2)
	set("c", 3)
	cp.Checkpoint()
	set("a", 10)
	st.Delete("b")
	set("d", 4)
	cp.Checkpoint()
	set("a", 1)
	set("c", 30)
	set("e", 5)
	cp.Checkpoint()
	set("c", 3)
	cp.Checkpoint()
	h := cp.History()
	rec, _ := cp.Recover()
	v := func(x int64) snap.Change { return snap.Change{Value: x} }
	t := snap.Change{Deleted: true}
	wantHist := []snap.Snapshot{
		{IsBase: true, Data: map[string]snap.Change{"a": v(1), "b": v(2), "c": v(3)}},
		{Data: map[string]snap.Change{"a": v(10), "b": t, "d": v(4)}},
		{Data: map[string]snap.Change{"a": v(1), "c": v(30), "e": v(5)}},
		{Data: map[string]snap.Change{"c": v(3)}},
	}
	report("section-3: all checkpoint contents + final Recover correct",
		reflect.DeepEqual(h, wantHist) &&
			maps.Equal(rec, map[string]int64{"a": 1, "c": 3, "d": 4, "e": 5}))

	// (甲) 合并时忽略 tombstone：base 的 b=2 会残留。
	noTomb := map[string]int64{}
	for _, sn := range h {
		for k, c := range sn.Data {
			if !c.Deleted {
				noTomb[k] = c.Value
			}
		}
	}
	_, bExists := rec["b"]
	report("(甲) b recorded as tombstone; skipping it leaves b lingering at 2",
		h[1].Data["b"] == t && !bExists && noTomb["b"] == 2)

	// (乙) 错误地相对 base 计算 delta：第三点 a==base 被漏，之后不再更新，恢复停在 10。
	wrongA := int64(1)
	for _, x := range []int64{10, 1, 1} { // 三次检查点时 a 的值；buggy 实现只记与 base 不同者
		if x != 1 {
			wrongA = x
		}
	}
	report("(乙) a->1 vs last checkpoint; base-relative diff misses a -> recover a=10",
		h[2].Data["a"] == v(1) && rec["a"] == 1 && wrongA == 10)

	// (丙) delta 合并 first-wins：delta2 的 c=30 挡住 delta3 的 c=3。
	firstWins := map[string]int64{"a": 1, "b": 2, "c": 3}
	seen := map[string]bool{}
	for _, sn := range h[1:] {
		for k, c := range sn.Data {
			if !seen[k] {
				seen[k] = true
				if c.Deleted {
					delete(firstWins, k)
				} else {
					firstWins[k] = c.Value
				}
			}
		}
	}
	report("(丙) last-write-wins c=3; first-wins delta merge keeps c=30",
		rec["c"] == 3 && firstWins["c"] == 30)

	// 三类哨兵错误互不相同；拒绝不留痕、实例仍可用。
	s := api.New(1)
	_, errNoBase := s.Recover()
	distinct := api.ErrEmptyKey != api.ErrTooManyKeys &&
		api.ErrTooManyKeys != api.ErrNoBase && api.ErrEmptyKey != api.ErrNoBase
	report("three distinct sentinel errors; rejected ops leave no trace",
		distinct && errors.Is(s.Set("", 1), api.ErrEmptyKey) && s.Set("a", 1) == nil &&
			errors.Is(s.Set("b", 2), api.ErrTooManyKeys) && errors.Is(errNoBase, api.ErrNoBase) &&
			maps.Equal(s.View(), map[string]int64{"a": 1}))

	// 遍历数只返回成败；scanCount 数值不经任何导出接口外泄。
	report("delta traversal is O(dirty keys=1), flat across m=100..10000", snap.SelfCheck() == nil)

	// 并发只读：N 个 goroutine 同时 Recover，结果逐键相同，无 sleep。
	cs := api.New(8)
	cs.Set("a", 1)
	cs.Set("b", 2)
	cs.Checkpoint()
	const N = 16
	var wg sync.WaitGroup
	rs := make([]map[string]int64, N)
	for g := range N {
		wg.Add(1)
		go func(g int) { defer wg.Done(); rs[g], _ = cs.Recover() }(g)
	}
	wg.Wait()
	concOK := true
	for g := range N {
		if !maps.Equal(rs[g], rs[0]) {
			concOK = false
		}
	}
	report("concurrent Recover by 16 goroutines all agree", concOK)

	report("api.SelfCheck verifies the four invariants", api.New(8).SelfCheck() == nil)

	if failed {
		os.Exit(1)
	}
}
