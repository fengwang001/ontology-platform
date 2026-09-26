// Command demo exercises the weighted reservoir sampling packages.
package main

import (
	"errors"
	"fmt"
	"math"
	"os"
	"sort"
	"sync"

	"ontology/api"
)

var failed bool

func check(name string, ok bool, detail ...any) {
	if !ok {
		failed = true
	}
	fmt.Println(map[bool]string{true: "OK ", false: "FAIL "}[ok] + name + " " + fmt.Sprint(detail...))
}

type refItem struct {
	v string
	w float64
	u float64
}

func offlineRef(items []refItem, k int) []string {
	all := make([]refItem, len(items))
	copy(all, items)
	sort.SliceStable(all, func(i, j int) bool {
		xi, xj := math.Pow(all[i].u, 1/all[i].w), math.Pow(all[j].u, 1/all[j].w)
		return xi > xj || (xi == xj && all[i].v < all[j].v)
	})
	out := make([]string, 0, min(k, len(all)))
	for _, q := range all[:min(k, len(all))] {
		out = append(out, q.v)
	}
	return out
}

func sampleVals(s *api.Sampler) []string {
	ss := s.Sample()
	out := make([]string, len(ss))
	for i, it := range ss {
		out[i] = it.Val
	}
	return out
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func main() {
	// (1) Section 3 five-step trace; size==min(k,N); first k all kept.
	us := []float64{0.9, 0.64, 0.75, 0.343, 0.1}
	ws := []float64{1, 2, 1, 3, 1}
	vals := []string{"A", "B", "C", "D", "E"}
	s, _ := api.New(2, func(i int) float64 {
		if i-1 < len(us) {
			return us[i-1]
		}
		return 0.5
	})
	want := [][]string{{"A"}, {"A", "B"}, {"A", "B"}, {"A", "B"}, {"A", "B"}}
	traceOK, sizeOK, firstOK := true, true, true
	var trace [][]string
	for i := range vals {
		_ = s.Feed([]api.Item{{Val: vals[i], Weight: ws[i]}})
		got := sampleVals(s)
		trace = append(trace, got)
		traceOK = traceOK && eq(got, want[i])
		sizeOK = sizeOK && s.Size() == min(2, i+1)
		firstOK = firstOK && (i >= 2 || s.Size() == i+1)
	}
	check("五步样本", traceOK, trace)
	check("大小=min(k,N)/前k全留", sizeOK && firstOK)

	// (2) Online result equals the naive offline reference on another stream.
	rng2 := func(i int) float64 { return float64(uint64(i)*2654435761%999983+1) / 999984 }
	t, _ := api.New(4, rng2)
	var ref []refItem
	for i := 0; i < 20; i++ {
		it := api.Item{Val: string(rune('a' + i)), Weight: float64(1 + (i*7)%5)}
		ref = append(ref, refItem{it.Val, it.Weight, rng2(i + 1)})
		_ = t.Feed([]api.Item{it})
	}
	check("离线参照一致", eq(sampleVals(t), offlineRef(ref, 4)), sampleVals(t))

	// (3) Four distinct, decidable sentinel errors.
	_, e1 := api.New(0, rng2)
	_, e2 := api.New(2, nil)
	s3, _ := api.New(2, func(int) float64 { return 0 })
	e3 := s3.Feed([]api.Item{{Val: "x", Weight: 1}})
	e4a := s.Feed([]api.Item{{Val: "", Weight: 1}})
	e4b := s.Feed([]api.Item{{Val: "z", Weight: 0}})
	errsOK := errors.Is(e1, api.ErrInvalidK) && errors.Is(e2, api.ErrNilRNG) &&
		errors.Is(e3, api.ErrUniformOutOfRange) && errors.Is(e4a, api.ErrInvalidItem) &&
		errors.Is(e4b, api.ErrInvalidItem) && e1 != e2 && e2 != e3 && e3 != e4a
	check("四类错误可判定", errsOK)

	// (4) A rejected batch leaves no trace; sampler still usable afterwards.
	before, beforeN := sampleVals(s), s.Size()
	_ = s.Feed([]api.Item{{Val: "ok", Weight: 1}, {Val: "bad", Weight: -2}})
	noTrace := eq(sampleVals(s), before) && s.Size() == beforeN
	_ = s.Feed([]api.Item{{Val: "Z", Weight: 1}})
	check("失败不留痕", noTrace)

	// (5) Retained count stays exactly k for large m: memory is O(k).
	big, _ := api.New(10, rng2)
	memOK := true
	for _, m := range []int{100, 1000, 10000} {
		for n := 0; n < m; n++ {
			_ = big.Feed([]api.Item{{Val: fmt.Sprintf("v%d", n), Weight: float64(1 + n%9)}})
		}
		memOK = memOK && big.Size() == 10 && len(big.Sample()) == 10
	}
	check("大m保留恒为k", memOK)

	// (6) Concurrent readers of a full sampler see identical samples.
	var wg sync.WaitGroup
	res := make([][]string, 16)
	for g := range res {
		wg.Add(1)
		go func(g int) { defer wg.Done(); res[g] = sampleVals(big) }(g)
	}
	wg.Wait()
	concOK := true
	for _, r := range res[1:] {
		concOK = concOK && eq(r, res[0])
	}
	check("并发只读一致/SelfCheck", concOK && s.SelfCheck() == nil && big.SelfCheck() == nil)

	if failed {
		os.Exit(1)
	}
}
