package main

import (
	"fmt"
	"os"
	"reflect"
	"sync"

	"ontology/api"
	"ontology/tbucket"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
		fmt.Println(name + ": FAIL")
		return
	}
	fmt.Println(name + ": OK")
}

func main() {
	check("negative ts buckets (-12->-2, -1->-1)",
		tbucket.Key(-12, 10) == -2 && tbucket.Key(-1, 10) == -1 &&
			tbucket.Contains(-2, -12, 10) && !tbucket.Contains(-1, -12, 10))

	// 第三节八步分步表：每步后核对该桶计数与 Dropped。
	seq := []int64{5, -12, 0, 10, -1, 20, 9, 30}
	wantCount := []int64{1, 1, 2, 1, 1, 1, 3, 1} // 各步事件落桶的累计计数
	wantDrop := []int64{0, 0, 0, 1, 1, 2, 2, 5}
	m, err := api.New(10, 3)
	stepsOK := err == nil
	for i, ts := range seq {
		if err := m.Feed([]api.Event{{TS: ts, Key: "k"}}); err != nil {
			stepsOK = false
			break
		}
		k := tbucket.Key(ts, 10)
		if m.View()["k"][k] != wantCount[i] || m.Dropped() != wantDrop[i] {
			stepsOK = false
			break
		}
	}
	check("eight-step per-step counts & Dropped", stepsOK)

	check("final retained buckets {1,2,3} & Dropped=5",
		reflect.DeepEqual(m.View(), api.View{"k": {1: 1, 2: 1, 3: 1}}) && m.Dropped() == 5)

	check("selfcheck: view == batch recompute & invariants", m.SelfCheck() == nil)

	// 三类可判定错误互不相同。
	_, eSize := api.New(0, 3)
	_, eR := api.New(10, 0)
	eKey := m.Feed([]api.Event{{TS: 40, Key: ""}})
	check("three distinguishable sentinel errors",
		eSize == api.ErrBadSize && eR == api.ErrBadR && eKey == api.ErrEmptyKey &&
			eSize != eR && eR != eKey && eSize != eKey)

	// 被拒整批不生效：状态与之前完全一致。
	before := m.View()
	bad := m.Feed([]api.Event{{TS: 40, Key: "k"}, {TS: 50, Key: ""}})
	check("rejected batch leaves state intact",
		bad == api.ErrEmptyKey && m.Dropped() == 5 && reflect.DeepEqual(m.View(), before))

	// 大 m 下清理只触碰越界桶（检查个数不随 m 增长的断言见 tpart 包内测试）。
	scaleOK := true
	for _, n := range []int64{100, 1000, 10000} {
		big, err := api.New(1, n)
		if err != nil {
			scaleOK = false
			break
		}
		evs := make([]api.Event, n+1)
		for i := range evs {
			evs[i] = api.Event{TS: int64(i), Key: "k"}
		}
		if err := big.Feed(evs); err != nil || big.Dropped() != 1 || len(big.View()["k"]) != int(n) {
			scaleOK = false
			break
		}
	}
	check("large-m cleanup touches only out-of-window buckets", scaleOK)

	// 并发只读：N 个 goroutine 拿到的 View/Dropped 必须逐 (Key,桶) 相同。
	wantView, finalDrop := m.View(), m.Dropped()
	start := make(chan struct{})
	var wg sync.WaitGroup
	concOK := true
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < 50; j++ {
				if !reflect.DeepEqual(m.View(), wantView) || m.Dropped() != finalDrop || m.SelfCheck() != nil {
					concOK = false
					return
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	check("concurrent read-only results identical", concOK)

	if failed {
		os.Exit(1)
	}
}
