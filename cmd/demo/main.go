// Command demo 核验双流水位对齐器；全部通过退出码 0，否则 1。
package main

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"sync"

	"ontology/align"
	"ontology/api"
	"ontology/wm"
)

var bad bool

func check(name string, ok bool) {
	s := "OK"
	if !ok {
		s, bad = "FAIL", true
	}
	fmt.Printf("%s: %s\n", name, s)
}

func ev(s byte, t int64) api.Event { return api.Event{Stream: s, TS: t} }

func main() {
	seq := []api.Event{ev('A', 1), ev('A', 2), ev('B', 1), ev('A', 3), ev('B', 2), ev('B', 5), ev('A', 4), ev('A', 2)}
	wantEmit := [][]api.Event{
		{}, {}, {ev('A', 1), ev('B', 1)}, {}, {ev('A', 2), ev('B', 2)}, {ev('A', 3)}, {ev('A', 4)}, {},
	}
	// wm 包级：推进、等于不算迟到、严格小于才迟到、Close 推 +inf。
	var w wm.Watermark
	okWM := w.Advance(3) && !w.Advance(2) && w.Advance(3) && w.Advance(4) && !w.Advance(3)
	w.Close()
	okWM = okWM && !w.Advance(1<<60) && w.IsLate(1<<60)
	// align 包级：八步逐步发出序列。
	al := align.New()
	okAlign := true
	for i, e := range seq {
		out, ok := al.Feed(e)
		if ok != (i != 7) || !slices.Equal(out, wantEmit[i]) {
			okAlign = false
		}
	}
	tail := al.Close()
	check("wm 推进/迟到 + align 八步发出", okWM && okAlign && len(tail) == 1 && tail[0] == ev('B', 5))
	// api 层八步：逐步判定 + 不提前发出。
	a := api.New()
	outs := make([][]api.Event, len(seq))
	var all []api.Event
	var m [2]int64
	var seen [2]bool
	okSteps, okPrem := true, true
	for i, e := range seq {
		out, err := a.Feed(e)
		outs[i] = out
		if err != nil || !slices.Equal(out, wantEmit[i]) {
			okSteps = false
		}
		k := int(e.Stream - 'A')
		seen[k], m[k] = true, max(m[k], e.TS)
		for _, o := range out {
			if !seen[0] || !seen[1] || o.TS > min(m[0], m[1]) {
				okPrem = false
			}
		}
		all = append(all, out...)
	}
	check("八步分步 wA/wB/W 与发出/缓冲/丢弃", okSteps && a.Dropped() == 1)
	check("步6发A3 / 步1缓冲 / 步8迟到", slices.Equal(outs[5], []api.Event{ev('A', 3)}) && len(outs[0]) == 0 && a.Dropped() == 1)
	tail2, err := a.Close()
	all = append(all, tail2...)
	want := []api.Event{ev('A', 1), ev('B', 1), ev('A', 2), ev('B', 2), ev('A', 3), ev('A', 4), ev('B', 5)}
	check("Close 后与朴素重排一致", err == nil && slices.Equal(all, want))
	nonDec := true
	for i := 1; i < len(all); i++ {
		nonDec = nonDec && all[i].TS >= all[i-1].TS
	}
	check("顺序非降 / 不提前发出", nonDec && okPrem)
	// 三类可判定错误。
	b := api.New()
	_, e1 := b.Feed(ev('X', 1))
	_, e2 := b.Feed(ev('A', -1))
	b.Close()
	_, e3 := b.Feed(ev('A', 1))
	_, e4 := b.Close()
	check("三类可判定错误互不相同", errors.Is(e1, api.ErrBadStream) && errors.Is(e2, api.ErrNegativeTS) &&
		errors.Is(e3, api.ErrClosed) && errors.Is(e4, api.ErrClosed) &&
		!errors.Is(e1, e2) && !errors.Is(e2, e3) && !errors.Is(e1, e3))
	// 被拒后状态不变且可继续用。
	c := api.New()
	c.Feed(ev('A', 2))
	v0, d0 := c.View(), c.Dropped()
	c.Feed(ev('X', 1))
	c.Feed(ev('A', -1))
	okState := slices.Equal(c.View(), v0) && c.Dropped() == d0
	_, errC := c.Feed(ev('B', 2))
	check("被拒后状态不变且可继续用", okState && errC == nil)
	// 大 m 滞留：只发出最小 TS 那一条（比较条数由 align 包内测试钉住）。
	d := api.New()
	for i := 0; i < 5000; i++ {
		d.Feed(ev('A', int64(10+i)))
	}
	out5, _ := d.Feed(ev('B', 5))
	check("大 m 滞留只发最小 TS", len(out5) == 1 && out5[0] == ev('B', 5))
	// 并发读同一个实例。
	f := api.New()
	for _, e := range seq {
		f.Feed(e)
	}
	f.Close()
	var wg sync.WaitGroup
	res := make(chan bool, 8)
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok := true
			for r := 0; r < 20; r++ {
				if !slices.Equal(f.View(), want) || f.SelfCheck() != nil {
					ok = false
				}
			}
			res <- ok
		}()
	}
	wg.Wait()
	close(res)
	okConc := true
	for ok := range res {
		okConc = okConc && ok
	}
	check("并发读 View/SelfCheck 一致", okConc)
	check("SelfCheck 四条不变量", api.New().SelfCheck() == nil)
	if bad {
		os.Exit(1)
	}
}
