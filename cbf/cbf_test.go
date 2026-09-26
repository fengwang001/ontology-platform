package cbf

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"
)

// indicesOf mirrors h_j(x) = (j*x) mod m for the naive replay model.
func indicesOf(m, k int, key int64) []int {
	idx := make([]int, k)
	r := key % int64(m)
	for j := 1; j <= k; j++ {
		idx[j-1] = int((int64(j) * r) % int64(m))
	}
	return idx
}

func TestNoFalseNegative(t *testing.T) {
	for _, m := range []int{8, 64, 1000} {
		f, _ := New(m, 4)
		for key := int64(0); key < 50; key++ {
			if err := f.Add(key); err != nil {
				t.Fatal(err)
			}
		}
		for key := int64(0); key < 50; key++ {
			if v, _ := f.Query(key); v < 1 {
				t.Fatalf("m=%d Query(%d)=%d after Add", m, key, v)
			}
		}
	}
}

func TestExactRemoval(t *testing.T) {
	for _, key := range []int64{0, 1, 3, 6, 8} {
		f, _ := New(8, 3)
		if f.Add(key) != nil || f.Remove(key) != nil {
			t.Fatalf("key=%d add/remove failed", key)
		}
		for i, v := range f.Snapshot() {
			if v != 0 {
				t.Fatalf("key=%d counter c[%d]=%d, want 0", key, i, v)
			}
		}
	}
}

func TestNaiveReplay(t *testing.T) {
	cases := []struct{ m, k, n int }{{8, 3, 200}, {64, 5, 1000}, {257, 7, 3000}}
	for ci, c := range cases {
		rng := rand.New(rand.NewSource(int64(ci + 1)))
		f, _ := New(c.m, c.k)
		model := make([]int64, c.m)
		for s := 0; s < c.n; s++ {
			key := rng.Int63n(20) // small range forces collisions
			idx := indicesOf(c.m, c.k, key)
			zero := false
			for _, i := range idx {
				if model[i] == 0 {
					zero = true
				}
			}
			if rng.Intn(2) == 0 {
				if err := f.Add(key); err != nil {
					t.Fatal(err)
				}
				for _, i := range idx {
					model[i]++
				}
			} else {
				err := f.Remove(key)
				if zero {
					if !errors.Is(err, ErrNotPresent) {
						t.Fatalf("want ErrNotPresent, got %v", err)
					}
				} else {
					if err != nil {
						t.Fatal(err)
					}
					for _, i := range idx {
						model[i]--
					}
				}
			}
		}
		if fmt.Sprint(f.Snapshot()) != fmt.Sprint(model) {
			t.Fatalf("m=%d state %v, want naive %v", c.m, f.Snapshot(), model)
		}
	}
}

func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	f, _ := New(8, 3)
	f.Add(3)
	cases := []struct {
		name string
		run  func(*Filter) error
		want error
	}{
		{"add negative", func(f *Filter) error { return f.Add(-1) }, ErrInvalidKey},
		{"query negative", func(f *Filter) error { _, e := f.Query(-2); return e }, ErrInvalidKey},
		{"remove absent", func(f *Filter) error { return f.Remove(5) }, ErrNotPresent},
	}
	for _, c := range cases {
		before := f.Snapshot()
		if !errors.Is(c.run(f), c.want) {
			t.Fatalf("%s: wrong error", c.name)
		}
		if fmt.Sprint(f.Snapshot()) != fmt.Sprint(before) {
			t.Fatalf("%s changed state: %v", c.name, f.Snapshot())
		}
	}
	if _, err := New(0, 3); !errors.Is(err, ErrInvalidParams) {
		t.Fatalf("m<=0: %v", err)
	}
	if _, err := New(8, 0); !errors.Is(err, ErrInvalidParams) {
		t.Fatalf("k<=0: %v", err)
	}
	if ErrInvalidParams == ErrInvalidKey || ErrInvalidKey == ErrNotPresent {
		t.Fatal("sentinel errors must be distinct")
	}
}

func TestSelfCheck(t *testing.T) {
	f, _ := New(8, 3)
	if !f.SelfCheck() {
		t.Fatal("SelfCheck must pass for a correct implementation")
	}
}

func TestQueryTouchesOnlyKCounters(t *testing.T) {
	cases := []struct{ m, k int }{{100, 3}, {1000, 5}, {10000, 7}}
	for _, c := range cases {
		f, _ := New(c.m, c.k)
		for key := 0; key < c.m; key++ {
			if err := f.Add(int64(key)); err != nil {
				t.Fatal(err)
			}
		}
		for _, q := range []int64{0, int64(c.m / 2), int64(c.m - 1)} {
			f.Query(q)
			if got := f.lastQueryTouches.Load(); got != int64(c.k) {
				t.Fatalf("m=%d Query(%d) touched %d counters, want exactly k=%d", c.m, q, got, c.k)
			}
		}
	}
}
