package merge

import (
	"math/rand/v2"
	"slices"
	"sync"
	"testing"

	"ontology/schema"
)

// naiveMerge 按规格逐条写成的朴素实现：先求交集，再逐源桶线性扫描找
// 「不小于该桶上界的最小公共边界」，找不到或溢出桶则入结果溢出桶。
func naiveMerge(a, b schema.Hist) schema.Hist {
	inA := make(map[int64]bool, len(a.Bounds))
	for _, x := range a.Bounds {
		inA[x] = true
	}
	var common []int64
	for _, x := range b.Bounds {
		if inA[x] {
			common = append(common, x)
		}
	}
	out := schema.Hist{Name: a.Name, Bounds: common, Counts: make([]int64, len(common)+1)}
	add := func(h schema.Hist) {
		for i, up := range h.Bounds {
			target := len(common)
			for j, cb := range common {
				if cb >= up {
					target = j
					break
				}
			}
			out.Counts[target] += h.Counts[i]
		}
		out.Counts[len(common)] += h.Counts[len(h.Bounds)]
	}
	add(a)
	add(b)
	out.Sum = a.Sum + b.Sum
	return out
}

func randHist(r *rand.Rand, name string, maxBound, maxCount int64) schema.Hist {
	n := 1 + r.Int64N(8)
	set := map[int64]bool{}
	var bounds []int64
	for int64(len(bounds)) < n {
		b := 1 + r.Int64N(maxBound)
		if !set[b] {
			set[b] = true
			bounds = append(bounds, b)
		}
	}
	slices.Sort(bounds)
	counts := make([]int64, len(bounds)+1)
	var sum int64
	for i := range counts {
		counts[i] = r.Int64N(maxCount)
		sum += counts[i] * bounds[min(i, len(bounds)-1)] / 2
	}
	return schema.Hist{Name: name, Bounds: bounds, Counts: counts, Sum: sum}
}

func total(c []int64) int64 {
	var t int64
	for _, x := range c {
		t += x
	}
	return t
}

func TestMergeAgainstNaive(t *testing.T) {
	r := rand.New(rand.NewPCG(42, 7))
	for trial := 0; trial < 500; trial++ {
		a := randHist(r, "n", 200, 1000)
		b := randHist(r, "n", 200, 1000)
		got, err := Merge(&a, &b)
		want := naiveMerge(a, b)
		if len(want.Bounds) == 0 {
			if err == nil {
				t.Fatalf("trial %d: 应报不兼容\na=%v\nb=%v", trial, a, b)
			}
			continue
		}
		if err != nil {
			t.Fatalf("trial %d: %v\na=%v\nb=%v", trial, err, a, b)
		}
		if !slices.Equal(got.Bounds, want.Bounds) || !slices.Equal(got.Counts, want.Counts) || got.Sum != want.Sum {
			t.Fatalf("trial %d:\na=%v\nb=%v\ngot=%v\nwant=%v", trial, a, b, got, want)
		}
		// 不变量：总计数与 Sum 守恒；结果边界是两侧子集
		if total(got.Counts) != total(a.Counts)+total(b.Counts) || got.Sum != a.Sum+b.Sum {
			t.Fatalf("trial %d: 不守恒 got=%v a=%v b=%v", trial, got, a, b)
		}
		for _, x := range got.Bounds {
			if !slices.Contains(a.Bounds, x) || !slices.Contains(b.Bounds, x) {
				t.Fatalf("trial %d: 结果边界非子集 %d", trial, x)
			}
		}
		if trial < 3 {
			t.Logf("trial %d 输入 a=%v b=%v 输出 %v（判定：与朴素交集+上移并桶模拟一致，计数与 Sum 守恒）",
				trial, a, b, got)
		}
	}
}

func TestMergeAllAgainstNaiveAndConcurrent(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	base := randHist(r, "all", 100, 100)
	hs := []schema.Hist{base}
	for i := 0; i < 4; i++ {
		h := randHist(r, "all", 100, 100)
		h.Bounds = append(append([]int64(nil), base.Bounds...), 1000+int64(i))
		slices.Sort(h.Bounds)
		h.Counts = make([]int64, len(h.Bounds)+1)
		for j := range h.Counts {
			h.Counts[j] = r.Int64N(50)
		}
		hs = append(hs, h)
	}
	want, err := MergeAll(hs)
	if err != nil {
		t.Fatal(err)
	}
	// 与朴素逐步合并对照
	acc := hs[0]
	for i := 1; i < len(hs); i++ {
		acc = naiveMerge(acc, hs[i])
	}
	if !slices.Equal(want.Bounds, acc.Bounds) || !slices.Equal(want.Counts, acc.Counts) || want.Sum != acc.Sum {
		t.Fatalf("MergeAll=%v naive=%v", want, acc)
	}
	t.Logf("输入 %v -> MergeAll=%v（判定：与朴素逐步交集合并一致）", hs, want)
	// 并发 Merge 同一对输入，结果必须与串行完全一致
	var wg sync.WaitGroup
	results := make([]schema.Hist, 16)
	for k := range results {
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			g, err := Merge(&hs[0], &hs[1])
			if err != nil {
				t.Error(err)
				return
			}
			results[k] = g
		}(k)
	}
	wg.Wait()
	for k := 1; k < len(results); k++ {
		if !slices.Equal(results[k].Counts, results[0].Counts) || results[k].Sum != results[0].Sum {
			t.Fatalf("并发结果不稳定: %v vs %v", results[k], results[k-1])
		}
	}
}
