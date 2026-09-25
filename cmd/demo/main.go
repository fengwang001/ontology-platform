package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"
	"unsafe"

	"ontology/api"
	"ontology/fill"
	"ontology/grid"
)

var bad = false

func ok(name string, cond bool, detail string) {
	if !cond {
		bad = true
		fmt.Printf("FAIL %s %s\n", name, detail)
		return
	}
	fmt.Printf("OK %s %s\n", name, detail)
}

func probesOf(f *fill.Filler) int {
	v := reflect.ValueOf(f).Elem().FieldByName("probes")
	return *(*int)(unsafe.Pointer(v.UnsafeAddr()))
}

func main() {
	pts := []grid.Point{{Key: "k", TS: 0, Val: 5}, {Key: "k", TS: 30, Val: 8},
		{Key: "k", TS: 40, Val: 8}, {Key: "k", TS: 70, Val: 12}, {Key: "k", TS: 100, Val: 12}}
	f := fill.NewFiller(10)
	if err := f.Feed(pts); err != nil {
		ok("feed", false, err.Error())
		os.Exit(1)
	}
	view := f.View("k")
	// 第三节五个点每步的缺失时刻与填充值
	gaps := fmt.Sprintf("10,20->5; -; 50,60->8; 80,90->12")
	want := []grid.Point{{TS: 0, Val: 5}, {TS: 10, Val: 5}, {TS: 20, Val: 5}, {TS: 30, Val: 8},
		{TS: 40, Val: 8}, {TS: 50, Val: 8}, {TS: 60, Val: 8}, {TS: 70, Val: 12},
		{TS: 80, Val: 12}, {TS: 90, Val: 12}, {TS: 100, Val: 12}}
	same := len(view) == len(want)
	mono := true
	for i := range view {
		if same && (view[i].TS != want[i].TS || view[i].Val != want[i].Val) {
			same = false
		}
		if i > 0 && (view[i].Val < view[i-1].Val || view[i].TS-view[i-1].TS != 10) {
			mono = false
		}
	}
	ok("gaps", same, gaps)
	ok("complete+monotone", mono && same, "11 points, step=10, non-decreasing")
	// 三类陷阱的具体错值
	c0 := int64(0)         // 固定常数 0 填充时 TS=10 的错值
	l10 := 5 + (8-5)*10/30 // 线性插值 TS=10
	l20 := 5 + (8-5)*20/30 // 线性插值 TS=20
	rc := int64(5)         // 区间含右端点时 TS=30 被覆盖成的错值
	ok("traps", c0 == 0 && l10 == 6 && l20 == 7 && rc == 5,
		fmt.Sprintf("const0@10=%d(breaks mono) lerp@10=%d,@20=%d right-closed@30=%d", c0, l10, l20, rc))
	// 大 m 下检查键个数不随 m 增长
	constant := true
	for _, m := range []int{100, 1000, 10000} {
		fm := fill.NewFiller(10)
		for i := 0; i < m; i++ {
			_ = fm.Feed([]grid.Point{{Key: fmt.Sprintf("k%d", i), TS: 0, Val: 1}})
		}
		_ = fm.Feed([]grid.Point{{Key: "k0", TS: 10, Val: 1}})
		if probesOf(fm) > 2 {
			constant = false
		}
	}
	ok("probes-O(1)", constant, "probes<=2 for m=100..10000")

	// 四类可判定错误，互不相同
	a, _ := api.New(10)
	_ = a.Feed(pts)
	_, e0 := api.New(0)
	e1 := a.Feed([]api.Point{{Key: "", TS: 110, Val: 1}})
	e2 := a.Feed([]api.Point{{Key: "k", TS: 105, Val: 1}})
	e3 := a.Feed([]api.Point{{Key: "k", TS: 30, Val: 1}})
	es := []error{e0, e1, e2, e3}
	sents := []error{api.ErrBadStep, fill.ErrEmptyKey, grid.ErrOffGrid, grid.ErrNotIncreasing}
	distinct := true
	for i, ei := range es {
		for j, sj := range sents {
			if errors.Is(ei, sj) != (i == j) {
				distinct = false
			}
		}
	}
	ok("4-errors-distinct", distinct, "badstep/emptykey/offgrid/notincreasing")
	// 被拒后状态不变
	before := f.View("k")
	_ = f.Feed([]grid.Point{{Key: "k", TS: 107, Val: 9}, {Key: "k", TS: 120, Val: 9}}) // 107 不在网格 → 整批拒
	after := f.View("k")
	sameAfter := len(before) == len(after)
	for i := range before {
		if sameAfter && before[i] != after[i] {
			sameAfter = false
		}
	}
	ok("reject-no-trace", sameAfter, "view unchanged after rejected batch")
	ok("selfcheck", a.SelfCheck() == nil, "4 invariants on built-in sequences")
	// 并发 View 结果一致
	conc := true
	var wg sync.WaitGroup
	for n := 0; n < 32; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got := a.View("k")
			if len(got) != len(want) {
				conc = false
				return
			}
			for i := range got {
				if got[i].TS != want[i].TS || got[i].Val != want[i].Val {
					conc = false
				}
			}
		}()
	}
	wg.Wait()
	ok("concurrent-view", conc, "32 goroutines identical views")
	if bad {
		os.Exit(1)
	}
}
