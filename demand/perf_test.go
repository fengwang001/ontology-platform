package demand

import (
	"testing"
	"time"
)

// TestEvalCostIndependentOfHistory demonstrates that the per-report
// evaluation cost does not grow with the length of the report history:
// it measures the average cost of a report early in the stream and again
// after a million accepted reports, on the same controller. A
// history-dependent implementation (e.g. scanning all past reports per
// evaluation, as the naive reference model does) would be roughly 50x
// slower in the second tier; the bounded-ledger implementation stays
// flat. The structural guarantee is asserted separately in
// TestBoundaryRetentionBounded.
func TestEvalCostIndependentOfHistory(t *testing.T) {
	cfg := Config{ContractDemandKW: 100000, WindowSeconds: 60, SlipSeconds: 10, MaxPhysicalPowerKW: 10000}
	c, err := NewController(cfg)
	if err != nil {
		t.Fatalf("NewController: %v", err)
	}
	for id := 1; id <= 30; id++ {
		if err := c.AddLoad(LoadSpec{ID: id, RatedPowerKW: 10, Priority: id}); err != nil {
			t.Fatalf("AddLoad: %v", err)
		}
	}
	const total = 1_020_000
	const tierStart1, tierEnd1 = 100_000, 110_000
	const tierStart2 = 1_000_000
	var tier1, tier2 time.Duration
	start := time.Now()
	for ts := int64(1); ts <= total; ts++ {
		if ts == tierStart1 {
			start = time.Now()
		}
		if ts == tierEnd1 {
			tier1 = time.Since(start)
		}
		if ts == tierStart2 {
			start = time.Now()
		}
		if _, err := c.Report(ts, 1); err != nil {
			t.Fatalf("Report(%d): %v", ts, err)
		}
	}
	tier2 = time.Since(start)
	per1 := tier1 / (tierEnd1 - tierStart1)
	per2 := tier2 / (total - tierStart2)
	ratio := float64(per2) / float64(per1)
	t.Logf("history tier 1 (%d-%d reports): %v/report", tierStart1, tierEnd1, per1)
	t.Logf("history tier 2 (%d-%d reports): %v/report", tierStart2, total, per2)
	t.Logf("tier2/tier1 ratio: %.2f (history length differs 50x)", ratio)
	if ratio > 30 {
		t.Fatalf("per-report cost grew %.1fx while history grew 50x; evaluation must be history-independent", ratio)
	}
}

// BenchmarkReport measures the amortized cost of one accepted report
// (ledger update + evaluation) with 30 loads and 6 overlapping windows.
// Run with: go test -bench=BenchmarkReport -benchmem ./demand/
func BenchmarkReport(b *testing.B) {
	cfg := Config{ContractDemandKW: 100000, WindowSeconds: 60, SlipSeconds: 10, MaxPhysicalPowerKW: 10000}
	c, err := NewController(cfg)
	if err != nil {
		b.Fatalf("NewController: %v", err)
	}
	for id := 1; id <= 30; id++ {
		if err := c.AddLoad(LoadSpec{ID: id, RatedPowerKW: 10, Priority: id}); err != nil {
			b.Fatalf("AddLoad: %v", err)
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := c.Report(int64(i+1), 1); err != nil {
			b.Fatalf("Report: %v", err)
		}
	}
}
