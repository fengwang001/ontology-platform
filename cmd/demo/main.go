package main

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
	"os"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/gk"
	"ontology/quant"
)

var fails int

func ok(name string, cond bool, detail string) {
	if !cond {
		fails++
	}
	fmt.Printf("%s %s %s\n", map[bool]string{true: "OK", false: "FAIL"}[cond], name, detail)
}

func show(s *gk.Summary) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, t := range s.Tuples() {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "(%d,%d,%d)", t.V, t.G, t.Delta)
	}
	return b.String() + "]"
}

func main() {
	s := gk.New(0.25) // 第三节六步：每步之后的摘要
	var got []string
	for _, op := range []func(){func() { s.Insert(10) }, func() { s.Insert(20) },
		func() { s.Insert(30) }, func() { s.Insert(40) }, s.Compress, func() { s.Insert(25) }} {
		op()
		got = append(got, show(s))
	}
	want := []string{"[(10,1,0)]", "[(10,1,0),(20,1,0)]", "[(10,1,0),(20,1,0),(30,1,0)]",
		"[(10,1,0),(20,1,0),(30,1,0),(40,1,0)]", "[(10,1,0),(20,1,0),(40,2,0)]", "[(10,1,0),(20,1,0),(25,1,1),(40,2,0)]"}
	ok("six-steps", reflect.DeepEqual(got, want), strings.Join(got, " "))

	b := gk.New(0.05) // 带宽不变：长流上每次插入+压缩后都成立
	band := true
	for i := 0; i < 5000; i++ {
		b.Insert(int64(i*7919) % 50021)
		b.Compress()
		band = band && b.BandOK()
	}
	ok("band-invariant", band, fmt.Sprintf("tuples=%d n=%d", len(b.Tuples()), b.N()))

	q := quant.New(s) // 第三节 (丙)：六步摘要上的查询
	q5, q7 := q.Query(0.5), q.Query(0.7)
	ok("query-0.5/0.7", q5 == 25 && q7 == 25, fmt.Sprintf("QUERY(0.5)=%d QUERY(0.7)=%d", q5, q7))

	a, errA := api.New(0.1) // 四类可判定错误，互不相同
	_, errEps := api.New(1.5)
	_ = a.Insert(10)
	_ = a.Insert(20)
	n0, qBefore := a.Size(), mustQ(a, 0.5)
	errDup := a.Insert(10)
	_, errPhi := a.Query(2)
	empty, _ := api.New(0.1)
	_, errEmp := empty.Query(0.5)
	sentinels := []error{api.ErrBadEpsilon, api.ErrDuplicate, api.ErrBadPhi, api.ErrEmpty}
	distinct := true
	for i, x := range sentinels {
		for j, y := range sentinels {
			distinct = distinct && (i == j || !errors.Is(x, y))
		}
	}
	ok("errors", errA == nil && errors.Is(errEps, api.ErrBadEpsilon) && errors.Is(errDup, api.ErrDuplicate) &&
		errors.Is(errPhi, api.ErrBadPhi) && errors.Is(errEmp, api.ErrEmpty) && distinct, "4 sentinel errors")
	ok("reject-no-trace", a.Size() == n0 && mustQ(a, 0.5) == qBefore, "state unchanged after rejects")

	naive := true // 与朴素排序参照之差：给定规则的真实保证是 <2εn（见 NOTES.md）
	for _, eps := range []float64{0.25, 0.1, 0.05} {
		x, _ := api.New(eps)
		var ref []int64
		for _, p := range rand.New(rand.NewSource(42)).Perm(2000) {
			_ = x.Insert(int64(p))
			ref = append(ref, int64(p))
		}
		sort.Slice(ref, func(i, j int) bool { return ref[i] < ref[j] })
		fn := float64(len(ref))
		for k := 1; k < 20 && naive; k++ {
			got, _ := x.Query(float64(k) / 20)
			rank := float64(sort.Search(len(ref), func(i int) bool { return ref[i] >= got }) + 1)
			naive = math.Abs(rank-float64(k)/20*fn) <= 2*eps*fn
		}
	}
	ok("naive-reference", naive, "rank diff <= 2*eps*n for all phi")

	maxT := 0 // 大 m 下摘要元组数不随 m 线性增长
	for _, m := range []int{100, 1000, 10000} {
		g := gk.New(0.3)
		for i := 0; i < m; i++ {
			g.Insert(int64(i))
			g.Compress()
		}
		maxT = max(maxT, len(g.Tuples()))
	}
	ok("scaling", maxT <= 20, fmt.Sprintf("max tuples=%d for m up to 10000", maxT))

	c, _ := api.New(0.02) // 并发查询结果逐 φ 一致
	for i := 0; i < 5000; i++ {
		_ = c.Insert(int64(i))
	}
	phis := []float64{0.1, 0.3, 0.5, 0.7, 0.9}
	var wantQ [5]int64
	for i, p := range phis {
		wantQ[i] = mustQ(c, p)
	}
	var wg sync.WaitGroup
	var consistent int64 = 1
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i, p := range phis {
				if got, err := c.Query(p); err != nil || got != wantQ[i] || c.Size() != 5000 || c.SelfCheck() != nil {
					atomic.StoreInt64(&consistent, 0)
				}
			}
		}()
	}
	wg.Wait()
	ok("concurrent-query", atomic.LoadInt64(&consistent) == 1, "16 goroutines x 5 phis")
	ok("selfcheck", c.SelfCheck() == nil, "built-in sequences")

	os.Exit(map[bool]int{true: 1, false: 0}[fails > 0])
}

func mustQ(a *api.Summary, phi float64) int64 {
	v, err := a.Query(phi)
	if err != nil {
		fails++
	}
	return v
}
