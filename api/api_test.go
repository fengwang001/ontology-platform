package api_test

import (
	"errors"
	"math/rand"
	"sync"
	"testing"

	"ontology/api"
	"ontology/hashk"
)

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func randElems(r *rand.Rand, n int) [][]byte {
	out := make([][]byte, n)
	for i := range out {
		b := make([]byte, 1+r.Intn(16))
		r.Read(b)
		out[i] = b
	}
	return out
}

func shadowOf(elems [][]byte, m, k int) []bool {
	s := make([]bool, m)
	for _, e := range elems {
		for _, p := range hashk.Positions(e, m, k) {
			s[p] = true
		}
	}
	return s
}

func TestNoFalseNegative(t *testing.T) {
	for _, c := range []struct{ m, k, n int }{{64, 3, 20}, {256, 4, 100}, {1024, 5, 300}} {
		r := rand.New(rand.NewSource(int64(c.m)))
		f, _ := api.New(c.m, c.k, c.n)
		for _, e := range randElems(r, c.n) {
			mustOK(t, f.Add(e))
			if ok, _ := f.Test(e); !ok {
				t.Fatalf("m=%d: false negative on %q", c.m, e)
			}
		}
	}
}

func TestNaiveReference(t *testing.T) {
	for _, c := range []struct{ m, k, n int }{{32, 2, 10}, {128, 3, 50}, {512, 6, 200}} {
		r := rand.New(rand.NewSource(int64(c.k)))
		elems := randElems(r, c.n)
		f, _ := api.New(c.m, c.k, c.n)
		for _, e := range elems {
			mustOK(t, f.Add(e))
		}
		shadow := shadowOf(elems, c.m, c.k)
		for _, p := range randElems(r, 2*c.n) {
			want := true
			for _, pos := range hashk.Positions(p, c.m, c.k) {
				want = want && shadow[pos]
			}
			if got, _ := f.Test(p); got != want {
				t.Fatalf("m=%d probe %q: got %v, want %v", c.m, p, got, want)
			}
		}
	}
}

func TestFalsePositiveControlled(t *testing.T) {
	const m, k = 16, 3 // 小位数组，必然出现假阳性
	r := rand.New(rand.NewSource(7))
	elems := randElems(r, 8)
	f, _ := api.New(m, k, len(elems))
	for _, e := range elems {
		mustOK(t, f.Add(e))
	}
	shadow := shadowOf(elems, m, k)
	fps := 0
	for _, p := range randElems(r, 500) {
		got, _ := f.Test(p)
		allSet := true
		for _, pos := range hashk.Positions(p, m, k) {
			allSet = allSet && shadow[pos]
		}
		if got != allSet {
			t.Fatalf("probe %q: got %v, shadow %v", p, got, allSet)
		}
		if got {
			fps++
		}
	}
	if fps == 0 {
		t.Fatal("expected false positives with m=16")
	}
}

func TestFailureNoTrace(t *testing.T) {
	for _, c := range []struct{ m, k, max int }{{0, 1, 1}, {1, 0, 1}, {-3, 2, 1}, {4, 2, -1}} {
		if _, err := api.New(c.m, c.k, c.max); !errors.Is(err, api.ErrInvalidParam) {
			t.Fatalf("New(%d,%d,%d) err = %v", c.m, c.k, c.max, err)
		}
	}
	f, err := api.New(64, 3, 1)
	mustOK(t, err)
	mustOK(t, f.Add([]byte("x")))
	before, snap := f.Count(), f.Snapshot()
	if err := f.Add(nil); !errors.Is(err, api.ErrEmptyElement) {
		t.Fatalf("empty add err = %v", err)
	}
	if _, err := f.Test(nil); !errors.Is(err, api.ErrEmptyElement) {
		t.Fatalf("empty test err = %v", err)
	}
	if err := f.Add([]byte("y")); !errors.Is(err, api.ErrCapacity) {
		t.Fatalf("overflow err = %v", err)
	}
	if errors.Is(api.ErrEmptyElement, api.ErrCapacity) || errors.Is(api.ErrCapacity, api.ErrInvalidParam) {
		t.Fatal("sentinel errors must be distinct")
	}
	if f.Count() != before {
		t.Fatal("rejected ops changed count")
	}
	for i, b := range f.Snapshot() {
		if b != snap[i] {
			t.Fatal("rejected ops changed bits")
		}
	}
	if ok, _ := f.Test([]byte("x")); !ok {
		t.Fatal("filter unusable after rejections")
	}
}

func TestSelfCheck(t *testing.T) {
	f, _ := api.New(64, 3, 8)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = f.SelfCheck()
		}()
	}
	wg.Wait()
	mustOK(t, f.SelfCheck())
}
