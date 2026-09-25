package check

import (
	"errors"
	"slices"
	"sync"
	"testing"

	"ontology/skip"
)

func TestSemantics(t *testing.T) {
	cases := []struct {
		name   string
		keys   []int
		lo, hi int
		rerr   error
	}{
		{"empty", nil, 0, 10, nil},
		{"dups", []int{9, 3, 7, 3, 1}, 2, 8, nil},
		{"neg", []int{-5, 0, 5, -10, 10}, -6, 6, nil},
		{"badrange", nil, 5, 5, skip.ErrBadRange},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l, r := skip.New[int](42), Ref{}
			for _, k := range c.keys {
				_, dup := r[k]
				if err := l.Insert(k, k*10); dup != errors.Is(err, skip.ErrDuplicate) {
					t.Fatalf("Insert(%d) err=%v", k, err)
				}
				r[k] = k * 10
			}
			if l.Len() != len(r) {
				t.Fatalf("Len=%d want %d", l.Len(), len(r))
			}
			for k := -11; k <= 11; k++ {
				v, ok := l.Find(k)
				rv, rok := r[k]
				if ok != rok || v != rv {
					t.Fatalf("Find(%d)=%d,%v", k, v, ok)
				}
			}
			got, err := l.Range(c.lo, c.hi)
			if !errors.Is(err, c.rerr) || !slices.Equal(got, r.Keys(c.lo, c.hi)) {
				t.Fatalf("Range=%v err=%v", got, err)
			}
			for k := range r {
				l.Delete(k)
				if _, ok := l.Find(k); ok {
					t.Fatal("Find hit after delete")
				}
			}
			if l.Len() != 0 {
				t.Fatal("Len != 0 after deletes")
			}
		})
	}
}
func TestReproducible(t *testing.T) {
	keys := iota(1000)
	a, b, c := build(1, keys), build(1, keys), build(2, keys)
	if a.Serialize() != b.Serialize() {
		t.Fatal("same seed: structure differs")
	}
	if a.Serialize() == c.Serialize() {
		t.Fatal("different seeds: identical structure")
	}
}
func TestComplexity(t *testing.T) {
	const n = 10000
	l := build(7, iota(n))
	base := l.Comparisons()
	for _, k := range iota(n) {
		l.Find(k)
	}
	if avg := float64(l.Comparisons()-base) / n; avg > 64 {
		t.Fatalf("avg comparisons %.2f > 64", avg)
	}
}
func TestConcurrentRead(t *testing.T) {
	l := build(5, iota(500))
	want, _ := l.Range(0, 500)
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := 0; k < 500; k++ {
				if v, ok := l.Find(k); !ok || v != k {
					t.Error("Find mismatch")
				}
			}
			if got, _ := l.Range(0, 500); !slices.Equal(got, want) {
				t.Error("Range mismatch")
			}
		}()
	}
	wg.Wait()
}
