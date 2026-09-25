package main

import (
	"errors"
	"fmt"
	"math/rand"
	"ontology/api"
	"ontology/sar"
	"sync"
)

type snapshot struct {
	last int64
	have bool
	n    int
}

func report(name string, ok bool) bool {
	if ok {
		fmt.Println("OK  " + name)
	} else {
		fmt.Println("FAIL " + name)
	}
	return ok
}

func main() {
	allOK := true

	// 第三节：N=4 八个序号，逐步 d / 判定 / 绝对序号；第4步回绕、第7步半圈。
	x, _ := api.New(4)
	const mask = 15
	seq := []uint64{13, 14, 15, 0, 1, 2, 10, 3}
	var ds []uint64
	var rels, abs []string
	var lastRes uint64 = seq[0]
	for i, s := range seq {
		var d uint64
		if i > 0 {
			d = (s - lastRes) & mask
		}
		rel := "first"
		if i > 0 {
			rel = x.Cmp(lastRes, s).String()
		}
		v, err := x.Feed(s)
		abs = append(abs, map[bool]string{true: fmt.Sprintf("%d", v), false: "err"}[err == nil])
		ds = append(ds, d)
		rels = append(rels, rel)
		if err == nil && (i == 0 || x.Cmp(lastRes, s) == sar.Less) {
			lastRes = s
		}
	}
	wantD := []uint64{0, 1, 1, 1, 1, 1, 8, 1}
	wantR := []string{"first", "Less", "Less", "Less", "Less", "Less", "Incomparable", "Less"}
	wantA := []string{"13", "14", "15", "16", "17", "18", "err", "19"}
	traceOK := len(abs) == 8
	for i := 0; i < 8; i++ {
		traceOK = traceOK && ds[i] == wantD[i] && rels[i] == wantR[i] && abs[i] == wantA[i]
	}
	l, _ := x.Last()
	allOK = report(fmt.Sprintf("8-step d=%v rel=%v abs=%v (step4 wrap->16, step7 half rejected)", ds[1:], rels, abs), traceOK && l == 19) && allOK

	// 反对称：随机 a,b，Cmp(a,b) 与 Cmp(b,a) 互反。
	anti, rng := true, rand.New(rand.NewSource(7))
	for _, n := range []int{1, 2, 4, 7, 16, 31, 63} {
		an, _ := api.New(n)
		m := uint64(1) << uint(n)
		for k := 0; k < 300; k++ {
			a, b := rng.Uint64()%m, rng.Uint64()%m
			r1, r2 := an.Cmp(a, b), an.Cmp(b, a)
			ok := (a == b && r1 == sar.Equal) ||
				(r1 == sar.Less && r2 == sar.Greater) ||
				(r1 == sar.Greater && r2 == sar.Less) ||
				(r1 == sar.Incomparable && r2 == sar.Incomparable)
			anti = anti && ok
		}
	}
	allOK = report("antisymmetry on random a,b across widths", anti) && allOK

	// 四类可判定错误，互不相同（哨兵身份比较）。
	_, eW := api.New(0)
	z, _ := api.New(4)
	_, eR := z.Feed(16)
	_, _ = z.Feed(0)
	_, eI := z.Feed(8)  // d==8 half circle
	_, eG := z.Feed(15) // d==15 > half, rewind
	distinct := eW != eR && eW != eI && eW != eG && eR != eI && eR != eG && eI != eG
	four := errors.Is(eW, api.ErrWidth) && errors.Is(eR, api.ErrOutOfRange) &&
		errors.Is(eI, api.ErrIncomparable) && errors.Is(eG, api.ErrGreater)
	allOK = report("four distinct decidable sentinel errors", four && distinct) && allOK

	// 失败不留痕：三次拒绝前后 Last 完全不变，之后仍可正常使用。
	y, _ := api.New(4)
	y.Feed(5)
	before, bh := y.Last()
	y.Feed(99)
	y.Feed(13) // d=8 incomparable
	y.Feed(4)  // d=15 greater
	after, ah := y.Last()
	v, contErr := y.Feed(6) // recovers: d=1 Less -> 6
	allOK = report("rejections leave last untouched; stream continues", before == 5 && after == 5 && bh && ah && v == 6 && contErr == nil) && allOK

	// O(1) 历史检查（计数器包内私有，只经 SelfCheck 给通过/失败）+ 不变量自检。
	scOK := true
	for _, n := range []int{1, 2, 4, 7, 16, 31, 63} {
		an, _ := api.New(n)
		scOK = scOK && an.SelfCheck() == nil
	}
	allOK = report("SelfCheck: invariants + per-Feed history checks <= 1, total == m", scOK) && allOK

	// 并发只读：多 goroutine 各自读取同一已展开实例，快照逐字段相同。
	c, _ := api.New(8)
	for i := 0; i < 500; i++ {
		c.Feed(uint64((100 + i) & 255))
	}
	want, _ := c.Last()
	const G = 16
	res := make([]snapshot, G)
	var wg sync.WaitGroup
	for g := 0; g < G; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for k := 0; k < 5000; k++ {
				ll, hh := c.Last()
				res[g] = snapshot{ll, hh, c.Width()}
			}
		}(g)
	}
	wg.Wait()
	concOK := true
	for _, s := range res {
		concOK = concOK && s == snapshot{want, true, 8}
	}
	allOK = report("concurrent readers see identical snapshots", concOK) && allOK

	if !allOK {
		panic("demo failed")
	}
}
