// demo 逐条打印 MinHash 估计器的判定，全部 OK 时退出码 0；不读参数、不联网。
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

func ok(name string, cond bool) {
	if !cond {
		failed = true
	}
	fmt.Println(map[bool]string{true: "OK ", false: "FAIL "}[cond] + name)
}

func hashes() []api.Hash {
	ps := [][3]uint64{{2, 3, 11}, {3, 1, 11}, {5, 4, 11}, {7, 2, 11}}
	hs := make([]api.Hash, 4)
	for i, p := range ps {
		hs[i], _ = hash.New(p[0], p[1], p[2])
	}
	return hs
}

func build(hs []api.Hash, xs ...uint64) *api.Sketch {
	s, _ := api.NewSketch(len(hs), hs)
	for _, x := range xs {
		s.Add(x)
	}
	return s
}

func naive(hs []api.Hash, sa, sb []uint64) float64 {
	eq := 0
	for _, h := range hs {
		f := func(xs []uint64) uint64 {
			m := uint64(math.MaxUint64)
			for _, x := range xs {
				m = min(m, h.Eval(x))
			}
			return m
		}
		if f(sa) == f(sb) {
			eq++
		}
	}
	return float64(eq) / float64(len(hs))
}

func main() {
	hs := hashes()
	a, b := build(hs, 1, 4, 7), build(hs, 1, 4, 8, 9)
	sa, sb := a.Signature(), b.Signature()
	ok(fmt.Sprintf("sigs A=%v B=%v", sa, sb), fmt.Sprint(sa) == "[0 0 2 7]" && fmt.Sprint(sb) == "[0 2 0 3]")

	eq := 0
	for i := range sa {
		if sa[i] == sb[i] {
			eq++
		}
	}
	est, _ := api.Estimate(a, b)
	ok(fmt.Sprintf("estimate eq=%d/4=%.2f, wrong(neq)/k=%.2f", eq, est, float64(4-eq)/4), eq == 1 && est == 0.25)

	h2v := []uint64{hs[1].Eval(1), hs[1].Eval(4), hs[1].Eval(8), hs[1].Eval(9)}
	ok(fmt.Sprintf("min sigB[2]=%d (max->%d first->%d)", slices.Min(h2v), slices.Max(h2v), h2v[0]),
		slices.Min(h2v) == 2 && slices.Max(h2v) == 6 && h2v[0] == 4)

	emptySig := build(hs).Signature()
	inf := true
	for _, v := range emptySig {
		inf = inf && v == math.MaxUint64
	}
	ok("empty sig = +Inf x4 (init-0 would false-match A at 2/4=0.50)", inf)

	rev, _ := api.Estimate(b, a)
	det := build(hs, 1, 4, 7).Signature()
	ok("symmetric & naive-consistent & deterministic",
		rev == est && est == naive(hs, []uint64{1, 4, 7}, []uint64{1, 4, 8, 9}) && fmt.Sprint(det) == fmt.Sprint(sa))

	_, e1 := api.NewSketch(0, hs)
	_, e2 := hash.New(0, 1, 1)
	_, e3 := api.Estimate(build(hs, 1), build(hs))
	_, e4 := api.Estimate(build(hs, 1), build(hs[:1], 1))
	distinct := errors.Is(e1, api.ErrBadK) && errors.Is(e2, api.ErrBadParams) &&
		errors.Is(e3, api.ErrEmpty) && errors.Is(e4, api.ErrHashMismatch)
	ok("4 distinct sentinel errors", distinct)

	before := fmt.Sprint(a.Signature())
	_, _ = api.Estimate(a, build(hs))
	_, _ = api.Estimate(a, build(hs[:1], 1))
	ok("rejected ops leave state unchanged", fmt.Sprint(a.Signature()) == before)

	scaleOK := true
	for _, m := range []int{100, 1000, 10000} { // 多档规模：每次 Add 恰好 k 次求值（计数器由白盒 TestAddEvalsConstant 钉死）
		xs := make([]uint64, m)
		s := build(hs)
		for i := range xs {
			xs[i] = uint64(i)
			s.Add(xs[i])
		}
		scaleOK = scaleOK && len(s.Signature()) == 4 && naive(hs, xs, xs) == 1
	}
	ok("Add cost k per element, estimate O(k) independent of m=100..10000", scaleOK)
	ok("concurrent read-only estimates identical", concurrent(a, b, est))
	if failed {
		os.Exit(1)
	}
}

func concurrent(a, b *api.Sketch, want float64) bool {
	const n = 64
	var wg sync.WaitGroup
	res := make(chan bool, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, err := api.Estimate(a, b)
			_ = a.Signature()
			res <- err == nil && v == want && api.SelfCheck() == nil
		}()
	}
	wg.Wait()
	close(res)
	for r := range res {
		if !r {
			return false
		}
	}
	return true
}
