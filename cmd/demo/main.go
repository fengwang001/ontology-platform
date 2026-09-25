// demo 逐项核验增量滑动窗口聚合的正确性，全部 OK 时退出码为 0。
package main

import (
	"errors"
	"fmt"
	"maps"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/wagg"
	"ontology/win"
)

var failed bool

func ok(name string, cond bool) {
	status := "OK"
	if !cond {
		status = "FAIL"
		failed = true
	}
	fmt.Printf("%s: %s\n", name, status)
}

func main() {
	// win 包：左开右闭边界判定——左边界 wm-size 不在窗内、且恰好过期。
	ok("win 边界判定(左开右闭)", !win.InWindow(15, 25, 10) && win.InWindow(16, 25, 10) &&
		win.InWindow(25, 25, 10) && win.Expired(15, 25, 10) && !win.Expired(16, 25, 10))
	// wagg 包：成员守恒自检与检查个数复杂度自检。
	agg, err := wagg.New(10, 0)
	if err == nil {
		err = agg.Apply([]wagg.Event{{Key: "a", TS: 5, Val: 10}, {Key: "a", TS: 15, Val: 20}})
	}
	view, dropped := agg.Snapshot()
	ok("wagg 窗口成员守恒自检", err == nil && view["a"] == 20 && dropped == 0 && agg.SelfCheck())
	ok("wagg 大m下检查个数不随m增长", wagg.ProbeSelfTest())
	// 第三节七个事件：逐步 sum 与第 3、6 步的入/出判定。
	w, _ := api.New(10, 0)
	steps := []api.Event{
		{Key: "k", TS: 5, Val: 10}, {Key: "k", TS: 15, Val: 20},
		{Key: "k", TS: 12, Val: 30}, {Key: "k", TS: 12, Val: 40},
		{Key: "k", TS: 25, Val: 50}, {Key: "k", TS: 15, Val: 60},
		{Key: "k", TS: 35, Val: 70},
	}
	wantSums := []int64{10, 20, 50, 90, 50, 50, 70}
	stepOK := true
	var drop3, drop6 int64
	for i, e := range steps {
		_, err = w.Feed([]api.Event{e})
		stepOK = stepOK && err == nil && w.View()["k"] == wantSums[i]
		if i == 2 {
			drop3 = w.Dropped()
		}
		if i == 5 {
			drop6 = w.Dropped()
		}
	}
	ok("七步sum与第3/6步判定", stepOK && drop3 == 0 && drop6 == 1)
	// View 与批量重算一致（每个 Key 至少一条事件留在最终窗口内）。
	w2, _ := api.New(10, 0)
	seq := []api.Event{
		{Key: "a", TS: 1, Val: 5}, {Key: "a", TS: 20, Val: 7}, {Key: "a", TS: 15, Val: 3},
		{Key: "b", TS: 100, Val: 1}, {Key: "b", TS: 50, Val: 9}, {Key: "a", TS: 8, Val: 2},
	}
	_, err = w2.Feed(seq)
	wm, batch := map[string]int64{}, map[string]int64{}
	for _, e := range seq {
		wm[e.Key] = max(wm[e.Key], e.TS)
	}
	for _, e := range seq {
		if e.TS > wm[e.Key]-10 {
			batch[e.Key] += e.Val
		}
	}
	ok("View 与批量重算一致", err == nil && maps.Equal(w2.View(), batch) && w2.Dropped() == 2)
	// 三类可判定且互不相同的错误。
	_, e1 := api.New(0, 0)
	_, e2 := w.Feed([]api.Event{{Key: "", TS: 1, Val: 1}})
	w3, _ := api.New(10, 1)
	_, err = w3.Feed([]api.Event{{Key: "x", TS: 1, Val: 1}})
	_, e3 := w3.Feed([]api.Event{{Key: "x", TS: 2, Val: 2}})
	ok("三类可判定错误", errors.Is(e1, api.ErrBadSize) && errors.Is(e2, api.ErrEmptyKey) && err == nil &&
		errors.Is(e3, api.ErrTooManyOpen) && api.ErrBadSize != api.ErrEmptyKey &&
		api.ErrEmptyKey != api.ErrTooManyOpen && api.ErrBadSize != api.ErrTooManyOpen)
	// 被拒后状态不变（w 已被空 Key 拒过一次；w3 超限整批不生效）。
	before, d0 := w.View(), w.Dropped()
	b3, d3 := w3.View(), w3.Dropped()
	_, _ = w.Feed([]api.Event{{Key: "", TS: 9, Val: 9}})
	_, _ = w3.Feed([]api.Event{{Key: "x", TS: 2, Val: 2}, {Key: "x", TS: 3, Val: 3}})
	ok("被拒后状态不变", maps.Equal(w.View(), before) && w.Dropped() == d0 &&
		maps.Equal(w3.View(), b3) && w3.Dropped() == d3)
	// 并发只读：多 goroutine 同时读，视图逐字段相同。
	var conc atomic.Bool
	conc.Store(true)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				if !maps.Equal(w.View(), before) || w.Dropped() != d0 || !w.SelfCheck() {
					conc.Store(false)
				}
			}
		}()
	}
	wg.Wait()
	ok("并发只读结果一致", conc.Load())
	ok("api.SelfCheck 内置序列四不变量", w.SelfCheck())
	if failed {
		fmt.Println("RESULT: FAIL")
	} else {
		fmt.Println("RESULT: OK")
	}
}
