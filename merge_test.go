package ontology

import (
	"errors"
	"math"
	"slices"
	"strings"
	"testing"
)

func TestBucketsSnapshotIsCopy(t *testing.T) {
	h, err := New(0, 1, 3)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Add(0); err != nil {
		t.Fatal(err)
	}

	first := h.Buckets()
	first[0] = 999
	second := h.Buckets()
	if second[0] != 1 {
		t.Fatalf("mutating Buckets() changed internal state: %v", second)
	}

	snap := h.Snapshot()
	snap.Buckets[0] = 888
	if h.Buckets()[0] != 1 {
		t.Fatal("mutating Snapshot().Buckets changed internal state")
	}
}

func TestMergeSameParameters(t *testing.T) {
	first, err := New(0, 1, 3)
	if err != nil {
		t.Fatal(err)
	}
	second, err := New(0, 1, 3)
	if err != nil {
		t.Fatal(err)
	}

	for _, x := range []float64{0, 0.5, 1, math.NaN()} {
		if err := first.Add(x); err != nil && !errors.Is(err, ErrNaNSample) {
			t.Fatal(err)
		}
	}
	for _, x := range []float64{0.9, 1.0 / 3.0, math.Inf(-1)} {
		if err := second.Add(x); err != nil {
			t.Fatal(err)
		}
	}

	firstBefore := first.Snapshot()
	secondBefore := second.Snapshot()
	merged, err := first.Merge(second)
	if err != nil {
		t.Fatal(err)
	}

	assertSnapshotEqual(t, first.Snapshot(), firstBefore)
	assertSnapshotEqual(t, second.Snapshot(), secondBefore)

	got := merged.Snapshot()
	wantBuckets := []uint64{1, 2, 1}
	if !slices.Equal(got.Buckets, wantBuckets) {
		t.Fatalf("merged buckets = %v, want %v", got.Buckets, wantBuckets)
	}
	if got.Underflow != 1 || got.Overflow != 1 || got.Skipped != 1 || got.Added != 7 {
		t.Fatalf("merged counts = %+v", got)
	}
}

func TestMergeDifferentParameters(t *testing.T) {
	first, _ := New(0, 1, 3)
	second, _ := New(-1, 2, 7)

	_, err := first.Merge(second)
	if !errors.Is(err, ErrHistogramMismatch) {
		t.Fatalf("Merge error = %v, want %v", err, ErrHistogramMismatch)
	}
	message := err.Error()
	for _, want := range []string{"(0,1,3)", "(-1,2,7)"} {
		if !strings.Contains(message, want) {
			t.Fatalf("error %q does not contain %s", message, want)
		}
	}
}

func assertSnapshotEqual(t *testing.T, got, want Snapshot) {
	t.Helper()
	if got.Lo != want.Lo || got.Hi != want.Hi ||
		!slices.Equal(got.Buckets, want.Buckets) ||
		got.Underflow != want.Underflow || got.Overflow != want.Overflow ||
		got.Skipped != want.Skipped || got.Added != want.Added {
		t.Fatalf("source changed: got %+v want %+v", got, want)
	}
}
