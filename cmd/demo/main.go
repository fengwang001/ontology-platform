// Command demo runs the incremental 3-way join acceptance checks, printing OK/FAIL.
package main

import (
	"errors"
	"fmt"
	"maps"
	"ontology/api"
	"os"
	"sync"
)

func report(ok bool, m string) {
	if ok {
		fmt.Println("OK " + m)
	} else {
		fmt.Println("FAIL " + m)
		os.Exit(1)
	}
}
func must(e error) {
	if e != nil {
		panic(e)
	}
}
func ncopies(m map[api.Quad]int) (n int) {
	for _, c := range m {
		n += c
	}
	return
}

// brute independently batch-recomputes R⋈S⋈T from table multisets.
func brute(g map[string]map[api.Tuple]int) map[api.Quad]int {
	o := map[api.Quad]int{}
	for r, rc := range g["R"] {
		for s, sc := range g["S"] {
			if r.Y == s.X {
				for t, tc := range g["T"] {
					if s.Y == t.X {
						o[api.Quad{A: r.X, B: r.Y, C: s.Y, D: t.Y}] += rc * sc * tc
					}
				}
			}
		}
	}
	return o
}

type op struct {
	del  bool
	tab  string
	t    api.Tuple
	want int
}

func main() {
	steps := []op{
		{false, "S", api.Tuple{X: 1, Y: 10}, 0},
		{false, "T", api.Tuple{X: 10, Y: 100}, 0},
		{false, "R", api.Tuple{X: 5, Y: 1}, 1},
		{false, "S", api.Tuple{X: 1, Y: 10}, 2},
		{false, "R", api.Tuple{X: 6, Y: 1}, 4},
		{true, "R", api.Tuple{X: 5, Y: 1}, 2},
		{true, "S", api.Tuple{X: 1, Y: 10}, 1},
		{true, "S", api.Tuple{X: 1, Y: 10}, 0},
	}
	v := api.New(0)
	lens, states, ok := []int{}, []string{}, true
	for i, s := range steps {
		var e error
		if s.del {
			e = v.Delete(s.tab, s.t)
		} else {
			e = v.Insert(s.tab, s.t)
		}
		r := v.Result()
		lens = append(lens, ncopies(r))
		states = append(states, fmt.Sprintf("%d:%v", i+1, r))
		if e != nil || ncopies(r) != s.want {
			ok = false
		}
	}
	report(ok, fmt.Sprintf("八步条数%v 各步结果%v", lens, states))

	q5 := api.Quad{A: 5, B: 1, C: 10, D: 100}
	h := api.New(0)
	must(h.Insert("S", api.Tuple{X: 1, Y: 10}))
	must(h.Insert("T", api.Tuple{X: 10, Y: 100}))
	must(h.Insert("R", api.Tuple{X: 5, Y: 1}))
	must(h.Insert("S", api.Tuple{X: 1, Y: 10}))
	report(h.Result()[q5] == 2, "第4步重复S乘法放大 -> "+fmt.Sprint(h.Result()))
	must(h.Insert("R", api.Tuple{X: 6, Y: 1}))
	must(h.Delete("R", api.Tuple{X: 5, Y: 1}))
	report(ncopies(h.Result()) == 2 && h.Result()[q5] == 0, "第6步级联删除2条 -> "+fmt.Sprint(h.Result()))
	must(h.Delete("S", api.Tuple{X: 1, Y: 10}))
	must(h.Insert("R", api.Tuple{X: 5, Y: 1}))
	report(ncopies(h.Result()) == 2 && h.Result()[q5] == 1, "第7步删1条且S剩1份 -> "+fmt.Sprint(h.Result()))
	must(h.Delete("R", api.Tuple{X: 5, Y: 1}))

	w := api.New(0)
	g := map[string]map[api.Tuple]int{"R": {}, "S": {}, "T": {}}
	add := func(tab string, t api.Tuple, n int) {
		for range n {
			must(w.Insert(tab, t))
			g[tab][t]++
		}
	}
	add("S", api.Tuple{X: 1, Y: 10}, 3)
	add("T", api.Tuple{X: 10, Y: 100}, 2)
	add("R", api.Tuple{X: 5, Y: 1}, 4)
	report(w.Result()[q5] == 24 && maps.Equal(w.Result(), brute(g)), "多重集3x2x4=24 且 Result==批量重连")

	e1 := w.Insert("X", api.Tuple{})
	e2 := w.Delete("R", api.Tuple{X: 9, Y: 9})
	l := api.New(1)
	must(l.Insert("S", api.Tuple{X: 1, Y: 10}))
	must(l.Insert("T", api.Tuple{X: 10, Y: 100}))
	must(l.Insert("R", api.Tuple{X: 5, Y: 1}))
	e3 := l.Insert("R", api.Tuple{X: 6, Y: 1})
	distinct := errors.Is(e1, api.ErrBadTable) && errors.Is(e2, api.ErrNotFound) && errors.Is(e3, api.ErrLimit) &&
		!errors.Is(e1, e2) && !errors.Is(e1, e3) && !errors.Is(e2, e3)
	report(distinct, "三类哨兵错误可判定且互不相同")
	before := w.Result()
	_ = w.Insert("X", api.Tuple{})
	_ = w.Delete("R", api.Tuple{X: 9, Y: 9})
	_ = l.Insert("R", api.Tuple{X: 6, Y: 1})
	report(maps.Equal(w.Result(), before) && ncopies(l.Result()) == 1 &&
		l.Insert("R", api.Tuple{X: 7, Y: 9}) == nil, "被拒操作零留痕且之后仍可用")

	report(api.New(0).SelfCheck() == nil, "大m不相交候选对恒0(不随m增长)、k匹配<=k+1、内置八步对拍")

	const n = 200
	p, q := api.New(0), api.New(0)
	for _, z := range []*api.View{p, q} {
		must(z.Insert("S", api.Tuple{X: 1, Y: 1}))
		must(z.Insert("T", api.Tuple{X: 1, Y: 1}))
	}
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func(i int) { defer wg.Done(); _ = p.Insert("R", api.Tuple{X: i, Y: 1}) }(i)
	}
	wg.Wait()
	for i := range n {
		must(q.Insert("R", api.Tuple{X: i, Y: 1}))
	}
	report(maps.Equal(p.Result(), q.Result()), "并发插入结果与单线程逐四元组多重度一致")
}
