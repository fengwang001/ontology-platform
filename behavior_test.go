package ftl

import (
	"fmt"
	"testing"
)

func TestWatermarkEqualAndOneBelow(t *testing.T) {
	cfg := Config{
		BlockCount:    8,
		PagesPerBlock: 2,
		LogicalPages:  32,
		LowWatermark:  2,
		HighWatermark: 5,
		EraseLimit:    100,
	}
	f := mustNew(t, cfg)

	writes(t, f, 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 0)
	if got := f.Stats().FreeBlocks; got != 2 {
		t.Fatalf("free blocks = %d, want exactly low watermark 2", got)
	}
	before := append([]int(nil), f.Stats().EraseCounts...)

	requireErrIs(t, f.Write(0), nil)
	if got := f.Stats().FreeBlocks; got != 2 {
		t.Fatalf("free blocks = %d, want GC restored low watermark", got)
	}
	erased := 0
	for blockID, count := range f.Stats().EraseCounts {
		if count != before[blockID] {
			erased++
		}
	}
	if erased == 0 {
		t.Fatalf("writing one below low watermark did not trigger GC")
	}
}

func TestVictimTieBreakers(t *testing.T) {
	cfg := Config{
		BlockCount:    9,
		PagesPerBlock: 4,
		LogicalPages:  64,
		LowWatermark:  2,
		HighWatermark: 7,
		EraseLimit:    100,
	}
	f := mustNew(t, cfg)

	fillBlock(t, f, 0)
	fillBlock(t, f, 4)
	fillBlock(t, f, 8)
	fillBlock(t, f, 12)
	fillBlock(t, f, 16)
	fillBlock(t, f, 20)

	requireErrIs(t, f.Discard(0), nil)
	requireErrIs(t, f.Discard(4), nil)
	fillBlock(t, f, 24)

	before := append([]int(nil), f.Stats().EraseCounts...)
	writes(t, f, 0)
	if f.Blocks()[0].EraseCount != before[0]+1 {
		t.Fatalf("smallest block id did not win equal valid-page and erase-count tie")
	}
}

func TestFullValidBlockIsNotVictim(t *testing.T) {
	f := mustNew(t, testConfig())
	fillBlock(t, f, 0)
	fillBlock(t, f, 4)
	fillBlock(t, f, 8)
	before := append([]int(nil), f.Stats().EraseCounts...)

	f.mu.Lock()
	f.collectGarbageLocked()
	f.mu.Unlock()

	after := f.Stats().EraseCounts
	if !equalInts(before, after) {
		t.Fatalf("GC erased without valid-page benefit: before=%v after=%v", before, after)
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestAdmissionNoStateChangeAndNoGC(t *testing.T) {
	f := mustNew(t, testConfig())
	limit := (f.cfg.BlockCount - f.cfg.LowWatermark) * f.cfg.PagesPerBlock
	for lpn := 0; lpn < limit; lpn++ {
		writes(t, f, lpn)
	}

	before := fullSnapshot(t, f)
	requireErrIs(t, f.Write(limit), ErrNoSpace)
	after := fullSnapshot(t, f)
	if before != after {
		t.Fatalf("rejected write changed state\nbefore: %s\nafter: %s", before, after)
	}

	writes(t, f, 0)
}

func fullSnapshot(t *testing.T, f *FTL) string {
	t.Helper()
	return fmt.Sprintf("stats=%+v blocks=%+v pages=%#v mapping=%v active=%d mapped=%d",
		f.Stats(), f.Blocks(), f.PhysicalPages(), f.mapping, f.active, f.mappedPages)
}
