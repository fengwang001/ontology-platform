package main

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/seg"
	"ontology/union"
)

var failed bool

func check(name string, ok bool) {
	line := "OK"
	if !ok {
		line, failed = "FAIL", true
	}
	fmt.Printf("%s: %s\n", name, line)
}

func iv(s, e int64) api.Interval { return api.Interval{S: s, E: e} }

func main() {
	// seg：接触=重叠或邻接；拆分不产生空残留段。
	g1 := seg.Touches(seg.Seg{S: 0, E: 10}, seg.Seg{S: 10, E: 20}) &&
		!seg.Overlaps(seg.Seg{S: 0, E: 10}, seg.Seg{S: 10, E: 20})
	l, _, hl, hr := seg.Split(seg.Seg{S: 0, E: 20}, 15, 35)
	g2 := hl && !hr && l == (seg.Seg{S: 0, E: 15})
	_, _, hl3, hr3 := seg.Split(seg.Seg{S: 30, E: 40}, 15, 35)
	check("seg geometry+split", g1 && g2 && !hl3 && hr3)

	// union：邻接也合并；定位检查个数不随 m 增长。
	u := union.New(8)
	u.Add(0, 10)
	u.Add(10, 20)
	v := u.View()
	check("union adjacency merge", len(v) == 1 && v[0] == (union.Interval{S: 0, E: 20}))
	check("union locate O(1) in m", union.SelfCheck() == nil)

	// api：八步序列每步视图（含第2/4/5步判定）+ 日志前缀自洽。
	e := api.New(8)
	ops := [][3]int64{{0, 0, 10}, {0, 10, 20}, {0, 30, 40}, {1, 15, 35},
		{0, 15, 35}, {1, 20, 25}, {0, 20, 25}, {1, 0, 40}}
	want := [][]api.Interval{
		{iv(0, 10)}, {iv(0, 20)}, {iv(0, 20), iv(30, 40)}, {iv(0, 15), iv(35, 40)},
		{iv(0, 40)}, {iv(0, 20), iv(25, 40)}, {iv(0, 40)}, {},
	}
	okViews, okLog := true, true
	var down []api.Interval
	var log4s []api.Change
	for i, o := range ops {
		var lg []api.Change
		var err error
		if o[0] == 0 {
			lg, err = e.Add(o[1], o[2])
		} else {
			lg, err = e.Withdraw(o[1], o[2])
		}
		if err != nil || !slices.Equal(e.View(), want[i]) {
			okViews = false
		}
		for _, c := range lg { // 下游按前缀应用：每条 - 必须精确命中
			if !c.Del {
				down = append(down, c.I)
				continue
			}
			k := slices.Index(down, c.I)
			if k < 0 {
				okLog = false
			} else {
				down = slices.Delete(down, k, k+1)
			}
		}
		if i == 3 {
			log4s = lg
		}
	}
	check("api 8-step views(s2/s4/s5)", okViews)
	want4 := []api.Change{{Del: true, I: iv(0, 20)}, {I: iv(0, 15)}, {Del: true, I: iv(30, 40)}, {I: iv(35, 40)}}
	check("api step4 log exact", slices.Equal(log4s, want4))
	check("api changelog prefixes", okLog)

	// api：三类可判定错误互不相同，被拒后状态不变。
	e2 := api.New(1)
	e2.Add(0, 1)
	before := e2.View()
	_, errInv := e2.Add(5, 5)
	_, errMany := e2.Add(2, 3)
	_, errMiss := e2.Withdraw(10, 20)
	distinct := errors.Is(errInv, api.ErrInvalid) && errors.Is(errMany, api.ErrTooMany) &&
		errors.Is(errMiss, api.ErrNotFound) && !errors.Is(errInv, api.ErrTooMany) &&
		!errors.Is(errMany, api.ErrNotFound) && !errors.Is(errMiss, api.ErrInvalid)
	check("api 3 reject errors", distinct)
	check("api reject keeps state", slices.Equal(before, e2.View()))

	// api：SelfCheck 四条不变量；并发只读结果一致。
	check("api SelfCheck", api.New(8).SelfCheck() == nil)
	full := api.New(16)
	for k := 0; k < 10; k++ {
		full.Add(int64(10*k), int64(10*k+5))
	}
	wantV, same := full.View(), atomic.Bool{}
	same.Store(true)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for r := 0; r < 100; r++ {
				if !slices.Equal(full.View(), wantV) {
					same.Store(false)
				}
			}
		}()
	}
	wg.Wait()
	check("api concurrent readonly", same.Load())

	if failed {
		os.Exit(1)
	}
}
