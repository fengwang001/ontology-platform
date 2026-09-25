// Command demo exercises the change-log checksum service end to end.
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/cksum"
	"ontology/verify"
)

var failed bool

func check(name string, cond bool) {
	failed = failed || !cond
	fmt.Println(map[bool]string{true: "OK  ", false: "FAIL"}[cond], name)
}

// build makes an api.Log holding the six drill records (1,10)..(6,60).
func build() *api.Log {
	l, _ := api.New(3)
	for i := int64(1); i <= 6; i++ {
		_ = l.Append(i, i*10)
	}
	return l
}

// drill replays the eight-step table of NOTES.md on a verify.Log.
func drill() (ok16, ok78 bool) {
	l, _ := verify.NewLog(3)
	want := [][3]int64{{110, 0, 110}, {330, 0, 330}, {660, 0, 660},
		{660, 440, 1100}, {660, 990, 1650}, {660, 1650, 2310}}
	for i, w := range want { // steps 1-6: append and compare seg sums/total
		_ = l.Append(int64(i+1), int64(i+1)*10)
		s1, _ := l.SegSum(1)
		s2, _ := l.SegSum(2)
		if s1 != w[0] || s2 != w[1] || l.Total() != w[2] {
			return
		}
	}
	ok16 = true
	_ = l.Corrupt(4, 70)
	_ = l.Corrupt(5, 80)
	segs, total, err := l.Recompute(1, 6) // step 7
	if err != nil || len(segs) != 2 || segs[0] != 660 || segs[1] != 1710 || total != 2370 {
		return ok16, false
	}
	got, ok, err := l.Verify(1, 6) // step 8
	return ok16, err == nil && !ok && got == 4
}

// traps reproduces the three misimplementation traps of NOTES.md.
func traps() (a, b, c bool) {
	l := build() // (甲): only Seq=5 corrupted (50->80)
	_ = l.Corrupt(5, 80)
	got, ok, err := l.Verify(1, 5)
	_, halfOpen, err2 := l.Verify(1, 4) // what a [from,to) scan would see
	a = err == nil && !ok && got == 5 && err2 == nil && halfOpen
	var tot, seg2 int64 // (乙): e miswritten as Seq+Val
	for s := int64(1); s <= 6; s++ {
		tot += s + s*10
		if s >= 4 {
			seg2 += s + s*10
		}
	}
	l2 := build()
	s2, err3 := l2.SegSum(2)
	b = tot == 231 && seg2 == 165 && err3 == nil && l2.Total() == 2310 && s2 == 1650
	l3 := build() // (丙): two corruptions, ascending first is 4
	_ = l3.Corrupt(4, 70)
	_ = l3.Corrupt(5, 80)
	got, ok, err = l3.Verify(1, 6)
	c = err == nil && !ok && got == 4 // a descending scan would report 5
	return
}

// errorsIntact: three distinct sentinel errors; rejected ops change nothing.
func errorsIntact() bool {
	if _, err := api.New(0); !errors.Is(err, api.ErrBadSegSize) {
		return false
	}
	l := build()
	before := l.Total()
	gap := l.Append(9, 90)
	_, _, bad := l.Verify(0, 3)
	distinct := !errors.Is(api.ErrBadSegSize, api.ErrSeqGap) && !errors.Is(api.ErrSeqGap, api.ErrBadRange) && !errors.Is(api.ErrBadSegSize, api.ErrBadRange)
	s1, err := l.SegSum(1)
	return errors.Is(gap, api.ErrSeqGap) && errors.Is(bad, api.ErrBadRange) &&
		distinct && err == nil && s1 == 660 && l.Total() == before &&
		l.Append(7, 70) == nil && l.Total() == before+770
}

// bigN: small-range Verify stays correct as N grows; the "records checked
// does not grow with N" assertion lives in verify's internal test.
func bigN() bool {
	for _, n := range []int64{100, 1000, 10000} {
		l, _ := api.New(7)
		for s := int64(1); s <= n; s++ {
			_ = l.Append(s, s)
		}
		if _, ok, err := l.Verify(n-9, n); err != nil || !ok {
			return false
		}
	}
	return true
}

// concurrent: 64 goroutines Verify the same range on a log with one
// corrupted record; every one must report corrupt Seq=4.
func concurrent() bool {
	l := build()
	_ = l.Corrupt(4, 70)
	var wg sync.WaitGroup
	var bad atomic.Int64
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if got, ok, err := l.Verify(1, 6); err != nil || ok || got != 4 {
				bad.Add(1)
			}
		}()
	}
	wg.Wait()
	return bad.Load() == 0
}

func main() {
	check("cksum: e(1,10)=110 e(6,60)=660", cksum.Elem(1, 10) == 110 && cksum.Elem(6, 60) == 660)
	s16, s78 := drill()
	check("verify: 八步表步骤1-6 段和与总量", s16)
	check("verify: 步骤7重算seg2=1710 total=2370; 步骤8定位Seq=4", s78)
	a, b, c := traps()
	check("api: (甲) 闭区间[1,5]定位Seq=5; 半开会漏报ok=true", a)
	check("api: (乙) 错权重Seq+Val: total=231 seg2=165 (正确2310/1650)", b)
	check("api: (丙) 升序第一条=4; 降序会错报5", c)
	check("api: 三类哨兵错误互不相同且被拒后状态不变", errorsIntact())
	check("api: 大N验证正确(复杂度由verify内测钉住); 并发Verify一致", bigN() && concurrent())
	check("api: SelfCheck", api.SelfCheck() == nil)
	if failed {
		os.Exit(1)
	}
	fmt.Println("demo: all checks passed")
}
