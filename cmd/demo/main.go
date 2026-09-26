package main

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
	"os"
	"sync"

	"ontology/api"
)

func main() {
	fails := 0
	ok := func(name string, good bool) {
		if good {
			fmt.Println("OK  ", name)
		} else {
			fails++
			fmt.Println("FAIL", name)
		}
	}
	rng := rand.New(rand.NewSource(1))

	// 1. 第三节五步 s。
	a, _ := api.New(0.9, 0, false)
	got := make([]float64, 5)
	for i, x := range []float64{10, 20, 10, 20, 10} {
		_ = a.Update("k", x)
		got[i], _ = a.Value("k")
	}
	want := []float64{9, 18.9, 10.89, 19.089, 10.9089}
	good := true
	for i := range got {
		good = good && math.Abs(got[i]-want[i]) <= 1e-10
	}
	ok("five-step s [9 18.9 10.89 19.089 10.9089]", good)
	// 2. 范围不变（校正分支 seed=0）。
	inRange := true
	for _, bc := range []bool{false, true} {
		r, _ := api.New(0.2+rng.Float64()*0.6, 0, bc)
		lo, hi := 0.0, 0.0
		for i := 0; i < 300; i++ {
			x := rng.NormFloat64() * 100
			lo, hi = math.Min(lo, x), math.Max(hi, x)
			_ = r.Update("r", x)
			v, _ := r.Value("r")
			inRange = inRange && v >= lo-1e-9 && v <= hi+1e-9
		}
	}
	ok("range invariant", inRange)
	// 3. 常数输入：校正精确为 c，不校正为 c(1-beta^n)。
	co, _ := api.New(0.9, 0, true)
	pl, _ := api.New(0.9, 0, false)
	bp, constOK := 1.0, true
	for n := 0; n < 30; n++ {
		bp *= 0.1
		_ = co.Update("c", 7)
		_ = pl.Update("c", 7)
		v1, _ := co.Value("c")
		v2, _ := pl.Value("c")
		constOK = constOK && math.Abs(v1-7) <= 1e-9 && math.Abs(v2-7*(1-bp)) <= 1e-9
	}
	ok("constant exact corrected/plain", constOK)
	// 4. 与闭式参照一致。
	cf, _ := api.New(0.3, 2.5, false)
	xs, cfOK := []float64{}, true
	for i := 0; i < 100; i++ {
		x := rng.NormFloat64() * 20
		xs = append(xs, x)
		_ = cf.Update("k", x)
		g, _ := cf.Value("k")
		w := math.Pow(0.7, float64(len(xs))) * 2.5
		for j, xx := range xs {
			w += 0.3 * math.Pow(0.7, float64(len(xs)-1-j)) * xx
		}
		cfOK = cfOK && math.Abs(g-w) <= 1e-8*math.Max(1, math.Abs(w))
	}
	ok("closed-form match", cfOK)
	// 5. 三类互不相同的哨兵错误。
	bad, errA := api.New(1.2, 0, false)
	g2, _ := api.New(0.5, 0, false)
	errK := g2.Update("", 1)
	_, errN := g2.Value("nope")
	ok("three distinct sentinel errors", bad == nil &&
		errors.Is(errA, api.ErrInvalidAlpha) && errors.Is(errK, api.ErrEmptyKey) &&
		errors.Is(errN, api.ErrNotFound) && errA != errK && errK != errN && errA != errN)
	// 6. 被拒不留痕，之后仍可用。
	_ = g2.Update("keep", 5)
	before, _ := g2.Value("keep")
	_ = g2.Update("", 9)
	_, errGhost := g2.Value("ghost")
	after, _ := g2.Value("keep")
	noTrace := errors.Is(errGhost, api.ErrNotFound) && after == before &&
		g2.Count("") == 0 && g2.Count("ghost") == 0 && g2.Update("keep", 8) == nil
	ok("rejected ops leave no trace; still usable", noTrace)
	// 7. 大 m 单次 Value 正常；reads==1 白盒断言在 ewma 同包测试。
	bigOK := true
	for _, m := range []int{100, 1000, 10000} {
		b, _ := api.New(0.4, 0, true)
		for i := 0; i < m; i++ {
			_ = b.Update("k", float64(i%13))
		}
		v, err := b.Value("k")
		bigOK = bigOK && err == nil && !math.IsNaN(v)
	}
	ok("O(1) state at m=100..10000 (reads==1 pinned in ewma test)", bigOK)

	// 8. 并发只读逐 key 一致。
	p, _ := api.New(0.3, 1, true)
	const K, R = 16, 16
	for k := 0; k < K; k++ {
		for i := 0; i < 20; i++ {
			_ = p.Update(string(rune('a'+k)), rng.NormFloat64())
		}
	}
	start := make(chan struct{})
	rows := make([][]float64, R)
	var wg sync.WaitGroup
	for r := 0; r < R; r++ {
		wg.Add(1)
		go func(r int) {
			defer wg.Done()
			<-start
			row := make([]float64, K)
			for k := range row {
				row[k], _ = p.Value(string(rune('a' + k)))
			}
			rows[r] = row
		}(r)
	}
	close(start)
	wg.Wait()
	cOK := true
	for k := 0; k < K; k++ {
		for r := 1; r < R; r++ {
			cOK = cOK && rows[r][k] == rows[0][k]
		}
	}
	ok("concurrent readers agree", cOK)

	// 9. 内置自检。
	sc, _ := api.New(0.4, 0, true)
	ok("SelfCheck", sc.SelfCheck() == nil)

	if fails > 0 {
		os.Exit(1)
	}
}
