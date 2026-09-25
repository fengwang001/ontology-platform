package main

import (
	"errors"
	"fmt"
	"math"
	"math/bits"
	"os"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/perfect"
	"ontology/sqrt"
)

var fails int

func line(label string, ok bool) {
	s := "OK   "
	if !ok {
		s, fails = "FAIL ", fails+1
	}
	fmt.Println(s + label)
}

// naive 是「从 0 逐个平方扫到超过 n」的朴素参照。
func naive(n int64) (k int64) {
	for ; (k+1)*(k+1) <= n; k++ {
	}
	return
}

// localIters 是 demo 自带的牛顿迭代（不读 sqrt 包的非导出计数器），演示步数与规模无关。
func localIters(n int64) int {
	x := int64(1) << uint((bits.Len64(uint64(n))+1)/2)
	for i := 1; ; i++ {
		y := (x + n/x) / 2
		if y >= x {
			return i
		}
		x = y
	}
}

// bisect 用不溢出的整除比较做二分；strict 模拟把 <= 错写成严格 <。
func bisect(n int64, strict bool) int64 {
	lo, hi := int64(0), int64(1)<<uint((bits.Len64(uint64(n))+1)/2)
	for lo < hi {
		mid := (lo + hi + 1) / 2
		less := mid <= n/mid // mid*mid<=n 的不溢出形式
		if strict {
			less = mid <= (n-1)/mid
		}
		if less {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return lo
}

func main() {
	// 1. 第三节的八个 n。
	eight := []struct{ n, r int64 }{
		{0, 0}, {1, 1}, {15, 3}, {16, 4}, {17, 4}, {4503599761588224, 67108864}, {4503599627370496, 67108864}, {math.MaxInt64, 3037000499},
	}
	ok8 := true
	for _, c := range eight {
		r, err := sqrt.Isqrt(c.n)
		ok8 = ok8 && err == nil && r == c.r
	}
	line("eight n isqrt values", ok8)
	// 2. float64 路线在 67108865^2-1 处偏大 1。
	const n6 = int64(4503599761588224)
	f := int64(uint64(math.Sqrt(float64(n6))))
	good, _ := sqrt.Isqrt(n6)
	line("float64 route overshoots by 1", f == good+1 && good == 67108864)
	// 3. hi=n、mid*mid<=n：第一次 mid*mid 在 int64 下回绕成负数。
	mid := math.MaxInt64 / 2
	line("mid*mid wraps negative", mid*mid < 0)
	// 4. 完美平方边界：<= 精确得 k，严格 < 下偏 1。
	const n7 = int64(4503599627370496)
	line("<= accepts square, < undershoots", bisect(n7, false) == 67108864 && bisect(n7, true) == 67108863)
	// 5. 与朴素参照一致（分档扫描，覆盖到 10^6）。
	naiveOK := true
	for b := int64(0); b <= 1_000_000; b += 4999 {
		if r, _ := sqrt.Isqrt(b); r != naive(b) {
			naiveOK = false
		}
	}
	line("agrees with naive scan", naiveOK)
	// 6. IsSquare 与 isqrt 一致（含 k²/k²±1 与最大完美平方）。
	sqOK := true
	for _, k := range []int64{0, 1, 2, 67108864, 67108865, 3037000499} {
		s, err := perfect.IsSquare(k * k)
		sqOK = sqOK && err == nil && s
		for _, d := range []int64{-1, 1} {
			if k >= 2 { // k<2 时 k²±1 可能是相邻平方，跳过
				s, err = perfect.IsSquare(k*k + d)
				sqOK = sqOK && err == nil && !s
			}
		}
	}
	line("IsSquare matches isqrt at boundaries", sqOK)
	// 7. 三类哨兵错误互不相同，被拒后无副作用。
	_, eNeg := sqrt.Isqrt(-123)
	_, eMin := sqrt.Isqrt(math.MinInt64)
	distinct := errors.Is(eNeg, sqrt.ErrNegative) && errors.Is(eMin, sqrt.ErrMinInt) &&
		eNeg.Error() != eMin.Error() && sqrt.ErrNotConverged.Error() != eNeg.Error()
	after, _ := sqrt.Isqrt(25)
	line("three distinct sentinel errors; no side effects", distinct && after == 5)
	bounded := true
	for _, m := range []int64{100, 1000, 10000, 100000, 3037000499} {
		bounded = bounded && localIters(m*m) <= 40
	}
	line("iterations bounded regardless of scale", bounded)
	a := api.New()
	ns := []int64{0, 1, 15, 16, 17, 4503599761588224, 4503599627370496, math.MaxInt64}
	eq := func(n int64) bool {
		r, e := a.Isqrt(n)
		s, e2 := a.IsSquare(n)
		return e == nil && e2 == nil && s == (r*r == n)
	}
	want := make(map[int64]bool, len(ns))
	for _, n := range ns {
		want[n] = eq(n)
	}
	var bad atomic.Int32
	var wg sync.WaitGroup
	for g := 0; g < 64; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, n := range ns {
				if eq(n) != want[n] {
					bad.Add(1)
				}
			}
		}()
	}
	wg.Wait()
	line("concurrent results match serial", bad.Load() == 0)
	// 10. 自检：四条不变量全部通过。
	line("SelfCheck passes", a.SelfCheck() == nil)
	if fails > 0 {
		os.Exit(1)
	}
}
