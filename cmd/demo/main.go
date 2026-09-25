package main

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"sync"

	"ontology/api"
	"ontology/win"
)

var failed bool

func ok(name string, cond bool) {
	s := "OK   "
	if !cond {
		s, failed = "FAIL ", true
	}
	fmt.Println(s + name)
}
func ev(k string, ts int64, s byte) api.Event { return api.Event{Key: k, TS: ts, Side: s} }
func jn(k string, w, l, r int64) api.Join {
	return api.Join{Key: k, WindowStart: w, LeftTS: l, RightTS: r}
}
func must(w, d int64) *api.Joiner { j, _ := api.New(w, d); return j }

// bugSim replays evs with switchable bugs: full = re-emit complete L×R per arrival (甲); strict = late/close only when wm > end (乙).
func bugSim(evs []api.Event, w, delay int64, full, strict bool) (counts []int, retained int) {
	type bkt struct{ l, r []int64 }
	m := map[int64]*bkt{}
	wm := int64(-1) << 62
	for _, e := range evs {
		wm = max(wm, e.TS-delay)
		ww := win.Of(e.TS, w)
		if late := wm >= ww.End; late && !strict || strict && wm > ww.End {
			continue
		}
		g := m[ww.Start]
		if g == nil {
			g = &bkt{}
			m[ww.Start] = g
		}
		other, mine := &g.l, &g.r
		if e.Side == 'R' {
			other, mine = &g.r, &g.l
		}
		n := len(*other)
		*mine = append(*mine, e.TS)
		if full {
			n = len(g.l) * len(g.r)
		}
		counts = append(counts, n)
		for s := range m {
			if closed := wm >= s+w; closed && !strict || strict && wm > s+w {
				delete(m, s)
			}
		}
	}
	for _, g := range m {
		retained += len(g.l) + len(g.r)
	}
	return
}

// eqMS compares two join lists as multisets.
func eqMS(a, b []api.Join) bool {
	c := map[api.Join]int{}
	for _, j := range a {
		c[j]++
	}
	for _, j := range b {
		c[j]--
	}
	for _, v := range c {
		if v != 0 {
			return false
		}
	}
	return true
}
func main() {
	steps := []api.Event{ev("k", 5, 'L'), ev("k", 6, 'R'), ev("k", 12, 'L'), ev("k", 8, 'R'), ev("k", 2, 'L'), ev("k", 15, 'R')}
	j := must(10, 3)
	want := [][]api.Join{nil, {jn("k", 0, 5, 6)}, nil, {jn("k", 0, 5, 8)}, {jn("k", 0, 2, 6), jn("k", 0, 2, 8)}, {jn("k", 10, 12, 15)}}
	got := make([][]api.Join, 6)
	for i, e := range steps {
		got[i], _ = j.Feed([]api.Event{e})
	}
	ok("第三节六事件逐步 join 正确且总数=5", slices.EqualFunc(got, want, slices.Equal) && len(j.Joins()) == 5 && j.Retained() == 2)
	ok("负时间戳窗口归属: TS=-5,W=10 -> [-10,0)", win.Of(-5, 10) == win.Window{Start: -10, End: 0})
	c, _ := bugSim(steps, 10, 3, true, false)
	ok("(甲) 完整重放错成: 第5步=4 总数=8 (正确 2/5)", slices.Equal(c, []int{0, 1, 0, 2, 4, 1}))
	jb := must(10, 0)
	jb.Feed([]api.Event{ev("k", 5, 'L'), ev("k", 10, 'R'), ev("k", 5, 'R')})
	c2, sr := bugSim([]api.Event{ev("k", 5, 'L'), ev("k", 10, 'R'), ev("k", 5, 'R')}, 10, 0, false, true)
	ok("(乙) 严格大于: 错保留3条并多输出(0,5,5); 正确0条join", len(jb.Joins()) == 0 && jb.Dropped() == 1 && jb.Retained() == 1 && slices.Equal(c2, []int{0, 0, 1}) && sr == 3)
	out, _ := must(10, 0).Feed([]api.Event{ev("k", -5, 'L'), ev("k", 5, 'R')})
	ok("(丙) 负窗口错配: 正确0条; 截断法会把-5与5同窗", len(out) == 0 && -5/10*10 == 0 && win.Of(-5, 10).Start != win.Of(5, 10).Start)
	flushOK := true
	for _, s := range [][]api.Event{steps, {ev("a", -5, 'L'), ev("a", 5, 'R'), ev("b", -6, 'R'), ev("a", -4, 'R')}, {ev("x", 100, 'L'), ev("x", 50, 'R'), ev("x", 101, 'R')}} {
		jf := must(10, 3)
		jf.Feed(s)
		jf.Flush()
		flushOK = flushOK && eqMS(jf.Joins(), api.Batch(10, 3, s))
	}
	ok("Flush 后与批量叉积一致", flushOK)
	set := map[api.Join]bool{}
	for _, jn2 := range j.Joins() {
		set[jn2] = true
	}
	const a, b = 8, 8
	jpar := must(10, 1<<40)
	var wg sync.WaitGroup
	for i := 0; i < a+b; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			jpar.Feed([]api.Event{ev("c", int64(i%5), "LR"[i/a])})
			jpar.Joins()
			jpar.Retained()
		}(i)
	}
	wg.Wait()
	ok("无重复配对且并发叉积=a*b", len(set) == len(j.Joins()) && len(jpar.Joins()) == a*b)
	je := must(10, 3)
	je.Feed([]api.Event{ev("k", 5, 'L'), ev("k", 6, 'R')})
	_, e1 := api.New(0, 1)
	_, e2 := api.New(1, -1)
	_, e3 := je.Feed([]api.Event{ev("", 7, 'L')})
	_, e4 := je.Feed([]api.Event{ev("k", 7, 'Q')})
	distinct := errors.Is(e1, api.ErrBadWindow) && errors.Is(e2, api.ErrBadDelay) && errors.Is(e3, api.ErrEmptyKey) && errors.Is(e4, api.ErrBadSide)
	noTrace := len(je.Joins()) == 1 && je.Dropped() == 0 && je.Retained() == 2
	_, err := je.Feed([]api.Event{ev("k", 8, 'R')})
	ok("三类错误可判定互不相同、被拒不留痕", distinct && noTrace && err == nil && len(je.Joins()) == 2)
	const m = 10000
	big := make([]api.Event, m)
	for i := range big {
		big[i] = ev(fmt.Sprint(i), 0, 'L')
	}
	jm := must(10, 1<<40)
	jm.Feed(append(big, ev("victim", -10, 'L')))
	jm.Feed([]api.Event{ev("t", 1<<40, 'R')}) // wm=0, closes only [-10,0)
	ok("大m下清除按窗口结束时刻定位(计数器断言在wjoin包内测试)", jm.Retained() == m+1)
	ok("SelfCheck 通过", must(10, 3).SelfCheck() == nil)
	if failed {
		os.Exit(1)
	}
}
