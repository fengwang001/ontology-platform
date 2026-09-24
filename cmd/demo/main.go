package main

import (
	"fmt"
	"math/rand"
	"os"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/djoin"
	"ontology/rel"
)

var fails int

func ck(n string, ok bool) {
	fmt.Println(map[bool]string{true: "OK " + n, false: "FAIL " + n}[ok])
	if !ok {
		fails++
	}
}

type tk struct {
	k    int64
	a, b string
}

func rejoin(r, s *rel.Table) map[tk]int64 { // 朴素全量重算 R⋈S
	o := map[tk]int64{}
	for _, x := range r.Snapshot() {
		s.Match(x.K, func(b string, m int64) { o[tk{x.K, x.V, b}] += x.Mult * m })
	}
	return o
}
func gen(rng *rand.Rand, t *rel.Table, vs [3]string) (out []api.Row) { // 随机可接受变更（一半按模型删）
	for i := 0; i < 1+rng.Intn(3); i++ {
		k, v, sg := int64(rng.Intn(6)+1), vs[rng.Intn(3)], 1
		if sp := t.Snapshot(); len(sp) > 0 && rng.Intn(2) == 0 {
			x := sp[rng.Intn(len(sp))]
			k, v, sg = x.K, x.V, -1
		}
		t.Add(k, v, int64(sg))
		out = append(out, api.Row{K: k, V: v, Sign: sg})
	}
	return
}
func goRun(wg *sync.WaitGroup, f func()) {
	wg.Add(1)
	go func() { defer wg.Done(); f() }()
}

func main() {
	R := func(k int64, v string, s int) rel.Row { return rel.Row{K: k, V: v, Sign: s} }
	A := func(k int64, v string, s int) api.Row { return api.Row{K: k, V: v, Sign: s} }
	t := rel.New()
	t.Add(1, "x", 2)
	n := 0
	t.Match(1, func(string, int64) { n++ })
	t.Add(1, "x", -2)
	ck("rel: 增减/归0即删/按K匹配", n == 1 && t.Mult(1, "x") == 0)
	e := djoin.New(100) // 第三节三批
	d1, _ := e.Feed([]rel.Row{R(1, "x", 1), R(2, "y", 1)}, []rel.Row{R(1, "p", 1), R(2, "q", 1)})
	d2, _ := e.Feed([]rel.Row{R(1, "z", 1), R(2, "y", -1)}, []rel.Row{R(1, "r", 1), R(2, "q", 1)})
	d3, _ := e.Feed([]rel.Row{R(1, "x", -1)}, []rel.Row{R(1, "r", -1)})
	ck("djoin: 三批输出差分与九行表完全一致",
		fmt.Sprint(d1) == "[{1 x p 1} {2 y q 1}]" &&
			fmt.Sprint(d2) == "[{1 x r 1} {1 z p 1} {1 z r 1} {2 y q -1}]" &&
			fmt.Sprint(d3) == "[{1 x p -1} {1 x r -1} {1 z r -1}]")
	ck("djoin: 批后视图={(1,z,p):1} 且 SelfCheck 通过",
		fmt.Sprint(e.View()) == "[{1 z p 1}]" && e.SelfCheck() == nil)
	mr, ms := rel.New(), rel.New() // 随机批次
	rng, j0, okR := rand.New(rand.NewSource(1)), api.New(100000), true
	for b := 0; b < 200; b++ {
		old := rejoin(mr, ms)
		got, err := j0.Feed(gen(rng, mr, [3]string{"a", "b", "c"}), gen(rng, ms, [3]string{"p", "q", "r"}))
		okR = okR && err == nil
		nw, gm := rejoin(mr, ms), map[tk]int64{}
		for _, d := range got {
			gm[tk{d.K, d.A, d.B}] = d.Mult
		}
		for x, m := range nw {
			okR = okR && gm[x] == m-old[x] && m > 0
		}
		for x, m := range old {
			if _, p := nw[x]; !p {
				okR = okR && gm[x] == -m
			}
		}
		okR = okR && len(j0.View()) == len(nw)
	}
	ck("api: 随机批次 差分=求差/视图=重算/多重性非负", okR)
	_, e1 := api.New(100).Feed(nil, []api.Row{A(1, "p", -1)})
	_, e2 := api.New(100).Feed([]api.Row{A(1, "x", 0)}, nil)
	_, e2b := api.New(100).Feed(nil, []api.Row{A(1, "", 1)})
	jl := api.New(1)
	jl.Feed([]api.Row{A(1, "a", 1)}, []api.Row{A(1, "p", 1)})
	_, e3 := jl.Feed([]api.Row{A(2, "a", 1)}, []api.Row{A(2, "p", 1)})
	ck("api: 三类哨兵错误可判定且互不相同", e1 == api.ErrDeleteMissing &&
		e2 == api.ErrInvalidChange && e2b == api.ErrInvalidChange && e3 == api.ErrViewLimit)
	ja := api.New(100) // 失败不留痕（同批另一表也不生效），之后仍可用
	ja.Feed([]api.Row{A(1, "x", 1)}, []api.Row{A(1, "p", 1)})
	_, f1 := ja.Feed([]api.Row{A(9, "z", -1)}, []api.Row{A(9, "q", 1)})
	_, f2 := ja.Feed([]api.Row{A(9, "z", 1)}, []api.Row{A(9, "", 1)})
	g, fg := ja.Feed([]api.Row{A(1, "y", 1)}, []api.Row{A(1, "q", 1)})
	ck("api: 被拒整批不留痕、之后可继续", f1 == api.ErrDeleteMissing && f2 == api.ErrInvalidChange &&
		fg == nil && len(g) == 3 && len(ja.View()) == 4)
	okB := true // 大 m：单条 dR 在 S 中恰匹配 1 行（计数器断言见包内测试）
	for _, m := range []int{100, 1000, 10000} {
		jb := api.New(m*2 + 10)
		dR, dS := make([]api.Row, 0, m), make([]api.Row, 0, m)
		for i := 1; i <= m; i++ {
			dR = append(dR, A(int64(i), "a", 1))
			dS = append(dS, A(int64(i), "p", 1))
		}
		jb.Feed(dR, dS)
		x, err := jb.Feed([]api.Row{A(42, "n", 1)}, nil)
		okB = okB && err == nil && len(x) == 1 && x[0].Mult == 1
	}
	ck("api: 大m下单条dR只产1条差分(按K索引非扫描)", okB)
	N, jc := 64, api.New(100000) // 并发：K 不交，读者只见整批
	var rok, stop atomic.Bool
	rok.Store(true)
	var wg, rg sync.WaitGroup
	rg.Add(1)
	go func() {
		defer rg.Done()
		for !stop.Load() {
			for _, d := range jc.View() {
				if d.Mult != 1 || d.A != "a" || d.B != "p" {
					rok.Store(false)
				}
			}
		}
	}()
	for i := 0; i < N; i++ {
		k := int64(1000 + i)
		goRun(&wg, func() { jc.Feed([]api.Row{A(k, "a", 1)}, []api.Row{A(k, "p", 1)}) })
	}
	wg.Wait()
	stop.Store(true)
	rg.Wait()
	ck("api: N路并发喂入视图=全量重算、无半批/负值", rok.Load() && len(jc.View()) == N)
	if fails > 0 {
		os.Exit(1)
	}
}
