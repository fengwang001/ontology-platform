package main

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/ckpt"
)

var failed bool

func check(name string, ok bool) {
	if ok {
		fmt.Printf("OK   %s\n", name)
	} else {
		fmt.Printf("FAIL %s\n", name)
		failed = true
	}
}

func main() {
	check("ckpt decide/fold",
		ckpt.Decide(0, 0, false) == ckpt.SkipPersisted &&
			ckpt.Decide(5, 0, true) == ckpt.SkipInFlight &&
			ckpt.Decide(5, 0, false) == ckpt.Accept &&
			ckpt.Fold(-1, func(o int64) bool { return o != 2 }) == 1)

	// 第三节七步轨迹：逐步核对 cp 与 sum["k"]（pending 效果经第5步幂等与第7步结果体现）
	a, err := api.New(16)
	steps := []struct {
		op      func()
		cp, sum int64
	}{
		{func() { _ = a.Apply("k", 0, 10) }, -1, 0},
		{func() { _ = a.Apply("k", 2, 20) }, -1, 0},
		{func() { _ = a.Apply("k", 3, 30) }, -1, 0},
		{a.Commit, 0, 10},
		{func() { _ = a.Apply("k", 2, 20) }, 0, 10}, // 在途重复，幂等跳过
		{func() { _ = a.Apply("k", 1, 40) }, 0, 10},
		{a.Commit, 3, 100},
	}
	traceOK := err == nil
	for _, s := range steps {
		s.op()
		if a.Checkpoint() != s.cp || a.Sum("k") != s.sum {
			traceOK = false
		}
	}
	check("seven-step trace + commits + step5 idempotent", traceOK)

	// 与朴素参照一致：确定性伪随机顺序 Apply/Commit 交错，参照同样只在提交点推进
	b, _ := api.New(512)
	applied := map[int64]int64{}
	ncp, nsum := int64(-1), int64(0)
	naiveOK := true
	for i := 0; i < 200; i++ {
		off := int64((i * 37) % 200)
		_ = b.Apply("k", off, int64(i))
		if _, dup := applied[off]; !dup {
			applied[off] = int64(i)
		}
		if i%7 == 0 {
			b.Commit()
			for d, ok := applied[ncp+1]; ok; d, ok = applied[ncp+1] {
				nsum += d
				ncp++
			}
		}
		if b.Checkpoint() != ncp || b.Sum("k") != nsum {
			naiveOK = false
		}
	}
	check("checkpoint/sum match naive reference", naiveOK)

	// Restore（丙）：崩溃后 cp/sum 保留、pending 丢失，从 cp+1 重放精确恢复
	c, _ := api.New(16)
	_, _, _ = c.Apply("k", 0, 10), c.Apply("k", 2, 20), c.Apply("k", 3, 30)
	c.Commit()
	c.Restore()
	restored := c.Checkpoint() == 0 && c.Sum("k") == 10
	_, _, _ = c.Apply("k", 1, 40), c.Apply("k", 2, 20), c.Apply("k", 3, 30)
	c.Commit()
	check("restore keeps cp/sum; replay from cp+1 exact", restored && c.Checkpoint() == 3 && c.Sum("k") == 100)

	// 三类可判定错误互不相同
	e1, e2 := c.Apply("", 50, 1), c.Apply("k", -1, 1)
	d, _ := api.New(2)
	_, _ = d.Apply("k", 0, 1), d.Apply("k", 1, 1)
	e3 := d.Apply("k", 2, 1)
	check("three sentinel errors distinguishable",
		errors.Is(e1, api.ErrEmptyKey) && errors.Is(e2, api.ErrNegativeOffset) &&
			errors.Is(e3, api.ErrTooManyPending) && e1 != e2 && e2 != e3 && e1 != e3)

	// 被拒后状态不变，且可继续正常使用
	cpBefore, sumBefore := d.Checkpoint(), d.Sum("k")
	d.Commit()
	check("rejected apply leaves no trace, still usable",
		cpBefore == -1 && sumBefore == 0 && d.Checkpoint() == 1 && d.Sum("k") == 2)

	// 大 m 下判重行为正确（检查个数 O(1) 的断言在 acc 白盒测试）
	const m = 10000
	g, _ := api.New(2 * m)
	for i := int64(0); i < m; i++ {
		_ = g.Apply("k", i, 1)
	}
	newOK := g.Apply("k", m, 1) == nil && g.Apply("k", m/2, 1) == nil // 新收、重跳
	g.Commit()
	check("large-m dedup behaves correctly", newOK && g.Checkpoint() == m && g.Sum("k") == m+1)

	// 并发 Apply：提交位点正确且读取单调不减
	const n = 64
	h, _ := api.New(2 * n)
	var violated atomic.Bool
	done := make(chan struct{})
	go func() {
		prev := int64(-1)
		for {
			select {
			case <-done:
				return
			default:
				if cp := h.Checkpoint(); cp < prev {
					violated.Store(true)
				} else {
					prev = cp
				}
			}
		}
	}()
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(off int64) { defer wg.Done(); _ = h.Apply("k", off, 1) }(int64((i * 31) % n))
	}
	wg.Wait()
	close(done)
	h.Commit()
	check("concurrent apply: cp correct & reads monotonic",
		!violated.Load() && h.Checkpoint() == n-1 && h.Sum("k") == n)

	check("api SelfCheck", h.SelfCheck() == nil)

	if failed {
		os.Exit(1)
	}
}
