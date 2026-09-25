package api_test

import (
	"errors"
	"math/big"
	"math/rand"
	"sync"
	"testing"

	"ontology/api"
	"ontology/num"
)

func must(t *testing.T, n, d int64) *api.Rat {
	t.Helper()
	r, err := api.New(n, d)
	if err != nil {
		t.Fatalf("New(%d,%d): %v", n, d, err)
	}
	return r
}

// TestSevenSteps 钉住第三节的七行分步表。
func TestSevenSteps(t *testing.T) {
	v := must(t, 6, -8)
	if v.String() != "-3/4" {
		t.Fatalf("step 1: %s, want -3/4", v)
	}
	seq := []struct {
		f    func(x, y *api.Rat) (*api.Rat, error)
		n, d int64
		want string
	}{
		{api.Add, 5, 6, "1/12"}, {api.Mul, -4, 1, "-1/3"}, {api.Sub, 1, 6, "-1/2"},
		{api.Div, -3, 1, "1/6"}, {api.Add, 1, 6, "1/3"}, {api.Mul, 0, 5, "0/1"},
	}
	for i, s := range seq {
		var err error
		if v, err = s.f(v, must(t, s.n, s.d)); err != nil || v.String() != s.want {
			t.Fatalf("step %d: got %v err=%v, want %s", i+2, v, err, s.want)
		}
	}
}

// TestAgainstBigReference 不变量 1：与 big.Rat 朴素参照逐条一致。
func TestAgainstBigReference(t *testing.T) {
	names := []string{"add", "sub", "mul", "div"}
	fs := []func(x, y *api.Rat) (*api.Rat, error){api.Add, api.Sub, api.Mul, api.Div}
	gs := []func(x, y *big.Rat) *big.Rat{new(big.Rat).Add, new(big.Rat).Sub, new(big.Rat).Mul, new(big.Rat).Quo}
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 300; i++ {
		an, ad := rng.Int63n(20001)-10000, rng.Int63n(10000)+1
		bn, bd := rng.Int63n(20001)-10000, rng.Int63n(10000)+1
		ad *= 1 - 2*rng.Int63n(2)
		a, b := must(t, an, ad), must(t, bn, bd)
		ra, rb := big.NewRat(an, ad), big.NewRat(bn, bd)
		for k := range names {
			if names[k] == "div" && b.IsZero() {
				continue
			}
			got, err := fs[k](a, b)
			if err != nil {
				t.Fatalf("%s(%s,%s): %v", names[k], a, b, err)
			}
			if want := gs[k](ra, rb).String(); got.String() != want {
				t.Fatalf("invariant1: %s(%s,%s)=%s, want %s", names[k], a, b, got, want)
			}
		}
	}
}

// TestCanonicalForm 不变量 2：分母恒正、既约、等值同形、0 恒为 0/1。
func TestCanonicalForm(t *testing.T) {
	in := [][2]int64{{6, -8}, {0, 5}, {0, -7}, {-3, -6}, {4, 2}, {-4, 2}, {7, 1}, {-7, -1}}
	want := []string{"-3/4", "0/1", "0/1", "1/2", "2/1", "-2/1", "7/1", "7/1"}
	for i, c := range in {
		r := must(t, c[0], c[1])
		if r.String() != want[i] {
			t.Errorf("New(%d,%d)=%s, want %s", c[0], c[1], r, want[i])
		}
		if r.Den() <= 0 || num.Gcd(r.Num(), r.Den()) != 1 {
			t.Errorf("New(%d,%d)=%s not canonical", c[0], c[1], r)
		}
	}
}

// TestAddCommutative 不变量 3：加法交换律。
func TestAddCommutative(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 200; i++ {
		a := must(t, rng.Int63n(2001)-1000, rng.Int63n(1000)+1)
		b := must(t, rng.Int63n(2001)-1000, rng.Int63n(1000)+1)
		ab, err1 := api.Add(a, b)
		ba, err2 := api.Add(b, a)
		if err1 != nil || err2 != nil {
			t.Fatal(err1, err2)
		}
		if ab.String() != ba.String() {
			t.Fatalf("invariant3: %s+%s=%s but %s+%s=%s", a, b, ab, b, a, ba)
		}
	}
}

// TestFailureNoSideEffect 不变量 4 + 故障注入：三类错误可判定且互不相同，
// 被拒操作整体失败、不留痕、可继续使用。
func TestFailureNoSideEffect(t *testing.T) {
	v := must(t, 3, 4)
	before := v.String()
	_, e1 := api.New(1, 0)
	_, e2 := api.Mul(must(t, 1<<62, 1), must(t, 1<<62, 1))
	_, e3 := api.Add(must(t, 1<<62, 1), must(t, 1<<62, 1))
	ok := errors.Is(e1, api.ErrZeroDenominator) && errors.Is(e2, api.ErrMulOverflow) &&
		errors.Is(e3, api.ErrAddOverflow)
	if !ok || e1 == e2 || e2 == e3 || e1 == e3 {
		t.Fatalf("sentinels bad: %v %v %v", e1, e2, e3)
	}
	if v.String() != before {
		t.Fatalf("invariant4: %s mutated to %s after rejected ops", before, v)
	}
	if w, err := api.Add(v, v); err != nil || w.String() != "3/2" {
		t.Fatalf("invariant4: unusable after rejected ops: %v", err)
	}
}

// TestConcurrentRead 并发只读同一个 *Rat，String/IsZero/SelfCheck 均安全；不用 sleep。
func TestConcurrentRead(t *testing.T) {
	r := must(t, -6, 8)
	strs := make([]string, 64)
	var wg sync.WaitGroup
	for i := range strs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			strs[i] = r.String()
			if r.IsZero() {
				t.Error("IsZero returned true")
			}
			if err := api.SelfCheck(); err != nil {
				t.Errorf("concurrent SelfCheck: %v", err)
			}
		}(i)
	}
	wg.Wait()
	for i := range strs {
		if strs[i] != "-3/4" {
			t.Fatalf("goroutine %d: %s, want -3/4", i, strs[i])
		}
	}
}
