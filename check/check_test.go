package check_test

import (
	"errors"
	"slices"
	"sync"
	"testing"

	"ontology/check"
	"ontology/skip"
)

func fill(l *skip.List[int], n int) *skip.List[int] {
	for i := 0; i < n; i++ {
		_ = l.Insert(i, i)
	}
	return l
}

func TestReproducible(t *testing.T) {
	ser := func(seed uint64) []byte { return fill(skip.New[int](seed), 1000).Serialize() }
	if !slices.Equal(ser(1), ser(1)) || slices.Equal(ser(1), ser(2)) {
		t.Error("structure not reproducible or seed-insensitive")
	}
}

func TestRangeAgainstRef(t *testing.T) {
	ref, l := &check.Ref{}, skip.New[int](9)
	for i := 0; i < 500; i++ {
		k := (i * 37) % 500
		_, _ = l.Insert(k, k), ref.Insert(k, k)
	}
	for _, c := range []struct{ lo, hi int }{{0, 500}, {10, 90}, {250, 251}, {0, 1}, {499, 500}} {
		if got, err := l.Range(c.lo, c.hi); err != nil || !slices.Equal(got, ref.Range(c.lo, c.hi)) {
			t.Errorf("Range(%d,%d) mismatch", c.lo, c.hi)
		}
	}
}

func TestOps(t *testing.T) {
	l := skip.New[int](3)
	if keys, err := l.Range(0, 10); err != nil || len(keys) != 0 {
		t.Error("empty Range should be empty")
	}
	for _, k := range []int{5, 1, 9, 3} {
		_ = l.Insert(k, k*10)
	}
	for _, c := range []struct{ k, v int }{{1, 10}, {3, 30}, {5, 50}, {9, 90}, {4, -1}} {
		v, ok := l.Find(c.k)
		if ok != (c.v >= 0) || (ok && v != c.v) {
			t.Errorf("Find(%d) = %d, %v", c.k, v, ok)
		}
	}
	for _, k := range []int{3, 1, 5, 9} {
		_ = l.Delete(k)
	}
	if _, ok := l.Find(3); ok {
		t.Error("Find(3) after Delete should miss")
	}
	if l.Len() != 0 {
		t.Errorf("Len = %d after drain", l.Len())
	}
}

func TestErrors(t *testing.T) {
	_, rangeErr := skip.New[int](0).Range(2, 2)
	cases := [][2]error{
		{rangeErr, skip.ErrBadRange},
		{fill(skip.New[int](0), 1).Insert(0, 0), skip.ErrDuplicate},
		{fill(skip.New[int](0), 1).Delete(99), skip.ErrNotFound},
	}
	for i, c := range cases {
		if !errors.Is(c[0], c[1]) {
			t.Errorf("case %d: got %v, want %v", i, c[0], c[1])
		}
	}
}

func TestConcurrentAndBound(t *testing.T) {
	l := fill(skip.New[int](17), 10000)
	want, _ := l.Range(100, 300)
	before := l.Compares()
	var wg sync.WaitGroup
	wg.Add(16)
	for g := 0; g < 16; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < 10000; i++ {
				_, _ = l.Find(i)
			}
			if got, _ := l.Range(100, 300); !slices.Equal(got, want) {
				t.Error("concurrent Range mismatch")
			}
		}()
	}
	wg.Wait()
	if avg := float64(l.Compares()-before) / (16 * 10000); avg > 64 {
		t.Errorf("avg compares %.1f > 64", avg)
	}
}
