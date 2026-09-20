package ontology

import (
	"errors"
	"fmt"
	"slices"
	"testing"
)

// k <= 0 must return a detectable error, not panic.
func TestInvalidCapacity(t *testing.T) {
	for _, k := range []int{0, -1, -100} {
		r, err := New(k, 1)
		if !errors.Is(err, ErrInvalidCapacity) {
			t.Fatalf("New(%d): expected ErrInvalidCapacity, got %v", k, err)
		}
		if r != nil {
			t.Fatalf("New(%d): expected nil reservoir", k)
		}
	}
}

// Stream shorter than k: all elements returned in arrival order.
func TestStreamShorterThanK(t *testing.T) {
	r, err := New(10, 5)
	if err != nil {
		t.Fatal(err)
	}
	items := []string{"a", "b", "c", "d"}
	feed(t, r, items, 1)
	got := r.Sample()
	if len(got) != len(items) {
		t.Fatalf("expected %d elements, got %d", len(items), len(got))
	}
	// Every arrival must be present.
	for _, it := range items {
		if !slices.Contains(got, it) {
			t.Fatalf("missing element %q in %v", it, got)
		}
	}
	if r.Len() != len(items) {
		t.Fatalf("Len() = %d, want %d", r.Len(), len(items))
	}
}

// Stream length exactly k: all elements retained.
func TestStreamExactlyK(t *testing.T) {
	const k = 25
	r, err := New(k, 5)
	if err != nil {
		t.Fatal(err)
	}
	items := streamOf(k, 2)
	feed(t, r, items, 2)
	got := r.Sample()
	if len(got) != k {
		t.Fatalf("expected %d elements, got %d", k, len(got))
	}
	for _, it := range items {
		if !slices.Contains(got, it) {
			t.Fatalf("missing element %q", it)
		}
	}
}

// k = 1 is a legal, common case.
func TestCapacityOne(t *testing.T) {
	r, err := New(1, 3)
	if err != nil {
		t.Fatal(err)
	}
	feed(t, r, streamOf(1000, 1), 1)
	got := r.Sample()
	if len(got) != 1 {
		t.Fatalf("expected exactly 1 element, got %d", len(got))
	}
	if r.Len() != 1 {
		t.Fatalf("Len() = %d, want 1", r.Len())
	}
	if r.Total() != 1000 {
		t.Fatalf("Total() = %d, want 1000", r.Total())
	}
}

// Invalid weights (zero, negative, fractional, NaN, Inf) must be
// rejected with a detectable error and must not enter the reservoir.
func TestInvalidWeightsRejected(t *testing.T) {
	r, err := New(5, 11)
	if err != nil {
		t.Fatal(err)
	}
	bad := []float64{0, -1, -2.5, 1.5, 0.999, 2.0001}
	for i, w := range bad {
		err := r.Add(fmt.Sprintf("bad-%d", i), w)
		if !errors.Is(err, ErrInvalidWeight) {
			t.Fatalf("Add(weight=%v): expected ErrInvalidWeight, got %v", w, err)
		}
	}
	if r.Rejected() != uint64(len(bad)) {
		t.Fatalf("Rejected() = %d, want %d", r.Rejected(), len(bad))
	}
	if r.Len() != 0 {
		t.Fatalf("rejected elements entered reservoir: Len() = %d", r.Len())
	}
	if r.Total() != 0 {
		t.Fatalf("Total() = %d, want 0", r.Total())
	}
	if r.RandConsumed() != 0 {
		t.Fatalf("rejected elements consumed randomness: %d", r.RandConsumed())
	}

	// A valid element afterwards still works and counters split cleanly.
	if err := r.Add("good", 4); err != nil {
		t.Fatal(err)
	}
	if r.Total() != 1 || r.Rejected() != uint64(len(bad)) || r.Len() != 1 {
		t.Fatalf("counters wrong: total=%d rejected=%d len=%d",
			r.Total(), r.Rejected(), r.Len())
	}
}

// The reservoir must never exceed k elements, even on a million-item
// stream, and must never buffer the stream.
func TestMillionStreamConstantMemory(t *testing.T) {
	const k = 32
	const n = 1_000_000
	r, err := New(k, 17)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		if err := r.Add(fmt.Sprintf("e%d", i), 1+(float64(i%7))); err != nil {
			t.Fatal(err)
		}
		if i%100_000 == 0 && r.Len() > k {
			t.Fatalf("reservoir exceeded capacity at i=%d: %d", i, r.Len())
		}
	}
	if r.Len() != k {
		t.Fatalf("Len() = %d, want %d", r.Len(), k)
	}
	if r.Total() != n {
		t.Fatalf("Total() = %d, want %d", r.Total(), n)
	}
	if r.RandConsumed() != n {
		t.Fatalf("RandConsumed() = %d, want %d", r.RandConsumed(), n)
	}
	if got := len(r.Sample()); got != k {
		t.Fatalf("sample size = %d, want %d", got, k)
	}
}
