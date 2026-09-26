// Command demo exercises the resource reservation scheduler.
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

func main() {
	fail := false
	ok := func(name string, cond bool, d string) {
		if !cond {
			fail = true
		}
		fmt.Printf("%s %s %s\n", map[bool]string{true: "OK", false: "FAIL"}[cond], name, d)
	}
	// 第三节八步（cap=10；编码 {-1,id}=释放，余为 {s,e,n,期望峰值,期望接受}）。
	var set res.Set
	act := map[int64]res.R{}
	var nid int64
	ops := [][5]int64{
		{0, 5, 4, 0, 1}, {5, 9, 7, 0, 1}, {2, 7, 5, 7, 0}, {0, 9, 1, 7, 1},
		{-1, 1, 0, 0, 0}, {2, 4, 9, 1, 1}, {0, 6, 2, 10, 0}, {-1, 2, 0, 0, 0},
	}
	d8, g8 := "", true
	for _, q := range ops {
		if q[0] == -1 {
			set.Remove(act[q[1]])
			delete(act, q[1])
			d8 += fmt.Sprintf("x%d ", q[1])
			continue
		}
		nid++
		pk := set.Peak(q[0], q[1])
		ac := pk+q[2] <= 10
		g8 = g8 && pk == q[3] && ac == (q[4] == 1)
		if ac {
			r := res.R{Start: q[0], End: q[1], Need: q[2], ID: nid}
			act[nid] = r
			set.Add(r)
		}
		d8 += fmt.Sprintf("%d:%c ", pk, map[bool]rune{true: 'A', false: 'R'}[ac])
	}
	ok("eight steps peak/decision", g8, d8)
	bm := true
	for _, m := range []int{100, 1000, 10000} {
		var big res.Set
		for i := 0; i < m; i++ {
			big.Add(res.R{Start: int64(4 * i), End: int64(4*i + 1), Need: 1, ID: int64(i + 1)})
		}
		big.Peak(202, 203)
		bm = bm && big.LastPeakCheckBounded(0)
	}
	ok("scan-count independent of m=100/1k/10k", bm, "bound=0")
	const C = int64(10)
	mg := rsv.NewManager(C)
	type rf struct{ s, e, n, id int64 }
	var rs []rf
	npk := func(a, b int64) (pk int64) {
		for x := a; x < b; x++ {
			var l int64
			for _, r := range rs {
				if r.s <= x && x < r.e {
					l += r.n
				}
			}
			pk = max(pk, l)
		}
		return
	}
	seed := int64(99)
	rnd := func(n int64) int64 { seed = (seed*1103515245 + 12345) & 0x7fffffff; return seed % n }
	nok := true
	for t := 0; t < 400; t++ {
		if len(rs) > 0 && rnd(3) == 0 {
			j := rnd(int64(len(rs)))
			nok = nok && mg.Release(rs[j].id) == nil
			rs = append(rs[:j], rs[j+1:]...)
			continue
		}
		a, b := rnd(20), rnd(20)
		if a > b {
			a, b = b, a
		}
		n := 1 + rnd(C)
		id, got, e := mg.Reserve(a, b, n)
		if e != nil {
			continue
		}
		nok = nok && got == (npk(a, b)+n <= C)
		if got {
			rs = append(rs, rf{a, b, n, id})
		}
	}
	ok("matches naive point-sweep (400 ops)", nok, "")
	mg2 := rsv.NewManager(5)
	mg2.Reserve(0, 4, 5)
	before := mg2.Active()
	_, _, en1 := mg2.Reserve(0, 1, 0)
	_, _, en2 := mg2.Reserve(0, 1, 6)
	_, _, ei := mg2.Reserve(3, 3, 1)
	er := mg2.Release(1 << 40)
	noTrace := mg2.Active() == before
	_, okA, _ := mg2.Reserve(4, 5, 5)
	distinct := en1 == en2 && errors.Is(en1, rsv.ErrNeed) && en1 != ei && en1 != er && ei != er
	ok("four distinct sentinels & rejection leaves no trace", distinct && noTrace && okA, "")
	_, ec := api.New(0)
	sc, _ := api.New(10)
	ok("api ErrConfig distinct & SelfCheck", ec != en1 && ec != ei && ec != er && sc.SelfCheck() == nil, "")
	a3, _ := api.New(6)
	fid, of, _ := a3.Reserve(0, 4, 6)
	_, ob, _ := a3.Reserve(0, 4, 1)
	a3.Release(fid)
	_, oar, _ := a3.Reserve(0, 4, 1)
	a2, _ := api.New(8)
	seed = 7
	cpOK := true
	for k := 0; k < 300; k++ {
		u, v := rnd(24), rnd(24)
		if u == v {
			continue
		}
		if u > v {
			u, v = v, u
		}
		a2.Reserve(u, v, 1+rnd(8))
		cpOK = cpOK && a2.Peak(0, 24) <= 8
	}
	ok("release invalidates & capacity never exceeded", of && !ob && oar && cpOK, "")
	const N = 64
	pc, _ := api.New(4)
	var wg sync.WaitGroup
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); pc.Reserve(int64(2*i), int64(2*i+1), 1) }(g)
	}
	wg.Wait()
	ok("concurrent reserves: Active==N, peak<=cap", pc.Active() == N && pc.Peak(0, 2*N) <= 4,
		fmt.Sprintf("active=%d", pc.Active()))
	if fail {
		os.Exit(1)
	}
}
