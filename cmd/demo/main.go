package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/win"
	"ontology/wmgr"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
	}
	fmt.Printf("%s %s\n", map[bool]string{true: "OK", false: "FAIL"}[ok], name)
}

func main() {
	// 1. 第三节七步序列：逐步核验 agg/trg/state 与本步触发器输出
	e := api.New(10, 100)
	type step struct {
		op           string
		val          int64
		agg, trg     int64
		st           win.State
		fired, exist bool
	}
	steps := []step{
		{"ingest", 7, 7, 0, win.Active, false, true},
		{"ingest", 5, 12, 1, win.Active, true, true},
		{"purge", 0, 12, 1, win.Purged, false, true},
		{"late", 3, 3, 1, win.Revived, false, true},
		{"late", 8, 11, 1, win.Revived, false, true},
		{"ingest", 2, 13, 1, win.Revived, false, true},
		{"gc", 0, 13, 1, win.Revived, false, true},
	}
	seqOK := true
	for _, s := range steps {
		before := e.Triggers()
		switch s.op {
		case "ingest":
			e.Ingest("1", s.val)
		case "late":
			e.Late("1", s.val)
		case "purge":
			e.Purge("1")
		case "gc":
			e.GC()
		}
		agg, trg, st, ok := e.Snapshot("1")
		fired := e.Triggers() - before
		wantFired := int64(0)
		if s.fired {
			wantFired = 1
		}
		if ok != s.exist || agg != s.agg || trg != s.trg || st != s.st || fired != wantFired {
			seqOK = false
		}
	}
	check("七步序列逐步 agg/trg/state/触发器输出", seqOK && e.Triggers() == 1)

	// 2~4. 复活只服务一条、复活后抑制触发、GC 保留 revived 删除 purged
	g := api.New(10, 100)
	g.Ingest("w", 7)
	g.Ingest("w", 5)
	g.Purge("w")
	g.Late("w", 3)
	agg, _, _, _ := g.Snapshot("w")
	check("复活只服务一条(agg=3 丢弃冻结12)", agg == 3)
	g.Late("w", 8)
	g.Ingest("w", 2)
	_, trg, _, _ := g.Snapshot("w")
	check("复活后抑制触发(trg=1 不增)", trg == 1 && g.Triggers() == 1)
	g.Ingest("v", 1)
	g.Purge("v")
	g.GC()
	_, _, stW, okW := g.Snapshot("w")
	_, _, _, okV := g.Snapshot("v")
	check("GC 保留 revived 删除 purged", okW && stW == win.Revived && !okV)

	// 5~6. 四类可判定错误互不相同；被拒后状态不变
	errs := []error{g.Ingest("", 1), g.Ingest("w", 0), g.Late("ghost", 1)}
	cap1 := api.New(10, 1)
	cap1.Ingest("a", 1)
	errs = append(errs, cap1.Ingest("b", 1))
	want := []error{wmgr.ErrBadID, wmgr.ErrBadVal, wmgr.ErrNoSuchWindow, wmgr.ErrTooManyWindows}
	distinct := true
	for i := range errs {
		if !errors.Is(errs[i], want[i]) {
			distinct = false
		}
		for j := range want {
			if i != j && errors.Is(errs[i], want[j]) {
				distinct = false
			}
		}
	}
	check("四类可判定错误互不相同", distinct)
	bA, _, _, _ := cap1.Snapshot("a")
	_, _, _, okB := cap1.Snapshot("b")
	check("被拒后状态不变且可继续用", bA == 1 && !okB && cap1.Ingest("a", 1) == nil)

	// 7. 大 m 下 GC 行为正确（检查数不随 m 增长的证明见 wmgr 内部测试）
	gcOK := true
	for _, m := range []int{100, 1000, 10000} {
		h := api.New(10, int64(m)+1)
		for i := 0; i < m; i++ {
			h.Ingest(fmt.Sprintf("a%d", i), 1)
		}
		h.Ingest("p", 1)
		h.Purge("p")
		h.GC()
		_, _, _, okP := h.Snapshot("p")
		_, _, _, okA := h.Snapshot("a0")
		gcOK = gcOK && !okP && okA
	}
	check("大 m 下 GC 只删 purged(检查数与 m 无关)", gcOK)

	// 8. 并发：N 个 goroutine Ingest 同一窗口；GC 与 Late 并发一致
	c := api.New(10, 100000)
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); c.Ingest("c", 1) }()
	}
	wg.Wait()
	cAgg, cTrg, _, _ := c.Snapshot("c")
	for i := 0; i < 32; i++ {
		id := fmt.Sprintf("d%d", i)
		c.Ingest(id, 1)
		c.Purge(id)
	}
	for i := 0; i < 32; i++ {
		wg.Add(2)
		go func(i int) { defer wg.Done(); c.Late(fmt.Sprintf("d%d", i), 5) }(i)
		go func() { defer wg.Done(); c.GC() }()
	}
	wg.Wait()
	consistent := true
	for i := 0; i < 32; i++ {
		_, _, st, ok := c.Snapshot(fmt.Sprintf("d%d", i))
		if ok && st != win.Revived { // 存在则必是 revived；不存在即被 GC 删
			consistent = false
		}
	}
	check("并发 Ingest 与 GC/Late 状态一致", cAgg == 64 && cTrg == 1 && consistent)

	// 9. 内置自检
	check("api.SelfCheck 四条不变量", api.SelfCheck() == nil)

	if failed {
		os.Exit(1)
	}
}
