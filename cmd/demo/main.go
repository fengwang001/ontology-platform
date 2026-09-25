// 演示程序：逐条打印 OK/FAIL，全部通过时退出码为 0。
package main

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"sort"
	"sync"
	"sync/atomic"

	"ontology/api"
)

var failed bool

func check(name string, ok bool) {
	s := "OK"
	if !ok {
		s, failed = "FAIL", true
	}
	fmt.Printf("%s: %s\n", name, s)
}

func ev(ts int64, id string) api.Event { return api.Event{TS: ts, ID: id} }

func out(id string, ts, outTS int64) api.Out { return api.Out{ID: id, TS: ts, OutTS: outTS} }

// naive 朴素重算：按 TS 非递减（并列按到达序）排序后逐条上钳。
func naive(evs []api.Event) []api.Out {
	s := append([]api.Event(nil), evs...)
	sort.SliceStable(s, func(i, j int) bool { return s[i].TS < s[j].TS })
	outs := make([]api.Out, 0, len(s))
	for i, e := range s {
		o := api.Out{ID: e.ID, TS: e.TS, OutTS: e.TS}
		if i > 0 && outs[i-1].OutTS > o.OutTS {
			o.OutTS = outs[i-1].OutTS
		}
		outs = append(outs, o)
	}
	return outs
}

func feedErr(delay int64, maxPending int, evs ...api.Event) error {
	eng, err := api.New(delay, maxPending)
	if err != nil {
		return err
	}
	_, err = eng.Feed(evs)
	return err
}

func main() {
	evs := []api.Event{ev(10, "a"), ev(7, "b"), ev(12, "c"), ev(9, "d"), ev(5, "e"), ev(11, "f"), ev(8, "g")}
	steps := [][]api.Out{{}, {out("b", 7, 7)}, {}, {out("d", 9, 9)}, {out("e", 5, 9)}, {}, {out("g", 8, 9)}}
	golden := []api.Out{out("b", 7, 7), out("d", 9, 9), out("e", 5, 9), out("g", 8, 9), out("a", 10, 10), out("f", 11, 11), out("c", 12, 12)}
	eng, err := api.New(3, 100)
	got := make([][]api.Out, len(evs))
	ok := err == nil
	for i, e := range evs { // 逐步喂入，核对每步发射
		got[i], err = eng.Feed([]api.Event{e})
		ok = ok && err == nil && slices.Equal(got[i], steps[i])
	}
	eng.Flush()
	check("1 七事件每步发射与outTS", ok && slices.Equal(eng.Output(), golden))
	check("2 第2步TS==wm即发射/第5步e上钳为9", slices.Equal(got[1], []api.Out{out("b", 7, 7)}) && slices.Equal(got[4], []api.Out{out("e", 5, 9)}))
	eng3, _ := api.New(3, 100) // 整批喂入同一组事件
	eng3.Feed(evs)
	eng3.Flush()
	check("3 Flush后与朴素排序+上钳一致", slices.Equal(eng3.Output(), naive(evs)))
	mono, seq := true, eng.Output()
	for i, o := range seq {
		mono = mono && o.OutTS >= o.TS && (i == 0 || o.OutTS >= seq[i-1].OutTS)
	}
	check("4 输出单调不减且outTS>=TS", mono)
	e1, e2 := feedErr(0, 2, ev(1, "x"), ev(2, "")), feedErr(0, 1, ev(1, "a"), ev(2, "b"))
	_, e3 := api.New(-1, 1)
	check("5 三类哨兵错误互不相同", errors.Is(e1, api.ErrEmptyID) && errors.Is(e2, api.ErrTooManyPending) &&
		errors.Is(e3, api.ErrNegativeDelay) && e1 != e2 && e2 != e3 && e1 != e3)
	eng2, _ := api.New(1, 2)
	eng2.Feed([]api.Event{ev(5, "k")})
	out0 := eng2.Output()
	wm0, _ := eng2.WM()
	eng2.Feed([]api.Event{ev(6, "")})
	eng2.Feed([]api.Event{ev(6, "x"), ev(7, "y"), ev(8, "z")})
	same := slices.Equal(out0, eng2.Output())
	wm1, _ := eng2.WM()
	_, err6 := eng2.Feed([]api.Event{ev(6, "ok")}) // 被拒后仍可正常使用
	check("6 被拒后状态不变且可继续用", same && wm0 == wm1 && err6 == nil)
	big, _ := api.New(3, 10000)
	many := make([]api.Event, 10000)
	for i := range many {
		many[i] = ev(10000, fmt.Sprintf("m%d", i))
	}
	em, err7 := big.Feed(many) // 全部 TS=10000 > wm=9997，零发射
	check("7 大m不可发射零发射(读取数见buf内测试)", err7 == nil && len(em) == 0 && len(big.Flush()) == 10000)
	want := eng.Output()
	var wg sync.WaitGroup
	var bad atomic.Bool
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !slices.Equal(eng.Output(), want) || eng.SelfCheck() != nil {
				bad.Store(true)
			}
		}()
	}
	wg.Wait()
	check("8 并发只读结果一致", !bad.Load())
	check("9 SelfCheck", eng.SelfCheck() == nil)
	if failed {
		os.Exit(1)
	}
}
