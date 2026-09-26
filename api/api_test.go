package api_test

import (
	"errors"
	"math"
	"math/rand"
	"sync"
	"testing"

	"ontology/api"
)

func must(t *testing.T, f func(float64) float64, a, b float64) *api.Estimator {
	t.Helper()
	est, err := api.New(f, a, b)
	if err != nil {
		t.Fatal(err)
	}
	return est
}

func addN(t *testing.T, est *api.Estimator, x float64, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if err := est.Add(x); err != nil {
			t.Fatal(err)
		}
	}
}

func TestConstantFunctionExact(t *testing.T) {
	for _, c := range []float64{-2.5, 0, 3.75} {
		for _, ab := range [][2]float64{{0, 2}, {1, 1.5}, {-1, 2}} {
			for _, n := range []int{1, 7, 50} {
				est := must(t, func(float64) float64 { return c }, ab[0], ab[1])
				addN(t, est, ab[0], n)
				got, err := est.Estimate()
				if want := c * (ab[1] - ab[0]); err != nil || got != want {
					t.Fatalf("c=%v ab=%v n=%d: got %v,%v want %v", c, ab, n, got, err, want)
				}
			}
		}
	}
}

func TestMatchesNaiveReplay(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	cases := []struct {
		f    func(float64) float64
		a, b float64
		n    int
	}{
		{func(x float64) float64 { return x * x }, 0, 2, 4},
		{math.Sin, -1, 3, 1000},
		{func(x float64) float64 { return x*x - 2*x + 1 }, -5, 5, 4096},
	}
	for _, tc := range cases {
		est := must(t, tc.f, tc.a, tc.b)
		var sum float64
		var n int64
		for i := 0; i < tc.n; i++ {
			x := tc.a + rng.Float64()*(tc.b-tc.a)
			if err := est.Add(x); err != nil {
				t.Fatal(err)
			}
			sum += tc.f(x)
			n++
		}
		got, err := est.Estimate()
		if want := (tc.b - tc.a) * sum / float64(n); err != nil || got != want {
			t.Fatalf("got %v,%v want %v", got, err, want)
		}
	}
}

func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	if _, err := api.New(func(x float64) float64 { return x }, 2, 1); !errors.Is(err, api.ErrInvalidInterval) {
		t.Fatalf("a>b: %v", err)
	}
	if _, err := api.New(nil, 0, 1); !errors.Is(err, api.ErrNilFunc) {
		t.Fatalf("nil f: %v", err)
	}
	est := must(t, func(x float64) float64 { return x }, 0, 1)
	if _, err := est.Estimate(); !errors.Is(err, api.ErrNoSamples) {
		t.Fatalf("empty estimate: %v", err)
	}
	addN(t, est, 0.5, 1)
	before, _ := est.Estimate()
	for _, x := range []float64{-0.1, 1.1, math.Inf(1)} {
		if err := est.Add(x); !errors.Is(err, api.ErrOutOfRange) {
			t.Fatalf("x=%v: %v", x, err)
		}
	}
	after, _ := est.Estimate()
	if est.Samples() != 1 || after != before {
		t.Fatalf("state changed: samples=%d est %v->%v", est.Samples(), before, after)
	}
	if err := est.Add(0.25); err != nil || est.Samples() != 2 {
		t.Fatalf("not usable after rejects: %v", err)
	}
}

func TestSentinelErrorsDistinct(t *testing.T) {
	errs := []error{api.ErrInvalidInterval, api.ErrNilFunc, api.ErrOutOfRange, api.ErrNoSamples}
	for i, ei := range errs {
		for j, ej := range errs {
			if i != j && (ei == ej || errors.Is(ei, ej)) {
				t.Fatalf("sentinels %d and %d not distinct", i, j)
			}
		}
	}
}

func TestConcurrentEstimateConsistent(t *testing.T) {
	est := must(t, func(x float64) float64 { return x * x }, 0, 2)
	for i := 0; i < 1000; i++ {
		if err := est.Add(float64(i%100) / 50); err != nil {
			t.Fatal(err)
		}
	}
	want, _ := est.Estimate()
	const g = 32
	results := make(chan float64, g*100)
	var wg sync.WaitGroup
	for i := 0; i < g; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = est.Samples()
			_ = est.SelfCheck()
			for j := 0; j < 100; j++ {
				v, err := est.Estimate()
				if err != nil {
					t.Error(err)
					return
				}
				results <- v
			}
		}()
	}
	wg.Wait()
	close(results)
	for v := range results {
		if v != want {
			t.Fatalf("concurrent estimate %v != %v", v, want)
		}
	}
}
