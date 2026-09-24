// Command demo prints OK/FAIL judgments; no args, no network, exit 0 iff all pass.
package main

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"sync"

	"ontology/api"
	"ontology/mhash"
	"ontology/mtree"
)

func ok(b bool) string { return map[bool]string{true: "OK", false: "FAIL"}[b] }
func must(f, d, mk int) *api.Replica {
	r, e := api.New(f, d, mk)
	if e != nil {
		panic(e)
	}
	return r
}
func tnew(f, d int) *mtree.Tree { t, _ := mtree.New(f, d); return t }
func load(r *api.Replica, m map[int64]int64) {
	o := make([]api.Op, 0, len(m))
	for k, v := range m {
		o = append(o, api.Op{Key: k, Val: v})
	}
	if e := r.Apply(o); e != nil {
		panic(e)
	}
}
func tload(t *mtree.Tree, m map[int64]int64) {
	o := make([]mtree.Op, 0, len(m))
	for k, v := range m {
		o = append(o, mtree.Op{Key: k, Val: v})
	}
	if e := t.Batch(o, 1<<20); e != nil {
		panic(e)
	}
}

func main() {
	chk := func(s string, b bool) {
		fmt.Printf("%s ... %s\n", s, ok(b))
		if !b {
			os.Exit(1)
		}
	}
	// 1 rules + value-0 distinguishable
	dist := true
	for k := int64(0); k < 1<<20 && dist; k += 997 {
		dist = mhash.LeafHash(k, 0) != 0
	}
	chk("1 rules & value-0 distinguishable", dist &&
		mhash.LeafHash(1, 5) == 1000165 && mhash.LeafHash(2, 5) == 2000168 && mhash.LeafHash(4, 0) == 4000019)
	ta, tb := tnew(4, 2), tnew(4, 2) // 2 section-3 exact table
	tload(ta, map[int64]int64{1: 5, 2: 5, 4: 0, 9: 7, 13: 3})
	tload(tb, map[int64]int64{1: 6, 2: 4, 9: 7, 13: 8})
	dk, cmp, _ := ta.Diff(tb)
	rows := [][4]int64{{0, 16, 322749291, 477521251}, {0, 4, 834376390, 989159576},
		{0, 1, 0, 0}, {1, 2, 1000165, 1000196}, {2, 3, 2000168, 2000137}, {3, 4, 0, 0},
		{4, 8, 477635560, 0}, {4, 5, 4000019, 0}, {5, 6, 0, 0}, {6, 7, 0, 0}, {7, 8, 0, 0},
		{8, 12, 247453045, 247453045}, {12, 16, 612068240, 540984628},
		{12, 13, 0, 0}, {13, 14, 13000139, 13000294}, {14, 15, 0, 0}, {15, 16, 0, 0}}
	g := len(cmp) == 17 && fmt.Sprint(dk) == "[1 2 4 13]"
	for i, x := range rows {
		ha, _ := ta.Hash(x[0], x[1])
		hb, _ := tb.Hash(x[0], x[1])
		g = g && cmp[i] == mtree.Range{Lo: x[0], Hi: x[1]} && ha == x[2] && hb == x[3]
	}
	chk("2 section-3 roots/17 ranges/diff", g)
	rng := rand.New(rand.NewSource(1)) // 3 random naive parity
	par := true
	for t := 0; t < 20; t++ {
		x, y := must(4, 3, 200), must(4, 3, 200)
		xm, ym := map[int64]int64{}, map[int64]int64{}
		for _, m := range []map[int64]int64{xm, ym} {
			for j := 0; j < 30; j++ {
				m[rng.Int63n(64)] = rng.Int63n(21) - 10
			}
		}
		load(x, xm)
		load(y, ym)
		ks, _, e := x.Diff(y)
		want := []int64{}
		for k := int64(0); k < 64; k++ {
			vx, ox := xm[k]
			vy, oy := ym[k]
			if ox != oy || ox && vx != vy {
				want = append(want, k)
			}
		}
		par = par && e == nil && fmt.Sprint(ks) == fmt.Sprint(want)
	}
	chk("3 random parity with naive compare", par)
	chk("4 SelfCheck four invariants", must(4, 2, 10).SelfCheck() == nil)
	_, ep := api.New(1, 1, 0) // 5 four distinct sentinels
	ek := must(2, 2, 10).Apply([]api.Op{{Key: 9, Val: 1}})
	em := must(4, 2, 1)
	load(em, map[int64]int64{0: 0})
	et := em.Apply([]api.Op{{Key: 1}})
	_, _, es := must(4, 2, 10).Diff(must(8, 2, 10))
	chk("5 four distinct sentinel errors", errors.Is(ep, api.ErrParams) && errors.Is(ek, api.ErrKeyRange) &&
		errors.Is(et, api.ErrTooMany) && errors.Is(es, api.ErrShape) && ep != ek && ek != et && et != es && ep != es)
	a := must(4, 2, 100) // 6 rejected batch leaves no trace
	load(a, map[int64]int64{1: 5, 2: 5})
	tr := true
	for _, bad := range [][]api.Op{{{Key: -1, Val: 1}, {Key: 5, Val: 5}}, {{Key: 1, Val: 1}, {Key: 99}}} {
		root := a.RootHash()
		tr = tr && a.Apply(bad) != nil && a.RootHash() == root
	}
	_, left, _ := a.Get(5)
	chk("6 rejected batch leaves no trace", tr && !left && a.Apply([]api.Op{{Key: 5, Val: 5}}) == nil)
	cost := true // 7 compared ranges bounded independently of m
	for _, m := range []int{100, 1000, 10000} {
		x, y := must(4, 8, 20000), must(4, 8, 20000)
		mm := map[int64]int64{}
		for j := 0; j < m; j++ {
			mm[int64(j*61%65536)] = int64(j)
		}
		load(x, mm)
		load(y, mm)
		_ = y.Apply([]api.Op{{Key: 61, Val: -1}})
		_, c, e := x.Diff(y)
		cost = cost && e == nil && len(c) <= 4*(1+4*8)
	}
	chk("7 diff cost independent of m", cost)
	x, y := must(4, 4, 20000), must(4, 4, 20000) // 8 concurrent identical
	mm := map[int64]int64{}
	for j := 0; j < 500; j++ {
		mm[int64(j*37%256)] = int64(j)
	}
	load(x, mm)
	load(y, mm)
	_ = y.Apply([]api.Op{{Key: 7, Val: -7}, {Del: true, Key: 11}})
	var wg sync.WaitGroup
	res := make([]string, 8)
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) { defer wg.Done(); ks, c, e := x.Diff(y); res[g] = fmt.Sprint(ks, c, e) }(g)
	}
	wg.Wait()
	same := true
	for _, s := range res[1:] {
		same = same && s == res[0]
	}
	chk("8 concurrent diffs identical", same)
}
