package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/res"
	"ontology/rsv"
)

func ok(name string, cond bool) {
	if cond {
		fmt.Println(name, "OK")
	} else {
		fmt.Println(name, "FAIL")
		os.Exit(1)
	}
}

type niv struct{ s, e, n int64 }

func loadAt(ivs []niv, x int64) int64 {
	var sum int64
	for _, iv := range ivs {
		if iv.s <= x && x < iv.e {
			sum += iv.n
		}
	}
	return sum
}

func main() {
	// 1. 第三节八步：峰值 + 接受/拒绝（res.Core 镜像取峰值，rsv.Mgr 判定）
	c := res.NewCore(10)
	m := rsv.NewMgr(10)
	type op struct {
		s, e, need, rel, wantPeak int64
		wantOK                    bool
	}
	steps := []op{
		{0, 5, 4, 0, 0, true}, {5, 9, 7, 0, 0, true}, {2, 7, 5, 0, 7, false}, {0, 9, 1, 0, 7, true},
		{rel: 1}, {2, 4, 9, 0, 1, true}, {0, 6, 2, 0, 10, false}, {rel: 2},
	}
	byID, good := map[int64]res.Interval{}, true
	for _, o := range steps {
		if o.rel > 0 {
			good = good && m.Release(o.rel) == nil
			iv := byID[o.rel]
			c.Remove(iv.Start, iv.End, iv.Need)
			continue
		}
		good = good && c.Peak(o.s, o.e) == o.wantPeak
		id, got, err := m.Reserve(o.s, o.e, o.need)
		good = good && err == nil && got == o.wantOK
		if got {
			byID[id] = res.Interval{Start: o.s, End: o.e, Need: o.need}
			c.Add(o.s, o.e, o.need)
		}
	}
	ok("steps: p0/A p0/A p7/R p7/A rel p1/A p10/R rel", good && m.Active() == 2)

	// 2+3. 与朴素参照一致 & 容量不越界：确定性序列对照
	sc, _ := api.New(8)
	live, liveIDs := []niv{}, []int64{}
	consistent, bounded := true, true
	seed := uint64(1)
	rnd := func(n int64) int64 {
		seed = seed*6364136223846793005 + 1442695040888963407
		return int64(seed>>33) % n
	}
	for i := 0; i < 200; i++ {
		a, b := rnd(20), rnd(20)
		if a > b {
			a, b = b, a
		}
		b++ // 保证 b > a
		need := 1 + rnd(8)
		id, got, err := sc.Reserve(a, b, need)
		want := true
		for x := a; x < b; x++ {
			if loadAt(live, x)+need > 8 {
				want = false
			}
		}
		consistent = consistent && err == nil && got == want
		if got {
			live = append(live, niv{a, b, need})
			liveIDs = append(liveIDs, id)
			for x := int64(0); x < 21; x++ {
				bounded = bounded && loadAt(live, x) <= 8
			}
		}
		if i%5 == 4 && len(liveIDs) > 0 {
			sc.Release(liveIDs[0])
			liveIDs, live = liveIDs[1:], live[1:]
		}
	}
	ok("naive-consistent", consistent)
	ok("capacity-bounded", bounded)

	// 4. 释放即失效
	sc2, _ := api.New(5)
	id1, _, _ := sc2.Reserve(0, 10, 5)
	sc2.Release(id1)
	_, got2, _ := sc2.Reserve(0, 10, 5)
	ok("release-invalidates", got2 && sc2.Active() == 1)

	// 5. 四类可判定错误互不相同
	_, errCap := api.New(0)
	_, _, errNeed := sc2.Reserve(0, 1, 0)
	_, _, errRange := sc2.Reserve(3, 3, 1)
	errID := sc2.Release(9999)
	distinct := !errors.Is(errCap, errNeed) && !errors.Is(errCap, errRange) && !errors.Is(errCap, errID) &&
		!errors.Is(errNeed, errRange) && !errors.Is(errNeed, errID) && !errors.Is(errRange, errID)
	ok("4-errors", errors.Is(errCap, api.ErrCapacity) && errors.Is(errNeed, api.ErrNeed) &&
		errors.Is(errRange, api.ErrRange) && errors.Is(errID, api.ErrNoID) && distinct)

	// 6. 被拒后状态不变且可继续用
	before := sc2.Active()
	sc2.Reserve(0, 1, 99)
	sc2.Reserve(5, 5, 1)
	sc2.Release(9999)
	_, ok3, _ := sc2.Reserve(10, 12, 1)
	ok("rejected-no-trace", sc2.Active() == before+1 && ok3)

	// 7. 大 m 下检查个数不随 m 增长（断言在 TestCheckedBounded，这里跑通场景）
	big, _ := api.New(1 << 40)
	for i := int64(0); i < 10000; i++ {
		big.Reserve(i*4, i*4+2, 1)
	}
	_, gapOK, _ := big.Reserve(2, 3, 1)
	ok("large-m bounded checks", gapOK)

	conc, _ := api.New(1 << 40)
	const N = 64
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int64) {
			defer wg.Done()
			conc.Reserve(i*4, i*4+2, 1)
		}(int64(i))
	}
	wg.Wait()
	ok("concurrent-reserve", conc.Active() == N)
	ok("selfcheck", sc.SelfCheck() == nil)
}
