package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/dedup"
	"ontology/win"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
	}
	fmt.Printf("%s %v\n", map[bool]string{true: "OK", false: "FAIL"}[ok], name)
}

func main() {
	// win：半开窗口 [last, last+W)，d==W 不重复，乱序 ts<=last 重复
	check("win: boundary d==W accept, d<W dup, out-of-order dup",
		!win.Duplicate(10, 15, 5) && win.Duplicate(10, 14, 5) && win.Duplicate(10, 10, 5) && win.Duplicate(10, 3, 5))

	// dedup：表满接受新 ID 淘汰 last 最小者；重复不刷新 last
	tab := dedup.New(5, 3)
	tab.Dedup("X", 10)
	tab.Dedup("X", 15)
	tab.Dedup("Y", 20)
	tab.Dedup("Z", 30)
	tab.Dedup("Y", 24) // 重复，Y.last 必须仍是 20
	tab.Dedup("W", 40) // 表满，淘汰 X(15)
	v := tab.View()
	_, xGone := v["X"]
	check("dedup: evict oldest (X) on full, dup keeps last",
		!xGone && v["Y"] == 20 && v["Z"] == 30 && v["W"] == 40)

	// api：第三节八步序列的判定与 last
	d, _ := api.New(5, 3)
	type ev struct {
		id   string
		ts   int64
		want bool
	}
	seq := []ev{{"X", 10, true}, {"X", 15, true}, {"Y", 20, true}, {"Z", 30, true},
		{"Y", 24, false}, {"W", 40, true}, {"Y", 25, true}, {"Z", 32, false}}
	ok := true
	for _, e := range seq {
		got, err := d.Dedup(e.id, e.ts)
		ok = ok && err == nil && got == e.want
	}
	fin := d.View()
	check("api: 8-step verdicts, last values, evict X at step 6",
		ok && len(fin) == 3 && fin["Y"] == 25 && fin["Z"] == 30 && fin["W"] == 40)

	// api：三类互不相同的可判定错误，被拒后状态不变
	acc, dup, nv := d.Accepted(), d.Duplicated(), len(d.View())
	_, e1 := d.Dedup("", 1)
	_, e2 := api.New(0, 1)
	_, e3 := api.New(1, -2)
	check("api: 3 distinct sentinel errors, rejection leaves no trace",
		errors.Is(e1, api.ErrEmptyID) && errors.Is(e2, api.ErrNonPositiveWindow) &&
			errors.Is(e3, api.ErrNonPositiveMaxOpen) && e1 != e2 && e2 != e3 &&
			d.Accepted() == acc && d.Duplicated() == dup && len(d.View()) == nv)

	// 大 m 下定位已存在 ID 的判定与表规模无关（行为演示；probes 计数证明见 dedup 包内测试）
	ok = true
	for _, m := range []int{100, 1000, 10000} {
		dm, _ := api.New(5, m+1)
		for i := 0; i < m; i++ {
			dm.Dedup(fmt.Sprintf("id%d", i), int64(i*10))
		}
		got, _ := dm.Dedup("id0", 1) // 乱序重复，O(1) 定位
		ok = ok && !got
	}
	check("dedup: locate existing ID independent of m (100..10000)", ok)

	// 并发：N 个不同 ID 全接受；同一 ID 相同 TS 恰好一个接受
	const n = 64
	dc, _ := api.New(5, n+1)
	var wg sync.WaitGroup
	start := make(chan struct{})
	wins := make(chan bool, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			dc.Dedup(fmt.Sprintf("c%d", i), 100)
			ok, _ := dc.Dedup("same", 100)
			wins <- ok
		}(i)
	}
	close(start)
	wg.Wait()
	exactlyOne := 0
	for i := 0; i < n; i++ {
		if <-wins {
			exactlyOne++
		}
	}
	check("concurrent: N distinct IDs accepted, same ID exactly one",
		dc.Accepted() == int64(n+1) && exactlyOne == 1)

	check("api: SelfCheck passes", d.SelfCheck() == nil)
	if failed {
		os.Exit(1)
	}
}
