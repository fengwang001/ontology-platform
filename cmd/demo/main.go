package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"
	"time"

	"ontology/agg"
	"ontology/api"
	"ontology/gset"
)

var fails int

func check(name string, ok bool) {
	if ok {
		fmt.Println("OK  " + name)
	} else {
		fails++
		fmt.Println("FAIL " + name)
	}
}

func mustG(d []int) gset.Group {
	g, ok := gset.Parse(d)
	if !ok {
		panic("bad dims")
	}
	return g
}

// eightStep 喂入第三节的八条事实，返回每步后组 () 的 sum 与终态引擎。
func eightStep() ([]int64, *agg.Engine) {
	e := agg.New([]gset.Group{mustG([]int{0}), mustG([]int{0, 1}), mustG(nil)})
	facts := []agg.Fact{
		{A: "x", B: "p", C: "u", M: 10}, {A: "x", B: "p", C: "v", M: 20},
		{A: "x", B: "q", C: "u", M: 5}, {A: "y", B: "p", C: "u", M: 7},
		{A: "x", B: "p", C: "v", M: -20}, {A: "y", B: "p", C: "u", M: 3},
		{A: "x", B: "p", C: "u", M: -10}, {A: "x", B: "q", C: "u", M: 2},
	}
	sums := make([]int64, 0, 8)
	for _, f := range facts {
		if err := e.Apply(f); err != nil {
			panic(err)
		}
		sums = append(sums, e.Snapshot()[7][""])
	}
	return sums, e
}

// hitCost 预填 m 个互异键后反复命中同一个已有键，返回单次 Apply 平均耗时。
func hitCost(m int) time.Duration {
	e := agg.New([]gset.Group{mustG([]int{0})})
	for i := 0; i < m; i++ {
		if err := e.Apply(agg.Fact{A: fmt.Sprintf("k%06d", i), B: "b", C: "c", M: 1000}); err != nil {
			panic(err)
		}
	}
	const iters = 3000
	start := time.Now()
	for i := 0; i < iters; i++ { // +1/-1 交替，键始终存活。
		d := int64(1)
		if i%2 == 1 {
			d = -1
		}
		if err := e.Apply(agg.Fact{A: "k000000", B: "b", C: "c", M: d}); err != nil {
			panic(err)
		}
	}
	return time.Since(start) / iters
}

func main() {
	gA, gAB, gN := mustG([]int{0}), mustG([]int{0, 1}), mustG(nil)
	check(fmt.Sprintf("group IDs (A)=%d (A,B)=%d ()=%d; GROUPING(C|A,B)=%d",
		gA.ID(), gAB.ID(), gN.ID(), gAB.Grouping(2)),
		gA.ID() == 3 && gAB.ID() == 1 && gN.ID() == 7 && gAB.Grouping(2) == 1)

	sums, e := eightStep()
	check(fmt.Sprintf("eight-step () sums=%v", sums),
		reflect.DeepEqual(sums, []int64{10, 30, 35, 42, 22, 25, 15, 17}))

	v := e.Snapshot() // 第 7 步 (x,p) 归零移除；第 8 步组 (A,B) live 条目恰为 2。
	_, alive := v[1][gAB.Key([3]string{"x", "p", "v"})]
	check(fmt.Sprintf("(x,p) zero-removal at step7=%v; live (A,B)=%d", !alive, len(v[1])),
		!alive && len(v[1]) == 2)

	c100, c10000 := hitCost(100), hitCost(10000)
	check(fmt.Sprintf("locate cost %v@100 vs %v@10000, no linear growth", c100, c10000),
		c100 > 0 && c10000 < 20*c100)

	const n = 16 // 并发只读：逐 (组,键) 一致，无 sleep 制造时序。
	got := make([]map[int]map[string]int64, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); <-start; got[i] = e.Snapshot() }(i)
	}
	close(start)
	wg.Wait()
	same := true
	for i := 1; i < n; i++ {
		same = same && reflect.DeepEqual(got[0], got[i])
	}
	check("16 concurrent readers see identical views", same)

	gv, err := api.New([][]int{{0}, {0, 1}, {}}) // 四类可判定错误 + 拒绝不留痕 + 仍可用。
	if err != nil {
		panic(err)
	}
	gv.Apply(api.Fact{A: "x", B: "p", C: "u", M: 10})
	distinct := api.ErrInvalidGroupingSets != api.ErrEmptyDimension &&
		api.ErrEmptyDimension != api.ErrZeroMeasure && api.ErrZeroMeasure != api.ErrNegativeResult
	rejected := true
	for _, g := range [][][]int{nil, {}, {{0}, {0}}, {{3}}} {
		rejected = rejected && errors.Is(func() error { _, e := api.New(g); return e }(), api.ErrInvalidGroupingSets)
	}
	for _, c := range []struct {
		f    api.Fact
		want error
	}{
		{api.Fact{A: "", B: "p", C: "u", M: 1}, api.ErrEmptyDimension},
		{api.Fact{A: "x", B: "p", C: "u", M: 0}, api.ErrZeroMeasure},
		{api.Fact{A: "x", B: "p", C: "v", M: -99}, api.ErrNegativeResult},
	} {
		before := gv.View()
		rejected = rejected && errors.Is(gv.Apply(c.f), c.want) && reflect.DeepEqual(gv.View(), before)
	}
	usable := gv.Apply(api.Fact{A: "x", B: "q", C: "u", M: 1}) == nil
	check("four distinct errors; rejected => state unchanged; reusable", distinct && rejected && usable)
	check("SelfCheck: batch-equivalence/completeness/non-negative/atomic", gv.SelfCheck() == nil)

	if fails > 0 {
		os.Exit(1)
	}
}
