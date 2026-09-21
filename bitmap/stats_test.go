package bitmap

import "testing"

// TestStatsVisitFewRuns proves Count/Min/Max inspect O(runs), not
// O(bits): a 10M-bit set with 2 runs is summarised by looking at
// at most 2 runs.
func TestStatsVisitFewRuns(t *testing.T) {
	b := New()
	b.SetRange(1_000_000, 10_999_999) // 2 runs, 10M bits

	if got := b.Count(); got != 10_000_000 {
		t.Fatalf("Count = %d", got)
	}
	if v := b.LastStatRuns(); v > 2 {
		t.Fatalf("Count visited %d runs, want <= 2", v)
	}

	if got, ok := b.Min(); !ok || got != 1_000_000 {
		t.Fatalf("Min = %d, %v", got, ok)
	}
	if v := b.LastStatRuns(); v > 2 {
		t.Fatalf("Min visited %d runs, want <= 2", v)
	}

	if got, ok := b.Max(); !ok || got != 10_999_999 {
		t.Fatalf("Max = %d, %v", got, ok)
	}
	if v := b.LastStatRuns(); v != 1 {
		t.Fatalf("Max visited %d runs, want 1", v)
	}

	// The run-visit counts must be negligible next to the bit count.
	b.Count()
	if v := b.LastStatRuns(); uint64(v)*1_000_000 > 10_000_000 {
		t.Fatalf("Count visited %d runs, not << 10M bits", v)
	}
}

func TestStatsManyRuns(t *testing.T) {
	b := New()
	// 100 isolated bits -> 100 1-runs plus gaps.
	for i := uint32(0); i < 100; i++ {
		b.Set(i * 10)
	}
	if got := b.Count(); got != 100 {
		t.Fatalf("Count = %d", got)
	}
	if v := b.LastStatRuns(); v > 199 {
		t.Fatalf("Count visited %d runs", v)
	}
	if got, _ := b.Min(); got != 0 {
		t.Fatalf("Min = %d", got)
	}
	if got, _ := b.Max(); got != 990 {
		t.Fatalf("Max = %d", got)
	}
}
