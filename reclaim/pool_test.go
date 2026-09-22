package reclaim

import (
	"errors"
	"testing"
)

func TestAcquireIsSmallestFirst(t *testing.T) {
	p := New(4)
	want := []int{0, 1, 2, 3}
	for _, w := range want {
		id, err := p.Acquire()
		if err != nil || id != w {
			t.Fatalf("acquire = %d,%v, want %d", id, err, w)
		}
	}
}

func TestReuseSmallestReleased(t *testing.T) {
	p := New(3)
	a, _ := p.Acquire()
	b, _ := p.Acquire()
	c, _ := p.Acquire()
	if _, err := p.Acquire(); !errors.Is(err, ErrExhausted) {
		t.Fatalf("full pool err = %v, want ErrExhausted", err)
	}
	if err := p.Release(b); err != nil {
		t.Fatal(err)
	}
	if id, _ := p.Acquire(); id != b {
		t.Fatalf("reuse = %d, want smallest free %d", id, b)
	}
	if err := p.Release(a); err != nil {
		t.Fatal(err)
	}
	if err := p.Release(c); err != nil {
		t.Fatal(err)
	}
	if got := p.Size(); got != 1 {
		t.Fatalf("size after releases = %d, want 1", got)
	}
}

func TestReleaseGuardsAndDeterministicSequence(t *testing.T) {
	p := New(3)
	if err := p.Release(7); !errors.Is(err, ErrNotHeld) {
		t.Fatalf("release out of range err = %v, want ErrNotHeld", err)
	}
	ids := make([]int, 3)
	for i := range ids {
		ids[i], _ = p.Acquire()
	}
	if err := p.Release(1); err != nil {
		t.Fatal(err)
	}
	if err := p.Release(1); !errors.Is(err, ErrNotHeld) {
		t.Fatalf("double release err = %v, want ErrNotHeld", err)
	}
	// Same operation sequence on a fresh pool yields the same ID stream.
	q := New(3)
	again := make([]int, 3)
	for i := range again {
		again[i], _ = q.Acquire()
	}
	for i := range ids {
		if ids[i] != again[i] {
			t.Fatal("allocation sequence not reproducible")
		}
	}
}
