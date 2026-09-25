// demo 逐条打印 MinHash 估计器的各项判定，全部 OK 时退出码为 0。
package main

import (
	"errors"
	"fmt"
	"math"
	"os"
	"slices"
	"sync"

	"ontology/api"
	"ontology/hash"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
	}
	fmt.Printf("%s: %s\n", map[bool]string{true: "OK", false: "FAIL"}[ok], name)
}

// countHash 包装注入的哈希，统计求值次数（演示 O(k) 与 m 无关）。
type countHash struct {
	h hash.Hash
	n *int
}

func (c countHash) Eval(x uint64) uint64     { *c.n++; return c.h.Eval(x) }
func (c countHash) Params() (a, b, p uint64) { return c.h.Params() }

func sec3() []hash.Hash {
	var hs []hash.Hash
	for _, p := range [][3]uint64{{2, 3, 11}, {3, 1, 11}, {5, 4, 11}, {7, 2, 11}} {
		h, err := hash.New(p[0], p[1], p[2])
		if err != nil {
			panic(err)
		}
		hs = append(hs, h)
	}
	return hs
}

func build(hs []hash.Hash, xs ...uint64) *api.Sketch {
	s, err := api.NewSketch(len(hs), hs)
	if err != nil {
		panic(err)
	}
	for _, x := range xs {
		s.Add(x)
	}
	return s
}

func minOf(h hash.Hash, xs []uint64) uint64 {
	m := uint64(math.MaxUint64)
	for _, x := range xs {
		if v := h.Eval(x); v < m {
			m = v
		}
	}
	return m
}

func main() {
	hs := sec3()
	a, b := build(hs, 1, 4, 7), build(hs, 1, 4, 8, 9)
	check("sig A=[0 0 2 7] B=[0 2 0 3]", slices.Equal(a.Signature(), []uint64{0, 0, 2, 7}) && slices.Equal(b.Signature(), []uint64{0, 2, 0, 3}))

	ab, err1 := api.Estimate(a, b)
	ba, err2 := api.Estimate(b, a)
	check("estimate 相等位置1/4=0.25 且对称", err1 == nil && err2 == nil && ab == 0.25 && ba == 0.25)

	mx := uint64(0)
	for _, x := range []uint64{1, 4, 8, 9} {
		if v := hs[1].Eval(x); v > mx {
			mx = v
		}
	}
	check("错值: 取max=6 取首个=4 (正确min=2)", mx == 6 && hs[1].Eval(1) == 4 && b.Signature()[1] == 2)

	check("空集合签名全为+Inf(MaxUint64)", slices.Equal(build(hs).Signature(), slices.Repeat([]uint64{math.MaxUint64}, 4)))

	naive := 0
	for _, h := range hs {
		if minOf(h, []uint64{1, 4, 7}) == minOf(h, []uint64{1, 4, 8, 9}) {
			naive++
		}
	}
	check("与朴素参照一致(相等位置=1)", naive == 1 && ab == float64(naive)/4)

	check("确定性: 两次构建逐位相同", slices.Equal(a.Signature(), build(hs, 1, 4, 7).Signature()))

	_, e1 := api.NewSketch(0, nil)
	_, e2 := api.NewSketch(1, []hash.Hash{nil})
	other, _ := hash.New(2, 3, 13)
	_, e3 := api.Estimate(build([]hash.Hash{other}, 5), build(hs[:1], 5))
	_, e4 := api.Estimate(a, build(hs))
	check("四类错误可判定且互异", errors.Is(e1, api.ErrBadK) && errors.Is(e2, api.ErrHashParams) &&
		errors.Is(e3, api.ErrHashMismatch) && errors.Is(e4, api.ErrEmptySet) &&
		!errors.Is(e1, e4) && !errors.Is(e3, e4))

	check("被拒后状态不变且可用", slices.Equal(a.Signature(), []uint64{0, 0, 2, 7}) && ab == 0.25)

	ok := true
	for _, m := range []int{100, 1000, 10000} {
		n := 0
		s, _ := api.NewSketch(4, []hash.Hash{countHash{hs[0], &n}, countHash{hs[1], &n}, countHash{hs[2], &n}, countHash{hs[3], &n}})
		for x := 0; x < m; x++ {
			before := n
			s.Add(uint64(x))
			ok = ok && n-before == 4
		}
	}
	check("单次Add恰k次求值, 与m无关", ok)

	const g = 32
	res := make([]float64, g)
	var wg sync.WaitGroup
	for i := 0; i < g; i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); v, _ := api.Estimate(a, b); res[i] = v }(i)
	}
	wg.Wait()
	same := true
	for _, v := range res {
		same = same && v == res[0]
	}
	check("并发只读结果逐位相同", same && res[0] == 0.25)

	if failed {
		os.Exit(1)
	}
}
