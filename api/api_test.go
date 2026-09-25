package api_test

import (
	"errors"
	"math"
	"sync"
	"testing"

	"ontology/api"
	"ontology/perfect"
)

// result 是一次 Isqrt+IsSquare 调用的可比较快照。
type result struct {
	r int64
	s bool
	e error
}

func call(a *api.API, n int64) result {
	r, e := a.Isqrt(n)
	s, _ := a.IsSquare(n) // perfect 经 sqrt，错误必然同源
	return result{r, s, e}
}

// TestExactInvariant 钉住不变量 1（含第三节八个 n）与 3 的精确边界。
func TestExactInvariant(t *testing.T) {
	a := api.New()
	eight := []struct{ n, r int64 }{
		{0, 0}, {1, 1}, {15, 3}, {16, 4}, {17, 4}, {4503599761588224, 67108864},
		{4503599627370496, 67108864}, {math.MaxInt64, 3037000499},
	}
	for _, c := range eight {
		got := call(a, c.n)
		if got.e != nil || got.r != c.r {
			t.Errorf("isqrt(%d) = %d,%v; want %d", c.n, got.r, got.e, c.r)
		}
		if !invariant(c.n, got.r) {
			t.Errorf("invariant violated at n=%d r=%d", c.n, got.r)
		}
	}
	for _, k := range []int64{2, 3, 100, 1000, 10000, 67108864, 3037000499} {
		for _, c := range []struct{ n, want int64 }{
			{k * k, k}, {k*k - 1, k - 1}, {k*k + 1, k},
		} {
			got := call(a, c.n)
			if got.e != nil || got.r != c.want || !invariant(c.n, got.r) {
				t.Errorf("k=%d n=%d got=%d,%v want=%d", k, c.n, got.r, got.e, c.want)
			}
			if wantSq := c.n == k*k; got.s != wantSq {
				t.Errorf("IsSquare(%d)=%v want %v", c.n, got.s, wantSq)
			}
		}
	}
}

// invariant 检查 r² <= n < (r+1)²，上界用不溢出的整除形式。
func invariant(n, r int64) bool { return r*r <= n && n/(r+1) < r+1 }

// TestIsSquare 表驱动：平方为真、相邻数为假，覆盖 int64 边界。
func TestIsSquare(t *testing.T) {
	a := api.New()
	cases := []struct {
		n    int64
		want bool
	}{
		{0, true}, {1, true}, {2, false}, {3, false}, {4, true},
		{4503599627370496, true}, {4503599761588224, false},
		{4503599761588225, true}, {3037000499 * 3037000499, true}, {math.MaxInt64, false},
	}
	for _, c := range cases {
		if got := call(a, c.n); got.e != nil || got.s != c.want {
			t.Errorf("IsSquare(%d) = %v,%v; want %v", c.n, got.s, got.e, c.want)
		}
	}
}

// TestSentinelErrors 钉住不变量 4：三类错误可判定且互不相同，不 panic、无半成品。
func TestSentinelErrors(t *testing.T) {
	a := api.New()
	for _, c := range []struct {
		n    int64
		want error
	}{{-123, perfect.ErrNegative}, {math.MinInt64, perfect.ErrMinInt}} {
		if _, e := a.Isqrt(c.n); !errors.Is(e, c.want) {
			t.Fatalf("Isqrt(%d): %v", c.n, e)
		}
		if _, e := a.IsSquare(c.n); !errors.Is(e, c.want) {
			t.Fatalf("IsSquare(%d): %v", c.n, e)
		}
	}
	if perfect.ErrNegative == perfect.ErrMinInt || perfect.ErrNegative == perfect.ErrNotConverged ||
		perfect.ErrMinInt == perfect.ErrNotConverged {
		t.Fatal("sentinel errors must be pairwise distinct")
	}
}

// TestRejectionNoSideEffects 被拒后不返回半成品，后续调用仍正常。
func TestRejectionNoSideEffects(t *testing.T) {
	a := api.New()
	for _, n := range []int64{-1, math.MinInt64} {
		r, e := a.Isqrt(n)
		if e == nil || r != 0 {
			t.Fatalf("Isqrt(%d) = %d,%v; want 0,non-nil", n, r, e)
		}
	}
	for _, c := range []struct{ n, r int64 }{{0, 0}, {1, 1}, {25, 5}} {
		if r, e := a.Isqrt(c.n); e != nil || r != c.r {
			t.Fatalf("after rejection Isqrt(%d) = %d,%v; want %d", c.n, r, e, c.r)
		}
	}
}

// TestConcurrentMatchesSerial N 个 goroutine 并发，结果与串行逐条一致，无 sleep。
func TestConcurrentMatchesSerial(t *testing.T) {
	a := api.New()
	ns := []int64{0, 1, 15, 16, 17, 4503599761588224, 4503599627370496,
		math.MaxInt64, -7, math.MinInt64}
	serial := make([]result, len(ns))
	for i, n := range ns {
		serial[i] = call(a, n)
	}
	const N = 64
	var wg sync.WaitGroup
	errs := make(chan error, N*len(ns))
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i, n := range ns {
				got := call(a, n)
				if got.r != serial[i].r || got.s != serial[i].s ||
					(got.e != nil) != (serial[i].e != nil) {
					errs <- errors.New("mismatch")
				}
			}
		}()
	}
	wg.Wait()
	if len(errs) != 0 {
		t.Fatalf("%d concurrent mismatches", len(errs))
	}
}

// TestSelfCheck 自检方法对内置样本核验四条不变量，通过返回 nil。
func TestSelfCheck(t *testing.T) {
	if err := api.New().SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}
