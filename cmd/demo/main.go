package main

import (
	"errors"
	"fmt"
	"os"

	"ontology/api"
	"ontology/cube"
	"ontology/dim"
)

func k(a, b, c string, am, bm, cm bool) dim.Key {
	return dim.Key{AVal: a, BVal: b, CVal: c, AAll: am, BAll: bm, CAll: cm}
}
func get(cs []cube.Cell, want dim.Key) (s int64) {
	for _, c := range cs {
		if c.Key == want {
			return c.Sum
		}
	}
	return 0
}

func main() {
	pass := true
	say := func(ok bool, f string, a ...any) {
		pass = pass && ok
		if ok {
			f = "OK " + f
		} else {
			f = "FAIL " + f
		}
		fmt.Printf(f+"\n", a...)
	}
	// dim：8 个 cell，level 分布恒为 1/3/3/1；ALL 哨兵区别于具体空串。
	ks := dim.Cells("a", "b", "c")
	var lv [4]int
	for _, x := range ks {
		lv[x.Level()]++
	}
	say(len(ks) == 8 && lv == [4]int{1, 3, 3, 1}, "dim: 8 cells, levels 1/3/3/1, ALL != \"\"")
	// 第三节五操作，逐步打印 T=(*,*,*) X=(a,*,*) Y=(*,b,c) Z=(a,b,c)。
	type op struct {
		f    [3]string
		v    int64
		rm   bool
		want [4]int64
	}
	ops := []op{
		{[3]string{"a", "b", "c"}, 2, false, [4]int64{2, 2, 2, 2}},
		{[3]string{"a", "b", "c"}, 3, false, [4]int64{5, 5, 5, 5}},
		{[3]string{"a", "d", "c"}, 5, false, [4]int64{10, 10, 5, 5}},
		{[3]string{"e", "b", "c"}, 7, false, [4]int64{17, 10, 12, 5}},
		{[3]string{"a", "b", "c"}, 2, true, [4]int64{15, 8, 10, 3}},
	}
	probe := [4]dim.Key{k("", "", "", true, true, true), k("a", "", "", false, true, true),
		k("", "b", "c", true, false, false), k("a", "b", "c", false, false, false)}
	cb, _ := cube.New(1_000_000)
	pc, _ := api.New(1_000_000)
	for i, o := range ops {
		f := api.Fact{A: o.f[0], B: o.f[1], C: o.f[2], V: o.v}
		if o.rm {
			_ = cb.Remove(o.f[0], o.f[1], o.f[2], o.v)
			_ = pc.Remove(f)
		} else {
			_ = cb.Add(o.f[0], o.f[1], o.f[2], o.v)
			_ = pc.Add(f)
		}
		var g [4]int64
		for j, key := range probe {
			g[j] = get(cb.View(), key)
		}
		say(g == o.want, "step%d: (*,*,*)=%d (a,*,*)=%d (*,b,c)=%d (a,b,c)=%d",
			i+1, g[0], g[1], g[2], g[3])
	}
	// 甲：(*,*,c)=15 (a,*,c)=8；乙：空串事实 T=19 (*,b,c)=14 ("",b,c)=4；丙：16->20。
	jc, ac := get(cb.View(), k("", "", "c", true, true, false)), get(cb.View(), k("a", "", "c", false, true, false))
	before := len(cb.View())
	_ = cb.Add("", "b", "c", 4)
	say(jc == 15 && ac == 8 && before == 16 && len(cb.View()) == 20 &&
		get(cb.View(), probe[0]) == 19 && get(cb.View(), probe[2]) == 14 &&
		get(cb.View(), k("", "b", "c", false, false, false)) == 4,
		"jia/yi/bing: (*,*,c)=15 (a,*,c)=8 absent-in-ROLLUP; empty fact 19/14 (\"\",b,c)=4; cells 16->20")
	// 三类互异哨兵错误；拒绝不留痕；api.SelfCheck。
	_, e0 := cube.New(0)
	small, _ := cube.New(8)
	_ = small.Add("a", "b", "c", 1)
	snap := len(small.View())
	e1, e2 := small.Add("p", "q", "r", 1), small.Remove("p", "q", "r", 1)
	bad := errors.Is(e0, cube.ErrInvalidMaxCells) && errors.Is(e1, cube.ErrCellLimit) &&
		errors.Is(e2, cube.ErrFactNotFound) && e1 != e2 && len(small.View()) == snap && pc.SelfCheck() == nil
	say(bad, "errors: 3 distinct sentinels, rejection leaves state intact; api.SelfCheck pass")
	// 大 m：全新事实触碰 8 键 = 7 新建非总计格 + 总计格 +1；Remove 逐格还原。
	ok8 := true
	for _, m := range []int{100, 1000, 10000} {
		big, _ := cube.New(m*8 + 100)
		for i := 0; i < m; i++ {
			_ = big.Add(fmt.Sprintf("a%d", i), fmt.Sprintf("b%d", i), fmt.Sprintf("c%d", i), 1)
		}
		old := map[dim.Key]int64{}
		for _, c := range big.View() {
			old[c.Key] = c.Sum
		}
		if big.Add("nA", "nB", "nC", 1) != nil {
			ok8 = false
			continue
		}
		tot := k("", "", "", true, true, true)
		nnew, nchg, newTot := 0, 0, old[tot]
		for _, c := range big.View() {
			if pv, ex := old[c.Key]; !ex {
				nnew++
			} else if pv != c.Sum {
				nchg, newTot = nchg+1, c.Sum
			}
		}
		ok8 = ok8 && nnew == 7 && nchg == 1 && newTot == old[tot]+1 &&
			big.Remove("nA", "nB", "nC", 1) == nil && len(big.View()) == len(old)
		for _, c := range big.View() {
			ok8 = ok8 && old[c.Key] == c.Sum
		}
	}
	say(ok8, "touched=8 (7 created + total +1) for m=100/1000/10000, independent of m")
	// 并发只读：8 goroutine 各自 View/Level/SelfCheck，逐 cell 必须一致。
	ref := pc.View()
	res := make(chan bool, 8)
	for g := 0; g < 8; g++ {
		go func() {
			ok := len(ref) == 16
			for r := 0; r < 50 && ok; r++ {
				v := pc.View()
				ok = len(v) == len(ref) && pc.SelfCheck() == nil
				for i := range v {
					ok = ok && v[i] == ref[i] && api.Level(v[i]) == v[i].Key.Level()
				}
			}
			res <- ok
		}()
	}
	conc := true
	for g := 0; g < 8; g++ {
		conc = conc && <-res
	}
	say(conc, "concurrent readers: identical View, race clean")
	if !pass {
		os.Exit(1)
	}
}
