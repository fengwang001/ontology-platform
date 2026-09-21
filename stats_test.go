package ontology

import (
	"errors"
	"testing"
)

func TestCountMinMaxBasic(t *testing.T) {
	b := New()
	b.SetRange(100, 199)
	b.Set(7)
	if got := b.Count(); got != 101 {
		t.Fatalf("Count = %d, want 101", got)
	}
	if v, err := b.Min(); err != nil || v != 7 {
		t.Fatalf("Min = %d, %v; want 7, nil", v, err)
	}
	if v, err := b.Max(); err != nil || v != 199 {
		t.Fatalf("Max = %d, %v; want 199, nil", v, err)
	}
}

func TestCountMinMaxEmpty(t *testing.T) {
	b := New()
	if got := b.Count(); got != 0 {
		t.Fatalf("Count = %d, want 0", got)
	}
	if _, err := b.Min(); !errors.Is(err, ErrEmpty) {
		t.Fatalf("Min err = %v, want ErrEmpty", err)
	}
	if _, err := b.Max(); !errors.Is(err, ErrEmpty) {
		t.Fatalf("Max err = %v, want ErrEmpty", err)
	}
}

// TestStatsVisitRunsNotBits: on a ten-million-bit set the statistics must
// inspect a number of runs far below the number of bits.
func TestStatsVisitRunsNotBits(t *testing.T) {
	b := New()
	b.SetRange(0, 9_999_999) // 10M bits, a single one-run
	count, countRuns := b.CountEx()
	if count != 10_000_000 {
		t.Fatalf("Count = %d, want 10_000_000", count)
	}
	if countRuns > 100 {
		t.Errorf("Count visited %d runs, want far below 10M", countRuns)
	}
	minV, minRuns, err := b.MinEx()
	if err != nil || minV != 0 {
		t.Fatalf("Min = %d, %v; want 0, nil", minV, err)
	}
	if minRuns > 100 {
		t.Errorf("Min visited %d runs, want far below 10M", minRuns)
	}
	maxV, maxRuns, err := b.MaxEx()
	if err != nil || maxV != 9_999_999 {
		t.Fatalf("Max = %d, %v; want 9999999, nil", maxV, err)
	}
	if maxRuns > 100 {
		t.Errorf("Max visited %d runs, want far below 10M", maxRuns)
	}
}
