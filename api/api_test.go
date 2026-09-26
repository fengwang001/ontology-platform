package api

import (
	"errors"
	"math"
	"math/rand"
	"sync"
	"testing"
)

// closedForm is the batch reference: alpha*sum(1-alpha)^(n-i)*x_i + (1-alpha)^n*seed.
func closedForm(alpha, seed float64, xs []float64) float64 {
	s, w := 0.0, 1.0
	for i := len(xs) - 1; i >= 0; i-- {
		s += w * xs[i]
		w *= 1 - alpha
	}
	return alpha*s + w*seed
}

func TestRangeInvariant(t *testing.T) {
	cases := []struct {
		alpha, seed float64
		biasCorrect bool
	}{
		{0.9, 0, true}, {0.3, 0, true}, {0.5, 5, false}, {0.1, -3, false},
	}
	rng := rand.New(rand.NewSource(1))
	for _, tc := range cases {
		svc, _ := New(tc.alpha, tc.seed, tc.biasCorrect)
		lo, hi := tc.seed, tc.seed
		for i := 0; i < 200; i++ {
			x := rng.Float64()*20 - 10
			lo, hi = math.Min(lo, x), math.Max(hi, x)
			_ = svc.Update("k", x)
			v, err := svc.Value("k")
			if err != nil || v < lo-1e-12 || v > hi+1e-12 {
				t.Fatalf("%+v step %d: v=%v outside [%v,%v]", tc, i, v, lo, hi)
			}
		}
	}
}

func TestConstantInputExact(t *testing.T) {
	for _, alpha := range []float64{0.1, 0.5, 0.9} {
		for _, c := range []float64{-3, 0, 4, 7.5} {
			corr, _ := New(alpha, 0, true)
			raw, _ := New(alpha, 0, false)
			for n := 1; n <= 20; n++ {
				_ = corr.Update("k", c)
				_ = raw.Update("k", c)
				vc, _ := corr.Value("k")
				vr, _ := raw.Value("k")
				want := c * (1 - math.Pow(1-alpha, float64(n)))
				// alpha=0.5 is dyadic: operations are exact, so the corrected
				// value must equal c bit-for-bit; others allow float rounding.
				if alpha == 0.5 && vc != c {
					t.Fatalf("alpha=0.5 c=%v n=%d: corrected=%v, want exact %v", c, n, vc, c)
				}
				if math.Abs(vc-c) > 1e-9 || math.Abs(vr-want) > 1e-9 {
					t.Fatalf("alpha=%v c=%v n=%d: corrected=%v raw=%v want raw=%v",
						alpha, c, n, vc, vr, want)
				}
			}
		}
	}
}

func TestMatchesClosedForm(t *testing.T) {
	cases := []struct {
		alpha, seed float64
		biasCorrect bool
	}{
		{0.9, 0, false}, {0.25, 1.5, false}, {0.5, 0, true}, {0.7, -2, false},
	}
	rng := rand.New(rand.NewSource(2))
	for _, tc := range cases {
		for _, n := range []int{1, 5, 50, 500} {
			svc, _ := New(tc.alpha, tc.seed, tc.biasCorrect)
			xs := make([]float64, n)
			for i := range xs {
				xs[i] = rng.Float64()*100 - 50
				_ = svc.Update("k", xs[i])
			}
			got, _ := svc.Value("k")
			want := closedForm(tc.alpha, tc.seed, xs)
			if tc.biasCorrect {
				want /= 1 - math.Pow(1-tc.alpha, float64(n))
			}
			if math.Abs(got-want) > 1e-6 {
				t.Fatalf("%+v n=%d: got %v want %v", tc, n, got, want)
			}
		}
	}
}

func TestRejectionLeavesNoTrace(t *testing.T) {
	for _, bad := range []float64{0, -0.5, 1, 2} {
		if _, err := New(bad, 0, false); !errors.Is(err, ErrBadAlpha) {
			t.Fatalf("alpha=%v: got %v, want ErrBadAlpha", bad, err)
		}
	}
	svc, _ := New(0.5, 0, false)
	_ = svc.Update("k", 2)
	before, _ := svc.Value("k")
	nbefore, _ := svc.Count("k")
	_, errV := svc.Value("ghost")
	_, errC := svc.Count("ghost")
	rejects := []error{svc.Update("", 99), errV, errC}
	for i, want := range []error{ErrEmptyKey, ErrUnknownKey, ErrUnknownKey} {
		if !errors.Is(rejects[i], want) {
			t.Fatalf("reject %d: got %v, want %v", i, rejects[i], want)
		}
	}
	after, _ := svc.Value("k")
	nafter, _ := svc.Count("k")
	if after != before || nafter != nbefore {
		t.Fatal("rejected operations mutated state")
	}
	if err := svc.Update("k", 4); err != nil { // still usable after rejections
		t.Fatal(err)
	}
}

func TestConcurrentReadConsistency(t *testing.T) {
	svc, _ := New(0.4, 1, true)
	keys := []string{"a", "b", "c", "d", "e"}
	want := make(map[string]float64)
	for i, k := range keys {
		for j := 0; j < 20+i; j++ {
			_ = svc.Update(k, float64(i*j+j))
		}
		want[k], _ = svc.Value(k)
	}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, k := range keys {
				v, err := svc.Value(k)
				if err != nil || v != want[k] {
					t.Errorf("key %s: got v=%v err=%v, want %v", k, v, err, want[k])
				}
			}
		}()
	}
	wg.Wait()
}
