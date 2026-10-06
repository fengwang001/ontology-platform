package ftl

import "testing"

type complexityCounters struct {
	heapComparisons int
	heapSwaps       int
}

func measureNonGCWrite(t *testing.T, blockCount int) complexityCounters {
	t.Helper()
	cfg := Config{
		BlockCount:    blockCount,
		PagesPerBlock: 4,
		LogicalPages:  blockCount * 4,
		LowWatermark:  2,
		HighWatermark: blockCount - 1,
		EraseLimit:    100,
	}
	f := mustNew(t, cfg)
	metrics := &heapMetrics{}
	f.victimBlocks.metrics = metrics
	f.coldBlocks.metrics = metrics

	if err := f.Write(0); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	return complexityCounters{heapComparisons: metrics.Comparisons, heapSwaps: metrics.Swaps}
}

func TestNonGCWriteIndependentOfBlockAndMappingCount(t *testing.T) {
	small := measureNonGCWrite(t, 32)
	large := measureNonGCWrite(t, 128)
	if small != (complexityCounters{}) || large != (complexityCounters{}) {
		t.Fatalf("ordinary write used indexed-heap work: small=%+v large=%+v", small, large)
	}
	if small != large {
		t.Fatalf("ordinary write work changed with scale: small=%+v large=%+v", small, large)
	}
}

func TestVictimSelectionIsLogarithmic(t *testing.T) {
	cfg := Config{
		BlockCount:    128,
		PagesPerBlock: 4,
		LogicalPages:  1024,
		LowWatermark:  2,
		HighWatermark: 100,
		EraseLimit:    100,
	}
	f := mustNew(t, cfg)
	for blockID := 0; blockID < 126; blockID++ {
		fillBlock(t, f, blockID*4)
	}

	metrics := &heapMetrics{}
	f.victimBlocks.metrics = metrics
	requireErrIs(t, f.Discard(0), nil)
	beforeComparisons := metrics.Comparisons
	if _, ok := f.selectVictimLocked(); !ok {
		t.Fatalf("expected victim")
	}
	comparisons := metrics.Comparisons - beforeComparisons
	if comparisons > 7 {
		t.Fatalf("victim selection comparisons = %d, exceeds heap height 7", comparisons)
	}
}
