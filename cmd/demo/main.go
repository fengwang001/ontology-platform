package main

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/drift"
	"ontology/wm"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
	}
	fmt.Println(map[bool]string{true: "OK  ", false: "FAIL "}[ok] + name)
}

func main() {
	// 1. 第三节八步：drift 包单源逐步核对 last 与分类。
	ws := []int64{100, 105, 120, 118, 117, 116, 130, 141}
	wantLast := []int64{100, 105, 120, 120, 120, 120, 130, 141}
	wantCls := []drift.Class{drift.Normal, drift.Normal, drift.Drift,
		drift.Reorder, drift.Reorder, drift.Rollback, drift.Normal, drift.Drift}
	s, ok := drift.NewSource(10, 3), true
	for i, w := range ws {
		got := s.Observe(w)
		last, _ := s.Last()
		ok = ok && got == wantCls[i] && last == wantLast[i]
	}
	check("eight-step last+class", ok)
	// 2. 第 5/6/7 步边界：d==tol 是 Reorder，d>tol 是 Rollback，w==last+thr 是 Normal。
	b := drift.NewSource(10, 3)
	b.Observe(120)
	c5, c6, c7 := b.Observe(117), b.Observe(116), b.Observe(130)
	check("step5/6/7 boundary", c5 == drift.Reorder && c6 == drift.Rollback && c7 == drift.Normal)

	det, err := api.New(10, 3)
	// 3+4. 水位线单调、分类与朴素重算一致（确定性伪随机序列，含回退）。
	mono, match := err == nil, err == nil
	var last, plast int64
	has := false
	for i := 0; i < 500; i++ {
		w := int64((i*i)%97 + i/3)
		cls, _ := det.Observe("m", w)
		cur, _ := det.Last("m")
		match = match && cls == naiveStep(&last, &has, w, 10, 3)
		mono = mono && cur >= plast
		plast = cur
	}
	check("watermark monotonic", mono)
	check("classify matches naive", match)
	// 5. 三计数守恒（wm 层八步：drift=2, reorder=2, rollback=1）。
	m, _ := wm.New(10, 3)
	for _, w := range ws {
		m.Observe("s", w)
	}
	d, r, rb := m.Counts()
	check("counts conserved", d == 2 && r == 2 && rb == 1 && d+r+rb == 5)
	// 6. 三类可判定错误互不相同 + 7. 被拒后状态不变。
	_, e1 := det.Observe("", 1)
	_, e2 := det.Observe("m", -1)
	_, e3 := api.New(0, 1)
	check("three distinct errors", errors.Is(e1, api.ErrEmptySource) &&
		errors.Is(e2, api.ErrNegativeMark) && errors.Is(e3, api.ErrInvalidThreshold) &&
		e1 != e2 && e2 != e3 && e1 != e3)
	b1, b2, b3 := det.Counts()
	lb, _ := det.Last("m")
	det.Observe("", 5)
	det.Observe("m", -5)
	a1, a2, a3 := det.Counts()
	la, _ := det.Last("m")
	check("reject leaves no trace", b1 == a1 && b2 == a2 && b3 == a3 && lb == la)
	// 8. 大 m 下读取历史个数恒为 1。
	ok = true
	for _, steps := range []int{100, 1000, 10000} {
		ok = ok && det.CheckConstantReads(steps)
	}
	check("O(1) reads==1 for large m", ok)
	check("concurrent counts", concurrentOK()) // 9. 并发计数正确且单调
	check("SelfCheck", det.SelfCheck() == nil) // 10. 四条不变量自检
	if failed {
		os.Exit(1)
	}
}

// naiveStep 是 demo 内独立的朴素单遍判定。
func naiveStep(last *int64, has *bool, w, thr, tol int64) api.Class {
	switch {
	case !*has:
		*has, *last = true, w
		return api.Normal
	case w > *last+thr:
		*last = w
		return api.Drift
	case w >= *last:
		*last = w
		return api.Normal
	case *last-w <= tol:
		return api.Reorder
	default:
		return api.Rollback
	}
}

// concurrentOK：8 个 goroutine 各喂一个源 0,100,99,90（各产 1 次 Drift/Reorder/Rollback），
// 期间并发读 Counts 总和必须单调不减；结束后每源 last=100、三计数各为 8。
func concurrentOK() bool {
	det, err := api.New(10, 3)
	if err != nil {
		return false
	}
	const n = 8
	var bad, stop atomic.Bool
	go func() { // 读侧：总和单调不减
		for prev := int64(-1); !stop.Load(); {
			if d, r, rb := det.Counts(); d+r+rb < prev {
				bad.Store(true)
			} else {
				prev = d + r + rb
			}
		}
	}()
	var wg sync.WaitGroup
	for g := 0; g < n; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for _, w := range []int64{0, 100, 99, 90} {
				if _, err := det.Observe(fmt.Sprintf("g%d", g), w); err != nil {
					bad.Store(true)
				}
			}
		}(g)
	}
	wg.Wait()
	stop.Store(true)
	for g := 0; g < n; g++ {
		if last, _ := det.Last(fmt.Sprintf("g%d", g)); last != 100 {
			return false
		}
	}
	d, r, rb := det.Counts()
	return !bad.Load() && d == n && r == n && rb == n
}
