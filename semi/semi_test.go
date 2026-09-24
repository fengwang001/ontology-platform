package semi

import (
	"errors"
	"fmt"
	"maps"
	"math/rand"
	"slices"
	"testing"
)

func sp(s string) *string { return &s }

// TestCheckedIndependentOfM pins the O(1) key-indexed locate: the left rows
// examined by AddRight equal the rows under that key, independent of m.
func TestCheckedIndependentOfM(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		e, _ := New(m)
		for i := 0; i < m; i++ {
			if err := e.AddLeft(int64(i), sp(fmt.Sprintf("k%d", i))); err != nil {
				t.Fatal(err)
			}
		}
		if err := e.AddRight(sp(fmt.Sprintf("k%d", m-1))); err != nil {
			t.Fatal(err)
		}
		if e.checked != 1 {
			t.Fatalf("m=%d: checked=%d, want 1 (key-indexed locate)", m, e.checked)
		}
	}
}

// TestBatchConsistency pins invariant 1: incremental view == batch recompute.
func TestBatchConsistency(t *testing.T) {
	for _, seed := range []int64{1, 2, 3, 7, 42} {
		r := rand.New(rand.NewSource(seed))
		e, _ := New(64)
		keys := []string{"a", "b", "c", "d"}
		for i := 0; i < 300; i++ {
			k := sp(keys[r.Intn(len(keys))])
			switch r.Intn(5) {
			case 0:
				_ = e.AddLeft(int64(i), k)
			case 1:
				_ = e.AddLeft(int64(i), nil)
			case 2:
				_ = e.DelLeft(int64(r.Intn(i + 1)))
			case 3:
				_ = e.AddRight(k)
			default:
				_ = e.DelRight(k)
			}
			if got, want := e.View(), e.BatchView(); !slices.Equal(got, want) {
				t.Fatalf("seed=%d step=%d: view %v != batch %v", seed, i, got, want)
			}
		}
	}
}

// TestNoDuplicate pins invariant 2: ref > 1 never duplicates a left row.
func TestNoDuplicate(t *testing.T) {
	e, _ := New(8)
	for id := int64(1); id <= 3; id++ {
		_ = e.AddLeft(id, sp("a"))
	}
	for range 5 {
		_ = e.AddRight(sp("a"))
	}
	if got := e.View(); !slices.Equal(got, []int64{1, 2, 3}) {
		t.Fatalf("ref=5: view=%v, want [1 2 3]", got)
	}
	for range 5 {
		_ = e.DelRight(sp("a"))
	}
	if got := e.View(); len(got) != 0 {
		t.Fatalf("drained: view=%v, want empty", got)
	}
}

// TestRefNonNegativeNullNoMatch pins invariant 3.
func TestRefNonNegativeNullNoMatch(t *testing.T) {
	e, _ := New(4)
	for _, k := range []*string{sp("x"), nil} {
		if err := e.DelRight(k); !errors.Is(err, ErrRefNegative) {
			t.Fatalf("key=%v: want ErrRefNegative, got %v", k, err)
		}
	}
	_ = e.AddRight(nil)
	_ = e.AddLeft(1, nil)
	if got := e.View(); len(got) != 0 {
		t.Fatalf("NULL key lit up: %v", got)
	}
}

// TestFailureNoTrace pins invariant 4: rejected ops mutate nothing.
func TestFailureNoTrace(t *testing.T) {
	cases := []struct {
		name string
		op   func(*Engine) error
		want error
	}{
		{"bad config", func(*Engine) error { _, err := New(0); return err }, ErrBadMaxLeft},
		{"dup id", func(e *Engine) error { return e.AddLeft(1, sp("z")) }, ErrLeftExists},
		{"over limit", func(e *Engine) error { return e.AddLeft(9, sp("z")) }, ErrTooManyLeft},
		{"missing id", func(e *Engine) error { return e.DelLeft(99) }, ErrLeftNotFound},
		{"negative ref", func(e *Engine) error { return e.DelRight(sp("z")) }, ErrRefNegative},
		{"negative NULL", func(e *Engine) error { return e.DelRight(nil) }, ErrRefNegative},
	}
	for _, c := range cases {
		e, _ := New(2)
		_ = e.AddLeft(1, sp("a"))
		_ = e.AddLeft(2, sp("a"))
		_ = e.AddRight(sp("a"))
		l0, r0, ret0, n0 := maps.Clone(e.left), maps.Clone(e.ref), maps.Clone(e.retained), e.nilRef
		if err := c.op(e); !errors.Is(err, c.want) {
			t.Fatalf("%s: want %v, got %v", c.name, c.want, err)
		}
		if !maps.Equal(e.left, l0) || !maps.Equal(e.ref, r0) || !maps.Equal(e.retained, ret0) || e.nilRef != n0 {
			t.Fatalf("%s: rejected op mutated state", c.name)
		}
	}
}
