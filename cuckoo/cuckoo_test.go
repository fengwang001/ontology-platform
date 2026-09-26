package cuckoo

import (
	"errors"
	"math/rand"
	"sync"
	"testing"
)

func TestInsertLookup(t *testing.T) {
	q, _ := New(4, 8)
	want := [][2][4]int{
		{{0, -1, -1, -1}, {-1, -1, -1, -1}}, {{0, 1, -1, -1}, {-1, -1, -1, -1}},
		{{0, 1, 2, -1}, {-1, -1, -1, -1}}, {{0, 1, 2, 3}, {-1, -1, -1, -1}},
		{{4, 1, 2, 3}, {-1, 0, -1, -1}}, {{4, 5, 2, 3}, {1, 0, -1, -1}},
		{{4, 5, 6, 3}, {1, 0, -1, 2}}, {{4, 5, 6, 7}, {1, 0, 3, 2}}}
	h2 := [4]int{1, 0, 3, 2}
	for i, x := range []int{0, 1, 2, 3, 4, 5, 6, 7} {
		if err := q.Insert(x); err != nil {
			t.Fatalf("insert %d: %v", x, err)
		} else if g := q.Dump4(); g != want[i] {
			t.Fatalf("after %d: %v", x, g)
		}
	}
	for x := 0; x <= 7; x++ {
		if ok, err := q.Lookup(x); !ok || err != nil || (x < 4 && q.t2[h2[x]] != x) {
			t.Fatalf("lookup %d = %v,%v", x, ok, err)
		}
	}
}
func TestTableFull(t *testing.T) {
	for _, c := range []struct{ k, last int }{{0, 3}, {1, 7}, {8, 7}} {
		q, _ := New(4, c.k)
		for x := 0; x <= c.last; x++ {
			if e := q.Insert(x); e != nil {
				t.Fatal(e)
			}
		}
		b := q.snap()
		if err := q.Insert(8); !errors.Is(err, ErrTableFull) || !equalSnap(b, q.snap()) {
			t.Fatalf("k=%d full/rollback: %v", c.k, err)
		}
	}
}
func TestFailureNoTrace(t *testing.T) {
	if _, err := New(0, 1); !errors.Is(err, ErrInvalidN) {
		t.Fatal(err)
	}
	for _, p := range [][2]error{{ErrDuplicate, ErrNotFound}, {ErrDuplicate, ErrTableFull},
		{ErrDuplicate, ErrInvalidN}, {ErrNotFound, ErrTableFull},
		{ErrNotFound, ErrInvalidN}, {ErrTableFull, ErrInvalidN}} {
		if errors.Is(p[0], p[1]) {
			t.Fatal("sentinel errors overlap")
		}
	}
	q, _ := New(8, 4)
	for _, x := range []int{0, 1, 8} {
		if e := q.Insert(x); e != nil {
			t.Fatal(e)
		}
	}
	rej := func(f func() error) {
		b := q.snap()
		if err := f(); err == nil || !equalSnap(b, q.snap()) {
			t.Fatal("rejected op must error and leave no trace")
		}
	}
	rej(func() error { return q.Insert(1) })
	rej(func() error { _, e := q.Lookup(7); return e })
	rej(func() error { return q.Delete(7) })
	if q.Insert(2) != nil || q.Delete(0) != nil || q.Len() != 3 {
		t.Fatal("table unusable after rejections")
	}
}
func TestNaiveReference(t *testing.T) {
	for _, c := range []struct {
		n, u, s int
		seed    int64
	}{{8, 16, 1500, 1}, {1024, 256, 1500, 2}} {
		q, _ := New(c.n, 16)
		ref := map[int]bool{}
		rng := rand.New(rand.NewSource(c.seed))
		for step := 0; step < c.s; step++ {
			x := rng.Intn(c.u)
			if rng.Intn(3) == 2 {
				err := q.Delete(x)
				if (ref[x] && err != nil) || (!ref[x] && !errors.Is(err, ErrNotFound)) {
					t.Fatal(err)
				}
				ref[x] = false
			} else if err := q.Insert(x); err == nil {
				if ref[x] {
					t.Fatal("duplicate accepted")
				}
				ref[x] = true
			} else if !errors.Is(err, ErrDuplicate) && !errors.Is(err, ErrTableFull) {
				t.Fatal(err)
			}
		}
		for k := 0; k < c.u; k++ {
			if ok, _ := q.Lookup(k); ok != ref[k] {
				t.Fatalf("seed %d key %d: %v vs %v", c.seed, k, ok, ref[k])
			}
		}
	}
}
func TestProbeCountO1(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		q, _ := New(4*m, 64)
		for i := 0; i < m; i++ {
			if e := q.Insert(i); e != nil {
				t.Fatal(e)
			}
		}
		for _, x := range []int{0, m / 2, 7*m + 1} {
			if _, err := q.Lookup(x); err != nil && !errors.Is(err, ErrNotFound) {
				t.Fatal(err)
			}
			if q.lastProbes.Load() != 2 {
				t.Fatalf("m=%d probes != 2", m)
			}
		}
	}
}
func TestConcurrentLookup(t *testing.T) {
	q, _ := New(4096, 64)
	for i := 0; i < 500; i++ {
		if e := q.Insert(i); e != nil {
			t.Fatal(e)
		}
	}
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for r := 0; r < 2000; r++ {
				x := (g*7 + r) % 1000
				ok, err := q.Lookup(x)
				if ok != (x < 500) || ((x < 500) != (err == nil)) {
					t.Errorf("lookup mismatch x=%d", x)
				}
				if g == 0 && r == 0 && (q.Len() != 500 || q.SelfCheck() != nil) {
					t.Error("Len/SelfCheck mismatch")
				}
			}
		}(g)
	}
	wg.Wait()
}
