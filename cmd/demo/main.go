package main

import (
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"ontology/api"
	"ontology/dedup"
	"ontology/win"
)

var failed bool

func check(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed = true
	}
	fmt.Printf("%s %s\n", status, name)
}

func main() {
	// win：半开边界 [last, last+W)，d==W 判接受，TS<=last 判重复
	check("win: d==W accepted", !win.Duplicate(10, 15, 5))
	check("win: d<W dup, ts<=last dup", win.Duplicate(10, 14, 5) && win.Duplicate(10, 10, 5) && win.Duplicate(10, 9, 5))

	// dedup+api：第三节八步序列，逐步核对判定与 last（第6步淘汰最旧的 X）
	d, err := api.New(5, 3)
	check("api: New(5,3)", err == nil)
	steps := []struct {
		id       string
		ts       int64
		accepted bool
		last     int64
	}{
		{"X", 10, true, 10}, {"X", 15, true, 15}, {"Y", 20, true, 20},
		{"Z", 30, true, 30}, {"Y", 24, false, 20}, {"W", 40, true, 40},
		{"Y", 25, true, 25}, {"Z", 32, false, 30},
	}
	ok := true
	for _, s := range steps {
		got, err := d.Dedup(s.id, s.ts)
		if err != nil || got != s.accepted || d.View()[s.id] != s.last {
			ok = false
		}
	}
	_, xKept := d.View()["X"]
	check("api: 8-step sequence, evict oldest X", ok && !xKept && len(d.View()) == 3)

	// 三类可判定错误互不相同，且被拒后状态不变
	view, acc, dup := d.View(), d.Accepted(), d.Duplicated()
	_, e1 := d.Dedup("", 1)
	_, e2 := api.New(0, 1)
	_, e3 := api.New(1, 0)
	same := len(view) == len(d.View()) && acc == d.Accepted() && dup == d.Duplicated()
	for k, v := range view {
		same = same && d.View()[k] == v
	}
	check("api: 3 distinct sentinel errors", e1 == api.ErrEmptyID && e2 == api.ErrNonPositiveWindow &&
		e3 == api.ErrNonPositiveMaxOpen && e1 != e2 && e2 != e3 && e1 != e3)
	check("api: state unchanged after rejection", same)
	check("api: SelfCheck", d.SelfCheck() == nil)

	// 大 m 下定位已存在 ID 的耗时不随 m 线性增长（map 定位的行为证据）
	check("dedup: lookup cost independent of m", lookupRatio() < 50)

	// 并发：不同 ID 全部接受；同 ID 同 TS 恰好一个接受
	check("api: concurrent dedup", concurrent())

	if failed {
		os.Exit(1)
	}
}

// lookupRatio 返回 m=100000 与 m=100 时单次 Dedup 已存在 ID 的耗时比。
func lookupRatio() float64 {
	measure := func(m int) time.Duration {
		tb, _ := dedup.New(10, m)
		for i := 0; i < m; i++ {
			_, _ = tb.Dedup(fmt.Sprintf("id%d", i), int64(i*10))
		}
		start := time.Now()
		for i := 0; i < 50000; i++ {
			_, _ = tb.Dedup("id0", 1) // 已存在且判重复，只经定位
		}
		return time.Since(start)
	}
	small, large := measure(100), measure(100000)
	if small <= 0 {
		return 1
	}
	return float64(large) / float64(small)
}

// concurrent 并发去重：N 个不同 ID 全接受；同 ID 同 TS 恰好一个接受。
func concurrent() bool {
	const n = 64
	d, err := api.New(5, n)
	if err != nil {
		return false
	}
	var wg sync.WaitGroup
	var accepted int64
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ok, _ := d.Dedup(fmt.Sprintf("g%d", i), 100)
			if ok {
				atomic.AddInt64(&accepted, 1)
			}
		}(i)
	}
	wg.Wait()
	if accepted != n {
		return false
	}
	var same int64
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, _ := d.Dedup("same", 7)
			if ok {
				atomic.AddInt64(&same, 1)
			}
		}()
	}
	wg.Wait()
	return same == 1
}
