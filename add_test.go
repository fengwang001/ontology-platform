package ontology

import (
	"errors"
	"math"
	"math/rand/v2"
	"testing"
)

func TestSpecialValues(t *testing.T) {
	h, err := New(0, 1, 3)
	if err != nil {
		t.Fatal(err)
	}

	if err := h.Add(0); err != nil {
		t.Fatalf("Add(+0) error = %v", err)
	}
	if err := h.Add(math.Copysign(0, -1)); err != nil {
		t.Fatalf("Add(-0) error = %v", err)
	}
	if err := h.Add(math.Inf(-1)); err != nil {
		t.Fatalf("Add(-Inf) error = %v", err)
	}
	if err := h.Add(math.Inf(1)); err != nil {
		t.Fatalf("Add(+Inf) error = %v", err)
	}
	if err := h.Add(math.NaN()); !errors.Is(err, ErrNaNSample) {
		t.Fatalf("Add(NaN) error = %v, want %v", err, ErrNaNSample)
	}

	s := h.Snapshot()
	if got := s.Buckets[0]; got != 2 {
		t.Fatalf("bucket 0 = %d, want 2 for signed zeros", got)
	}
	if s.Underflow != 1 || s.Overflow != 1 || s.Skipped != 1 {
		t.Fatalf("counts = under:%d over:%d skipped:%d, want 1/1/1",
			s.Underflow, s.Overflow, s.Skipped)
	}
	if s.Added != 5 {
		t.Fatalf("added = %d, want 5", s.Added)
	}
}

func TestCountingInvariant(t *testing.T) {
	h, err := New(-1.5, 2.5, 7)
	if err != nil {
		t.Fatal(err)
	}

	rng := rand.New(rand.NewPCG(1, 2))
	var attempted uint64
	for i := 0; i < 20000; i++ {
		x := rng.Float64()*6 - 2
		if i%17 == 0 {
			x = math.NaN()
		}
		if err := h.Add(x); err != nil && !errors.Is(err, ErrNaNSample) {
			t.Fatalf("Add(%v) unexpected error %v", x, err)
		}
		attempted++
		assertInvariant(t, h, attempted)
	}
}

func assertInvariant(t *testing.T, h *Histogram, attempted uint64) {
	t.Helper()

	s := h.Snapshot()
	var bucketTotal uint64
	for _, count := range s.Buckets {
		bucketTotal += count
	}
	got := bucketTotal + s.Underflow + s.Overflow + s.Skipped
	if got != attempted || got != s.Added {
		t.Fatalf("invariant = %d, attempted = %d, added = %d", got, attempted, s.Added)
	}
}
