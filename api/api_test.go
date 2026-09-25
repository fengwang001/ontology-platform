package api_test

import (
	"errors"
	"math"
	"slices"
	"sync"
	"testing"

	"ontology/api"
	"ontology/hash"
)

func testHashes(t *testing.T) []api.Hash {
	hs := make([]api.Hash, 4)
	for i, p := range [][3]uint64{{2, 3, 11}, {3, 1, 11}, {5, 4, 11}, {7, 2, 11}} {
		hs[i], _ = hash.New(p[0], p[1], p[2]) // 常量参数必合法
	}
	return hs
}

func build(t *testing.T, hs []api.Hash, xs ...uint64) *api.Sketch {
	s, err := api.NewSketch(len(hs), hs)
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range xs {
		s.Add(x)
	}
	return s
}

func naive(hs []api.Hash, sa, sb []uint64) float64 {
	eq := 0
	for _, h := range hs {
		ma, mb := uint64(math.MaxUint64), uint64(math.MaxUint64)
		for _, x := range sa {
			ma = min(ma, h.Eval(x))
		}
		for _, x := range sb {
			mb = min(mb, h.Eval(x))
		}
		if ma == mb {
			eq++
		}
	}
	return float64(eq) / float64(len(hs))
}

func TestMatchesNaive(t *testing.T) {
	hs := testHashes(t)
	cases := []struct {
		name string
		a, b []uint64
	}{
		{"spec", []uint64{1, 4, 7}, []uint64{1, 4, 8, 9}},
		{"disjoint", []uint64{2, 3}, []uint64{5, 6}},
		{"identical", []uint64{1, 2, 3}, []uint64{1, 2, 3}},
		{"subset", []uint64{1, 4}, []uint64{1, 4, 8, 9}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := api.Estimate(build(t, hs, c.a...), build(t, hs, c.b...))
			if err != nil {
				t.Fatal(err)
			}
			if want := naive(hs, c.a, c.b); got != want {
				t.Fatalf("Estimate=%v, naive=%v", got, want)
			}
		})
	}
}

func TestDeterministicAndSymmetric(t *testing.T) {
	hs := testHashes(t)
	a1, a2 := build(t, hs, 1, 4, 7), build(t, hs, 1, 4, 7)
	b := build(t, hs, 1, 4, 8, 9)
	if !slices.Equal(a1.Signature(), a2.Signature()) {
		t.Fatal("two builds differ")
	}
	ab, err1 := api.Estimate(a1, b)
	ba, err2 := api.Estimate(b, a1)
	if err1 != nil || err2 != nil || ab != ba || ab != 0.25 {
		t.Fatalf("ab=%v ba=%v err=%v,%v; want 0.25", ab, ba, err1, err2)
	}
}

func TestFailureLeavesNoTrace(t *testing.T) {
	hs := testHashes(t)
	a, b := build(t, hs, 1, 4, 7), build(t, hs, 1, 4, 8, 9)
	beforeA, beforeB := a.Signature(), b.Signature()
	_, e1 := api.NewSketch(0, hs)
	_, e2 := hash.New(2, 1, 1)    // p <= 1
	_, e2b := hash.New(11, 1, 11) // a ≡ 0 (mod p)
	_, e3 := api.Estimate(a, build(t, hs))
	_, e4 := api.Estimate(a, build(t, hs[:1], 1))
	for _, want := range []struct {
		got, sentinel error
	}{{e1, api.ErrBadK}, {e2, api.ErrBadParams}, {e2b, api.ErrBadParams}, {e3, api.ErrEmpty}, {e4, api.ErrHashMismatch}} {
		if !errors.Is(want.got, want.sentinel) {
			t.Fatalf("got %v, want %v", want.got, want.sentinel)
		}
	}
	errs := []error{api.ErrBadK, api.ErrBadParams, api.ErrEmpty, api.ErrHashMismatch}
	for i := range errs { // 四类错误互不相同
		if slices.ContainsFunc(errs[i+1:], func(e error) bool { return errors.Is(errs[i], e) }) {
			t.Fatal("sentinel errors not distinct")
		}
	}
	if !slices.Equal(a.Signature(), beforeA) || !slices.Equal(b.Signature(), beforeB) {
		t.Fatal("rejected ops mutated state")
	}
	if v, err := api.Estimate(a, b); err != nil || v != 0.25 { // 被拒后仍可用
		t.Fatalf("sketch unusable after rejections: %v %v", v, err)
	}
}

func TestConcurrentReadOnly(t *testing.T) {
	hs := testHashes(t)
	a, b := build(t, hs, 1, 4, 7), build(t, hs, 1, 4, 8, 9)
	want, err := api.Estimate(a, b)
	if err != nil {
		t.Fatal(err)
	}
	const n = 64
	var wg sync.WaitGroup
	same := make(chan bool, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := api.Estimate(a, b)
			_ = a.Signature()
			same <- err == nil && got == want && api.SelfCheck() == nil
		}()
	}
	wg.Wait()
	close(same)
	for ok := range same {
		if !ok {
			t.Fatal("concurrent Estimate results differ")
		}
	}
}

func TestSelfCheck(t *testing.T) {
	if err := api.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
