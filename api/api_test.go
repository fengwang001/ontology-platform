package api

import (
	"errors"
	"sync"
	"testing"
)

// TestConstantExact 不变量 1：常数函数的估计精确等于 c*(b-a)。
func TestConstantExact(t *testing.T) {
	cases := []struct {
		c, a, b float64
		n       int
	}{
		{3.25, -1, 4, 4}, {0, 0, 2, 1}, {-2.5, -3, -1, 7}, {1e6, 0, 1, 13},
	}
	for _, tc := range cases {
		in, err := New(func(float64) float64 { return tc.c }, tc.a, tc.b)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < tc.n; i++ {
			x := tc.a + (tc.b-tc.a)*float64(i)/float64(tc.n)
			if err := in.Add(x); err != nil {
				t.Fatal(err)
			}
		}
		if est, _ := in.Estimate(); est != tc.c*(tc.b-tc.a) {
			t.Fatalf("%+v: est=%v, want %v", tc, est, tc.c*(tc.b-tc.a))
		}
	}
}

// TestMatchesNaiveReplay 不变量 2：与逐点求和的朴素批量重放一致。
func TestMatchesNaiveReplay(t *testing.T) {
	f := func(x float64) float64 { return x*x - x + 0.5 }
	for seed := int64(1); seed <= 30; seed++ {
		in, _ := New(f, 0, 2)
		sum, n := 0.0, int64(0)
		for i := int64(0); i < 40; i++ {
			x := float64((seed*37+i*29)%201) / 100
			sum += float64(f(x)) // 显式转换阻断 FMA 融合，与逐点求值一致
			n++
			if err := in.Add(x); err != nil {
				t.Fatal(err)
			}
		}
		if est, _ := in.Estimate(); est != 2*sum/float64(n) {
			t.Fatalf("seed=%d: est=%v, want %v", seed, est, 2*sum/float64(n))
		}
	}
}

// TestZeroWidthInterval 不变量 3：a==b 时估计为 0。
func TestZeroWidthInterval(t *testing.T) {
	for _, a := range []float64{0, 1.5, -2, 100} {
		in, err := New(func(x float64) float64 { return x * x }, a, a)
		if err != nil || in.Add(a) != nil {
			t.Fatal(err)
		}
		if est, _ := in.Estimate(); est != 0 {
			t.Fatalf("a=%v: est=%v, want 0", a, est)
		}
	}
}

// TestRejectionLeavesNoTrace 不变量 4：四类拒绝互不相同且不改状态。
func TestRejectionLeavesNoTrace(t *testing.T) {
	if _, err := New(func(x float64) float64 { return x }, 2, 1); !errors.Is(err, ErrInvalidInterval) {
		t.Fatalf("a>b: %v", err)
	}
	if _, err := New(nil, 0, 1); !errors.Is(err, ErrNilFunc) {
		t.Fatalf("nil f: %v", err)
	}
	empty, _ := New(func(x float64) float64 { return x }, 0, 1)
	if _, err := empty.Estimate(); !errors.Is(err, ErrNoSamples) {
		t.Fatalf("empty estimate: %v", err)
	}
	errs := []error{ErrInvalidInterval, ErrNilFunc, ErrOutOfRange, ErrNoSamples}
	seen := map[error]bool{}
	for _, e := range errs {
		if seen[e] {
			t.Fatalf("duplicate sentinel: %v", e)
		}
		seen[e] = true
	}
	in, _ := New(func(x float64) float64 { return x * x }, 0, 2)
	if err := in.Add(1.0); err != nil {
		t.Fatal(err)
	}
	n0, est0 := in.Samples(), mustEst(t, in)
	for _, x := range []float64{-0.5, 2.5} {
		if err := in.Add(x); !errors.Is(err, ErrOutOfRange) {
			t.Fatalf("x=%v: %v", x, err)
		}
	}
	if in.Samples() != n0 || mustEst(t, in) != est0 {
		t.Fatal("rejected ops changed state")
	}
	if err := in.Add(2.0); err != nil {
		t.Fatal("integrator unusable after rejections")
	}
}

func mustEst(t *testing.T, in *Integrator) float64 {
	t.Helper()
	est, err := in.Estimate()
	if err != nil {
		t.Fatal(err)
	}
	return est
}

// TestConcurrentEstimate 并发估计结果完全一致，无 sleep 制造时序。
func TestConcurrentEstimate(t *testing.T) {
	in, _ := New(func(x float64) float64 { return x * x }, 0, 2)
	for i := 0; i < 500; i++ {
		_ = in.Add(float64(i%201) / 100) // 值域内，不会失败
	}
	want := mustEst(t, in)
	var wg sync.WaitGroup
	errs := make(chan float64, 8*64)
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 64; i++ {
				est, err := in.Estimate()
				if err != nil || est != want {
					errs <- est
				}
				_ = in.Samples()
				_ = in.SelfCheck()
			}
		}()
	}
	wg.Wait()
	close(errs)
	for est := range errs {
		t.Fatalf("concurrent estimate=%v, want %v", est, want)
	}
}

func TestSelfCheck(t *testing.T) {
	in, _ := New(func(x float64) float64 { return x }, 0, 1)
	if err := in.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
