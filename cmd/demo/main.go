// demo 演示物化视图冷启动：全量快照 + 增量切换的各项判定。
package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"

	"ontology/api"
	"ontology/snap"
)

var failed bool

func check(name string, ok bool) {
	if ok {
		fmt.Println("OK " + name)
	} else {
		fmt.Println("FAIL " + name)
		failed = true
	}
}

func main() {
	events := []snap.Event{
		{Pos: 1, Key: "a", Delta: 10}, {Pos: 2, Key: "b", Delta: 20},
		{Pos: 3, Key: "a", Delta: 1}, {Pos: 4, Key: "c", Delta: 30},
		{Pos: 5, Key: "b", Delta: 5}, {Pos: 6, Key: "a", Delta: 2},
		{Pos: 7, Key: "d", Delta: 40}, {Pos: 8, Key: "b", Delta: 3},
	}
	snapshot := snap.Snapshot{SP: 4, Table: map[string]int64{"a": 11, "b": 20, "c": 30}}

	// 1. 第三节四步增量：每步之后的 state 与推导表一致。
	steps := []map[string]int64{
		{"a": 11, "b": 25, "c": 30},
		{"a": 13, "b": 25, "c": 30},
		{"a": 13, "b": 25, "c": 30, "d": 40},
		{"a": 13, "b": 28, "c": 30, "d": 40},
	}
	v := api.New()
	ok := v.ApplySnapshot(snapshot) == nil && v.Applied() == 4
	for i, ev := range events[4:] {
		ok = ok && v.ApplyIncremental(ev) == nil && reflect.DeepEqual(v.State(), steps[i])
	}
	check("冷启动四步增量每步 state 与推导表一致", ok)

	// 2. 最终 state 与朴素重算一致；(甲)(乙)(丙) 错值复现。
	naive := map[string]int64{}
	for _, ev := range events {
		naive[ev.Key] += ev.Delta
	}
	jia := naive["c"] + 30 // (甲) 位点 4 的 c,+30 被重复计
	yi := naive["c"] - 30  // (乙) 位点 4 的 c,+30 被丢
	bing := naive["b"] - 5 // (丙) 位点 5 的 b,+5 被丢
	check("最终 state 与朴素重算一致", reflect.DeepEqual(v.State(), naive))
	check("(甲)重复计位点4→c=60 (乙)丢位点4→c=0 (丙)丢位点5→b=23",
		jia == 60 && yi == 0 && bing == 23)

	// 3. 三类可判定错误互不相同；4. 被拒后状态不变。
	before, beforeApplied := v.State(), v.Applied()
	e1 := v.ApplyIncremental(snap.Event{Pos: 99, Key: "x", Delta: 1})
	e2 := v.ApplySnapshot(snap.Snapshot{SP: -1})
	e3 := v.ApplyIncremental(snap.Event{Pos: 9, Key: ""})
	check("三类错误可判定且互不相同",
		errors.Is(e1, snap.ErrGap) && errors.Is(e2, snap.ErrBadSP) &&
			errors.Is(e3, snap.ErrEmptyKey) && e1 != e2 && e2 != e3 && e1 != e3)
	check("被拒后 state 与 applied 全部不变",
		reflect.DeepEqual(v.State(), before) && v.Applied() == beforeApplied)

	// 5. 大 m 快照下增量结果正确（O(1) 不重扫由 sw 包测试钉住）。
	big := api.New()
	table := map[string]int64{}
	for i := 0; i < 10000; i++ {
		table[fmt.Sprintf("k%05d", i)] = int64(i)
	}
	ok = big.ApplySnapshot(snap.Snapshot{SP: 3, Table: table}) == nil &&
		big.ApplyIncremental(snap.Event{Pos: 4, Key: "k00001", Delta: 100}) == nil &&
		big.State()["k00001"] == 101
	check("m=10000 快照下单条增量结果正确", ok)

	// 6. 并发读结果一致。
	want := v.State()
	var wg sync.WaitGroup
	consistent := true
	var mu sync.Mutex
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				if !reflect.DeepEqual(v.State(), want) {
					mu.Lock()
					consistent = false
					mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()
	check("32 goroutine 并发读结果逐 key 一致", consistent)

	// 7. 自检。
	check("SelfCheck 通过", api.SelfCheck() == nil)

	if failed {
		os.Exit(1)
	}
}
