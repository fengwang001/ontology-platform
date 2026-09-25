package main

import (
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/snap"
	"ontology/sw"
)

var failed bool

func check(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed = true
	}
	fmt.Printf("%s: %s\n", name, status)
}

func main() {
	// snap：三类哨兵错误互不相同，且校验函数可判定。
	distinct := snap.ErrGap != snap.ErrNegativeSP &&
		snap.ErrGap != snap.ErrEmptyKey &&
		snap.ErrNegativeSP != snap.ErrEmptyKey
	checks := snap.CheckSnapshot(snap.Snapshot{SP: -1}) == snap.ErrNegativeSP &&
		snap.CheckSnapshot(snap.Snapshot{SP: 0}) == nil &&
		snap.CheckEvent(4, snap.Event{Pos: 6, Key: "a"}) == snap.ErrGap &&
		snap.CheckEvent(4, snap.Event{Pos: 5, Key: ""}) == snap.ErrEmptyKey &&
		snap.CheckEvent(4, snap.Event{Pos: 5, Key: "a"}) == nil
	check("snap sentinel errors distinct & decidable", distinct && checks)

	// sw：第三节冷启动，SP=4 后逐步增量 5..8，每步 state 与推导表一致。
	events := []snap.Event{
		{Pos: 1, Key: "a", Delta: 10}, {Pos: 2, Key: "b", Delta: 20},
		{Pos: 3, Key: "a", Delta: 1}, {Pos: 4, Key: "c", Delta: 30},
		{Pos: 5, Key: "b", Delta: 5}, {Pos: 6, Key: "a", Delta: 2},
		{Pos: 7, Key: "d", Delta: 40}, {Pos: 8, Key: "b", Delta: 3},
	}
	wantSteps := []map[string]int64{
		{"a": 11, "b": 25, "c": 30},
		{"a": 13, "b": 25, "c": 30},
		{"a": 13, "b": 25, "c": 30, "d": 40},
		{"a": 13, "b": 28, "c": 30, "d": 40},
	}
	s := sw.New()
	stepsOK := s.ApplySnapshot(snap.Snapshot{SP: 4, Table: map[string]int64{"a": 11, "b": 20, "c": 30}}) == nil
	for i, ev := range events[4:] {
		if s.ApplyIncremental(ev) != nil || !equalMap(s.View(), wantSteps[i]) {
			stepsOK = false
		}
	}
	check("sw cold-start steps 5..8 states", stepsOK)

	// sw：最终 state 与从头朴素重算一致。
	naive := map[string]int64{}
	for _, ev := range events {
		naive[ev.Key] += ev.Delta
	}
	check("sw final state == naive replay", equalMap(s.View(), naive))

	// api：内置自检覆盖四条不变量。
	check("api SelfCheck", api.SelfCheck() == nil)

	// api：被拒操作不留痕，之后实例仍可用。
	v := api.New()
	rejOK := v.ApplySnapshot(snap.Snapshot{SP: 4, Table: map[string]int64{"a": 11, "b": 20, "c": 30}}) == nil
	before := v.State()
	rejOK = rejOK && v.ApplyIncremental(snap.Event{Pos: 7, Key: "x", Delta: 1}) == snap.ErrGap
	rejOK = rejOK && v.Applied() == 4 && equalMap(v.State(), before)
	rejOK = rejOK && v.ApplyIncremental(snap.Event{Pos: 5, Key: "b", Delta: 5}) == nil
	check("api rejected op leaves no trace", rejOK)

	// api：大 m 快照下增量结果正确（O(1) 由 sw 包内测试钉住）。
	big := api.New()
	table := map[string]int64{}
	for i := 0; i < 10000; i++ {
		table[fmt.Sprintf("k%05d", i)] = int64(i)
	}
	bigOK := big.ApplySnapshot(snap.Snapshot{SP: 50, Table: table}) == nil &&
		big.ApplyIncremental(snap.Event{Pos: 51, Key: "new-key", Delta: 7}) == nil &&
		big.State()["new-key"] == 7 && big.State()["k09999"] == 9999
	check("api large-m snapshot incremental correct", bigOK)

	// api：并发 View 结果逐 key 一致。
	var wg sync.WaitGroup
	concOK := true
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				if !equalMap(v.State(), map[string]int64{"a": 11, "b": 25, "c": 30}) {
					concOK = false
				}
			}
		}()
	}
	wg.Wait()
	check("api concurrent View consistent", concOK)

	if failed {
		os.Exit(1)
	}
}

func equalMap(a, b map[string]int64) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
