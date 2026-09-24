package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/gw"
	"ontology/gwin"
)

var failed bool

func check(name string, ok bool) {
	if ok {
		fmt.Printf("OK   %s\n", name)
	} else {
		failed = true
		fmt.Printf("FAIL %s\n", name)
	}
}

func feedVals(ap *api.API, key string, vals []int64) bool {
	for _, v := range vals {
		if ap.Feed([]api.Event{{Key: key, Val: v}}) != nil {
			return false
		}
	}
	return true
}

func main() {
	vals := []int64{10, 20, 30, 40, 50, 60, 70, 80}

	// gw：累加后在第 3、6 步触发，触发不重置。
	var a gw.Acc
	firedAt := []int64{}
	for i, v := range vals {
		a.Add(v)
		if a.Fired(3) {
			firedAt = append(firedAt, int64(i+1))
		}
	}
	check("gw: fire at 3,6; no reset (sum=360,cnt=8)",
		a.Sum() == 360 && a.Cnt() == 8 && len(firedAt) == 2 && firedAt[0] == 3 && firedAt[1] == 6)

	// gwin+api：八步轨迹，第 3、6 步快照含触发元素且不重置。
	ap, err := api.New(3, 8)
	traceOK := err == nil && feedVals(ap, "k", vals)
	wantSum := []int64{10, 30, 60, 100, 150, 210, 280, 360}
	ap2, _ := api.New(3, 8)
	for i, v := range vals {
		traceOK = traceOK && ap2.Feed([]api.Event{{Key: "k", Val: v}}) == nil
		s, c := ap2.Totals("k")
		traceOK = traceOK && s == wantSum[i] && c == int64(i+1)
	}
	snaps := ap.Snapshots("k")
	check("8-step trace; snap3=(60,3) snap6=(210,6) cumulative", traceOK &&
		len(snaps) == 2 && snaps[0] == (api.Snapshot{Sum: 60, Cnt: 3}) &&
		snaps[1] == (api.Snapshot{Sum: 210, Cnt: 6}))

	// 快照与批量重算逐字段一致 + 单调递增。
	batchOK, monoOK, prev := true, true, int64(0)
	for i, s := range snaps {
		sum := int64(0)
		for j := 0; j < (i+1)*3; j++ {
			sum += vals[j]
		}
		batchOK = batchOK && s.Sum == sum && s.Cnt == int64(i+1)*3
		monoOK = monoOK && s.Cnt > prev
		prev = s.Cnt
	}
	check("snapshots match batch recompute", batchOK)
	check("snapshots strictly increasing", monoOK)

	// 三类可判定且互不相同的哨兵错误。
	_, errBadPeriod := api.New(0, 1)
	apE, _ := api.New(3, 8)
	errEmpty := apE.Feed([]api.Event{{Key: "", Val: 1}})
	apS, _ := api.New(3, 1)
	errSnap := apS.Feed([]api.Event{{Key: "k", Val: 1}, {Key: "k", Val: 2}, {Key: "k", Val: 3},
		{Key: "k", Val: 4}, {Key: "k", Val: 5}, {Key: "k", Val: 6}})
	check("3 distinct sentinel errors",
		errors.Is(errBadPeriod, api.ErrBadPeriod) && errors.Is(errEmpty, api.ErrEmptyKey) &&
			errors.Is(errSnap, api.ErrTooManySnaps) &&
			!errors.Is(errBadPeriod, api.ErrEmptyKey) && !errors.Is(errEmpty, api.ErrTooManySnaps))

	// 被拒后状态不变，且之后仍可正常使用。
	s0, c0 := apE.Totals("k")
	rej := apE.Feed([]api.Event{{Key: "k", Val: 7}, {Key: "", Val: 8}})
	s1, c1 := apE.Totals("k")
	usable := apE.Feed([]api.Event{{Key: "k", Val: 10}}) == nil
	s2, c2 := apE.Totals("k")
	check("rejected feed leaves state unchanged; still usable",
		errors.Is(rej, api.ErrEmptyKey) && s0 == s1 && c0 == c1 && usable && s2 == 10 && c2 == 1)

	// 大 m：period=m+1，第 m+1 个元素恰好触发，重读已累积元素数恒为 0。
	o1 := true
	for _, m := range []int64{100, 1000, 10000} {
		g, _ := gwin.NewManager(m+1, 1)
		evs := make([]gwin.Event, m+1)
		for i := range evs {
			evs[i] = gwin.Event{Key: "k", Val: int64(i)}
		}
		o1 = o1 && g.Apply(evs) == nil && g.SnapshotReadO1("k")
	}
	check("large-m trigger reads O(1), not O(m)", o1)

	// 并发：N 个 goroutine 各喂不同 Key，Totals 与单线程一致。
	apC, _ := api.New(5, 100)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			key := fmt.Sprintf("key-%d", g)
			for i := 0; i < 200; i++ {
				_ = apC.Feed([]api.Event{{Key: key, Val: int64(g*1000 + i)}})
			}
		}(g)
	}
	wg.Wait()
	concOK := true
	for g := 0; g < 8; g++ {
		sum := int64(0)
		for i := 0; i < 200; i++ {
			sum += int64(g*1000 + i)
		}
		s, c := apC.Totals(fmt.Sprintf("key-%d", g))
		concOK = concOK && s == sum && c == 200
	}
	check("concurrent feed: totals match single-threaded", concOK)

	// api 自检（四条不变量 + O(1)）。
	self, _ := api.New(3, 8)
	check("api.SelfCheck", self.SelfCheck() == nil)

	if failed {
		os.Exit(1)
	}
}
