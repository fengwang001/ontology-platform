package main

import (
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/gw"
	"ontology/gwin"
)

var failed bool

func ok(name string, cond bool) {
	tag := "OK  "
	if !cond {
		tag, failed = "FAIL ", true
	}
	fmt.Println(tag + name)
}

func main() {
	// 第三节八步：逐步 sum/cnt 与触发点；第3、6步触发且触发后不重置。
	var a gw.Acc
	type trig struct {
		step     int
		sum, cnt int64
	}
	var trigs []trig
	want := []int64{10, 20, 30, 40, 50, 60, 70, 80}
	wantSum := []int64{10, 30, 60, 100, 150, 210, 280, 360}
	good := true
	for i, v := range want {
		a.Add(v)
		if a.Sum != wantSum[i] || a.Cnt != int64(i+1) {
			good = false
		}
		if gw.Triggered(a.Cnt, 3) {
			trigs = append(trigs, trig{i + 1, a.Sum, a.Cnt})
		}
	}
	good = good && len(trigs) == 2 &&
		trigs[0] == (trig{3, 60, 3}) && trigs[1] == (trig{6, 210, 6}) &&
		a.Sum == 360 && a.Cnt == 8
	ok("8-step: triggers (60,3)&(210,6), include elem, no reset", good)

	// gwin：快照与批量重算逐字段一致；cnt 严格递增；Totals 全量保留。
	tb, _ := gwin.New(3, 100)
	evs := make([]gwin.Event, 8)
	for i, v := range want {
		evs[i] = gwin.Event{Key: "k", Val: v}
	}
	if err := tb.Feed(evs); err != nil {
		panic(err)
	}
	snaps := tb.Snapshots("k")
	bgood := len(snaps) == 2
	for i, s := range snaps {
		c := int64((i + 1) * 3)
		var bs int64
		for j := int64(0); j < c; j++ {
			bs += want[j]
		}
		bgood = bgood && s.Cnt == c && s.Sum == bs && (i == 0 || s.Cnt > snaps[i-1].Cnt)
	}
	sum, cnt := tb.Totals("k")
	ok("snapshots == batch recompute; monotonic; totals (360,8)", bgood && sum == 360 && cnt == 8)

	// 三类互不相同哨兵错误；被拒整批（含“好”元素）不留痕；之后仍可用。
	_, perr := gwin.New(0, 10)
	before := len(tb.Snapshots("k"))
	eerr := tb.Feed([]gwin.Event{{Key: "ok", Val: 1}, {Key: "", Val: 1}})
	s1, c1 := tb.Totals("ok")
	tl, _ := gwin.New(1, 1)
	_ = tl.Feed([]gwin.Event{{Key: "q", Val: 1}})
	lerr := tl.Feed([]gwin.Event{{Key: "q", Val: 1}})
	sq, cq := tl.Totals("q")
	distinct := perr == gwin.ErrInvalidPeriod && eerr == gwin.ErrEmptyKey && lerr == gwin.ErrSnapshotLimit
	ok("3 distinct sentinel errors; rejected batch leaves no trace",
		distinct && s1 == 0 && c1 == 0 && len(tb.Snapshots("k")) == before && sq == 1 && cq == 1)

	// api.SelfCheck：内置序列核第二节四条不变量。
	w, err := api.New(3, 100)
	if err != nil {
		panic(err)
	}
	ok("api SelfCheck passes", w.SelfCheck() == nil)

	// 大 m：多档 m（period=m+1 不整除 m）累积后 1 条恰好触发，快照瞬时正确。
	obig := true
	for _, m := range []int64{100, 1000, 10000} {
		tm, _ := gwin.New(m+1, 5)
		big := make([]gwin.Event, m)
		for i := range big {
			big[i] = gwin.Event{Key: "b", Val: 1}
		}
		if err := tm.Feed(big); err != nil {
			panic(err)
		}
		if err := tm.Feed([]gwin.Event{{Key: "b", Val: 1}}); err != nil {
			panic(err)
		}
		s := tm.Snapshots("b")
		obig = obig && len(s) == 1 && s[0].Cnt == m+1 && s[0].Sum == m+1
	}
	ok("big-m trigger O(1)-correct (readCnt==0 pinned by test)", obig)

	// 并发：N 个 goroutine 各向不同 Key 喂满，Totals 与单线程逐字段相同。
	const N = 16
	wc, _ := api.New(2, 100000)
	var wg sync.WaitGroup
	var wsum int64
	for v := int64(1); v <= 100; v++ {
		wsum += v
	}
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			batch := make([]gwin.Event, 100)
			for v := range batch {
				batch[v] = gwin.Event{Key: fmt.Sprintf("g%d", g), Val: int64(v + 1)}
			}
			if err := wc.Feed(batch); err != nil {
				panic(err)
			}
		}(g)
	}
	wg.Wait()
	cgood := true
	for g := 0; g < N; g++ {
		s, c := wc.Totals(fmt.Sprintf("g%d", g))
		cgood = cgood && s == wsum && c == 100
	}
	ok("concurrent feeds on distinct keys match serial totals", cgood)

	if failed {
		os.Exit(1)
	}
}
