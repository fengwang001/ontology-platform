package check

import (
	"bytes"
	"errors"
	"math/rand/v2"
	"sync"
	"testing"

	"ontology/skip"
)

func build(t testing.TB, seed uint64, keys []int) *skip.SkipList[int] {
	t.Helper()
	s := skip.New[int](seed)
	for _, k := range keys {
		if err := s.Insert(k, k*2); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func evens(n int) []int {
	ks := make([]int, n)
	for i := range ks {
		ks[i] = i * 2
	}
	return ks
}

func TestReproducible(t *testing.T) {
	ks := make([]int, 1000)
	for i := range ks {
		ks[i] = i * 3
	}
	a, b, c := build(t, 42, ks), build(t, 42, ks), build(t, 7, ks)
	if !bytes.Equal(a.MarshalStructure(), b.MarshalStructure()) {
		t.Fatal("same seed => different structures")
	}
	if bytes.Equal(a.MarshalStructure(), c.MarshalStructure()) {
		t.Fatal("different seeds => identical")
	}
}

func TestRangeOrdered(t *testing.T) {
	s := build(t, 1, evens(100))
	for _, tc := range [][2]int{{0, 50}, {-5, 20}, {100, 200}, {0, 199}, {190, 198}} {
		got, err := s.Range(tc[0], tc[1])
		if err != nil {
			t.Fatal(err)
		}
		want := 0
		for k := tc[0]; k < tc[1]; k++ {
			if k%2 == 0 && k >= 0 && k < 200 {
				want++
			}
		}
		if len(got) != want {
			t.Fatalf("%v len %d want %d", tc, len(got), want)
		}
		for i := 1; i < len(got); i++ {
			if got[i].Key <= got[i-1].Key {
				t.Fatalf("%v not ascending at %d", tc, i)
			}
		}
		for _, kv := range got {
			if kv.Key < tc[0] || kv.Key >= tc[1] || kv.Val != kv.Key*2 {
				t.Fatalf("%v bad elem %+v", tc, kv)
			}
		}
	}
}

func TestCRUDErrorsBoundaries(t *testing.T) {
	s := build(t, 3, []int{1, 2, 3})
	for _, tc := range []struct {
		name string
		errW bool
		fn   func() error
	}{
		{"hit", false, func() error {
			v, ok := s.Find(2)
			if !ok || v != 4 {
				return errors.New("x")
			}
			return nil
		}},
		{"miss", false, func() error {
			if _, ok := s.Find(9); ok {
				return errors.New("x")
			}
			return nil
		}},
		{"dup", true, func() error { return s.Insert(2, 0) }},
		{"del", false, func() error { return s.Delete(2) }},
		{"delmiss", true, func() error { return s.Delete(2) }},
		{"rangeEq", true, func() error { _, e := s.Range(1, 1); return e }},
		{"rangeRev", true, func() error { _, e := s.Range(5, 1); return e }},
		{"len", false, func() error {
			if s.Len() != 2 {
				return errors.New("x")
			}
			return nil
		}},
	} {
		err := tc.fn()
		if (err != nil) != tc.errW {
			t.Errorf("%s: %v", tc.name, err)
		}
	}
	if !errors.Is(mustErr(func() error { _, e := s.Range(2, 2); return e }), skip.ErrBadRange) {
		t.Fatal("want ErrBadRange")
	}
	empty := skip.New[int](1)
	if r, err := empty.Range(0, 10); err != nil || len(r) != 0 {
		t.Fatal("empty range")
	}
	for k := 0; k < 10; k++ {
		if err := empty.Insert(k, k); err != nil {
			t.Fatal(err)
		}
	}
	for k := 0; k < 10; k++ {
		if err := empty.Delete(k); err != nil {
			t.Fatal(err)
		}
	}
	if empty.Len() != 0 {
		t.Fatalf("len %d", empty.Len())
	}
}

func mustErr(f func() error) error { return f() }

func TestComparisonBound(t *testing.T) {
	keys := rand.New(rand.NewPCG(1, 2)).Perm(10000)
	s := build(t, 11, keys)
	s.ResetCmp()
	for _, k := range keys {
		if _, ok := s.Find(k); !ok {
			t.Fatal("miss")
		}
	}
	s.Find(-1)
	if avg := float64(s.CmpCount()) / float64(len(keys)+1); avg > 64 {
		t.Fatalf("avg %.2f", avg)
	} else {
		t.Logf("avg comparisons %.2f", avg)
	}
}

func TestConcurrentReads(t *testing.T) {
	s := build(t, 5, evens(1000))
	full, err := s.Range(0, 2000)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for w := 0; w < 16; w++ {
		wg.Add(1)
		go func(off int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				k := (i*7 + off) % 1000 * 2
				if v, ok := s.Find(k); !ok || v != k*2 {
					t.Errorf("find %d", k)
					return
				}
				got, err := s.Range(0, 2000)
				if err != nil || len(got) != len(full) || got[0] != full[0] || got[len(got)-1] != full[len(full)-1] {
					t.Errorf("range mismatch")
					return
				}
			}
		}(w)
	}
	wg.Wait()
}
