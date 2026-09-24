// Command demo 逐条验证水位线漂移/回拨检测器，输出不超过 10 行。
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/drift"
	"ontology/wm"
)

var failed bool

func report(tag string, ok bool) {
	if !ok {
		failed = true
	}
	fmt.Printf("%s: %s\n", tag, map[bool]string{true: "OK", false: "FAIL"}[ok])
}

// naiveClass 是与实现解耦的朴素单遍判定，返回分类与推进后的 last。
func naiveClass(last int64, seen bool, w int64) (drift.Class, int64) {
	switch {
	case !seen:
		return drift.Normal, w
	case w > last+10:
		return drift.Drift, w
	case w >= last:
		return drift.Normal, w
	case last-w <= 3:
		return drift.Reorder, last
	default:
		return drift.Rollback, last
	}
}

func main() {
	ws := []int64{100, 105, 120, 118, 117, 116, 130, 141}
	wantClass := []drift.Class{drift.Normal, drift.Normal, drift.Drift, drift.Reorder,
		drift.Reorder, drift.Rollback, drift.Normal, drift.Drift}
	wantLast := []int64{100, 105, 120, 120, 120, 120, 130, 141}

	// 1) 第三节八步：逐步 last 与分类；第 5/6/7 步边界。
	st := drift.NewState(10, 3)
	ok8 := true
	for i, w := range ws {
		c := st.Observe(w)
		l, _ := st.Last()
		ok8 = ok8 && c == wantClass[i] && l == wantLast[i]
	}
	report("eight steps: per-step last & class; 5/6/7=Reorder/Rollback/Normal", ok8)

	// 2) 水位线单调：前六步含回拨到 116，last 必须仍是 120；130 恰为 +10 边界 => Normal。
	dm, _ := api.New(10, 3)
	for _, w := range ws[:6] {
		dm.Observe("s", w)
	}
	mc, _ := dm.Observe("s", 130)
	report("last monotonic: rejected rollback keeps last=120 (step 7 Normal)", mc == drift.Normal)

	// 3)+4) 分类与朴素重算逐条一致；每步三计数守恒。
	d, _ := api.New(10, 3)
	var nl, nd, nr, nrb int64
	seen := false
	naiveOK, consOK := true, true
	for _, w := range ws {
		nc, nlast := naiveClass(nl, seen, w)
		nl, seen = nlast, true
		switch nc {
		case drift.Drift:
			nd++
		case drift.Reorder:
			nr++
		case drift.Rollback:
			nrb++
		}
		got, _ := d.Observe("s", w)
		gd, gr, grb := d.Counts()
		naiveOK = naiveOK && got == nc
		consOK = consOK && gd == nd && gr == nr && grb == nrb && gd+gr+grb == nd+nr+nrb
	}
	report("class matches naive single-pass recomputation", naiveOK)
	report("three counters conserved at every step", consOK)

	// 5)+6) 三类错误互不相同且可判定；被拒后计数与 last 不变、仍可使用。
	distinct := !errors.Is(api.ErrInvalidThreshold, api.ErrEmptySource) &&
		!errors.Is(api.ErrEmptySource, api.ErrNegativeWatermark) &&
		!errors.Is(api.ErrInvalidThreshold, api.ErrNegativeWatermark)
	d2, _ := api.New(10, 3)
	d2.Observe("s", 100)
	c0d, c0r, c0b := d2.Counts()
	_, e1 := d2.Observe("", 5)
	_, e2 := d2.Observe("s", -1)
	_, e3 := api.New(0, -1)
	c1d, c1r, c1b := d2.Counts()
	probe, _ := d2.Observe("s", 110) // last 仍为 100：110 是 Normal 边界
	report("three distinct decidable sentinel errors", distinct &&
		errors.Is(e1, api.ErrEmptySource) && errors.Is(e2, api.ErrNegativeWatermark) && errors.Is(e3, api.ErrInvalidThreshold))
	report("rejected ops leave no trace; detector still usable",
		c0d == c1d && c0r == c1r && c0b == c1b && probe == drift.Normal)

	// 7) 大 m 下读取历史个数恒为 1。
	report("O(1): exactly 1 history read at m = 100/1000/10000", wm.VerifyObserveIsO1())

	// 8) 并发 Observe：结束后计数正确，且并发读者所见计数单调不减（不用 sleep）。
	mgr := wm.NewManager(10, 3)
	seq := []int64{0, 5, 20, 18, 17, 16, 30, 41}
	const n = 16
	var wg sync.WaitGroup
	done := make(chan struct{})
	mono := true
	go func() {
		var pd, pr, prb int64
		for {
			select {
			case <-done:
				return
			default:
				x, y, z := mgr.Counts()
				mono, pd, pr, prb = mono && x >= pd && y >= pr && z >= prb, x, y, z
			}
		}
	}()
	wg.Add(n)
	for g := 0; g < n; g++ {
		src := fmt.Sprintf("src-%d", g)
		go func() {
			defer wg.Done()
			for _, w := range seq {
				mgr.Observe(src, w)
			}
		}()
	}
	wg.Wait()
	close(done)
	x, y, z := mgr.Counts()
	report("concurrent observe: counts correct and non-decreasing",
		mono && x == int64(2*n) && y == int64(2*n) && z == int64(n))

	// 9) 内置自检（第二节四条不变量）。
	ds, _ := api.New(10, 3)
	report("SelfCheck passes", ds.SelfCheck() == nil)

	if failed {
		os.Exit(1)
	}
}
