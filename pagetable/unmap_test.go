package pagetable

import (
	"errors"
	"testing"
)

// TestUnmapSplitBothEnds splits a 64-leaf at both ends in one operation:
// Unmap(1,62) over [0,64) creates one L1 table plus two L0 tables (s=3).
func TestUnmapSplitBothEnds(t *testing.T) {
	m := mustNew(t, 3)
	mustMap(t, m, 0, 64, 128, true)
	if got := mustUnmap(t, m, 1, 62); got != 62 {
		t.Fatalf("Unmap(1,62) = %d, want 62", got)
	}
	checkState(t, m, 3, 2, 2)
	checkLeaves(t, m, []LeafInfo{
		{Start: 0, Size: 1, PFN: 128, W: true},
		{Start: 63, Size: 1, PFN: 191, W: true},
	})
}

// TestUnmapPeakQuotaNoCreditForFreedTables: tables freed later in the same
// operation must not offset the peak. P=2, U=2 (L1+L0 with 8 one-page
// leaves, plus an 8-leaf in the same L1). Unmap(0,9) frees the whole L0
// (final U would be 2) but the split of the 8-leaf needs s=1, so the peak
// U+s=3 exceeds P=2 and the operation is rejected.
func TestUnmapPeakQuotaNoCreditForFreedTables(t *testing.T) {
	m := mustNew(t, 2)
	for i := 0; i < 8; i++ {
		mustMap(t, m, i, 1, 16+i, true)
	}
	mustMap(t, m, 8, 8, 64, true)
	checkState(t, m, 2, 16, 9)
	before := m.Leaves()
	if _, err := m.Unmap(0, 9); !errors.Is(err, ErrQuota) {
		t.Fatalf("Unmap(0,9) err = %v, want ErrQuota", err)
	}
	checkState(t, m, 2, 16, 9)
	checkLeaves(t, m, before)
	// With P=3 the same operation succeeds: final U=2 (L0 gone, new L0').
	m2 := mustNew(t, 3)
	for i := 0; i < 8; i++ {
		mustMap(t, m2, i, 1, 16+i, true)
	}
	mustMap(t, m2, 8, 8, 64, true)
	if got := mustUnmap(t, m2, 0, 9); got != 9 {
		t.Fatalf("Unmap(0,9) = %d, want 9", got)
	}
	checkState(t, m2, 2, 7, 10)
	checkLeaves(t, m2, []LeafInfo{
		{Start: 9, Size: 1, PFN: 65, W: true},
		{Start: 10, Size: 1, PFN: 66, W: true},
		{Start: 11, Size: 1, PFN: 67, W: true},
		{Start: 12, Size: 1, PFN: 68, W: true},
		{Start: 13, Size: 1, PFN: 69, W: true},
		{Start: 14, Size: 1, PFN: 70, W: true},
		{Start: 15, Size: 1, PFN: 71, W: true},
	})
}

// TestUnmapHolesOnly: a range covering only unmapped pages changes nothing
// and does not bump the epoch.
func TestUnmapHolesOnly(t *testing.T) {
	m := mustNew(t, 4)
	mustMap(t, m, 0, 8, 16, true)
	checkState(t, m, 1, 8, 1)
	before := m.Leaves()
	for _, tc := range []struct{ vpn, n int }{
		{8, 8}, {100, 50}, {504, 8}, {8, 504},
	} {
		if got := mustUnmap(t, m, tc.vpn, tc.n); got != 0 {
			t.Fatalf("Unmap(%d,%d) = %d, want 0", tc.vpn, tc.n, got)
		}
	}
	checkState(t, m, 1, 8, 1)
	checkLeaves(t, m, before)
}

func TestUnmapInvalidParams(t *testing.T) {
	m := mustNew(t, 4)
	mustMap(t, m, 0, 8, 16, true)
	for _, tc := range []struct{ vpn, n int }{
		{-1, 1}, {512, 1}, {0, 0}, {0, -3}, {0, 513}, {500, 13}, {511, 2},
	} {
		if _, err := m.Unmap(tc.vpn, tc.n); !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("Unmap(%d,%d) err = %v, want ErrInvalidParam", tc.vpn, tc.n, err)
		}
	}
	checkState(t, m, 1, 8, 1)
}

// TestUnmapCascadeReclaim: removing the last leaf of an L0 reclaims it, and
// the reclamation cascades to the L1 when it becomes empty too.
func TestUnmapCascadeReclaim(t *testing.T) {
	m := mustNew(t, 4)
	mustMap(t, m, 0, 1, 16, true)
	checkState(t, m, 2, 1, 1)
	if got := mustUnmap(t, m, 0, 1); got != 1 {
		t.Fatalf("Unmap(0,1) = %d, want 1", got)
	}
	checkState(t, m, 0, 0, 2)
	checkLeaves(t, m, nil)
}

// TestUnmapFullRangeCounter: on a full image (8 L1 + 64 L0 tables, P=72),
// Unmap(0,512) frees everything wholesale examining at most 8 slots.
func TestUnmapFullRangeCounter(t *testing.T) {
	m := mustNew(t, 72)
	for i := 0; i < 512; i++ {
		mustMap(t, m, i, 1, i, true)
	}
	checkState(t, m, 72, 512, 512)
	if got := mustUnmap(t, m, 0, 512); got != 512 {
		t.Fatalf("Unmap(0,512) = %d, want 512", got)
	}
	checkState(t, m, 0, 0, 513)
	if m.cUnmap > 8 {
		t.Fatalf("Unmap(0,512) examined %d slots, want <= 8", m.cUnmap)
	}
}

// TestUnmapSlotBound checks the 40-slot bound on boundary-heavy ranges over
// a full image.
func TestUnmapSlotBound(t *testing.T) {
	for _, tc := range []struct{ vpn, n int }{
		{1, 510}, {1, 511}, {0, 511}, {3, 5}, {63, 2}, {7, 2}, {100, 300},
	} {
		m := mustNew(t, 72)
		for i := 0; i < 512; i++ {
			mustMap(t, m, i, 1, i, true)
		}
		if _, err := m.Unmap(tc.vpn, tc.n); err != nil {
			t.Fatalf("Unmap(%d,%d) err = %v", tc.vpn, tc.n, err)
		}
		if m.cUnmap > 40 {
			t.Fatalf("Unmap(%d,%d) examined %d slots, want <= 40", tc.vpn, tc.n, m.cUnmap)
		}
	}
}

// TestUnmapSplitInheritsAD: split children inherit W, A and D; a ScanDirty
// right after the split returns exactly the children that inherited D.
func TestUnmapSplitInheritsAD(t *testing.T) {
	m := mustNew(t, 4)
	mustMap(t, m, 0, 64, 128, true)
	mustTouch(t, m, 40, true) // A=D=1 on the 64-leaf
	if got := mustUnmap(t, m, 3, 5); got != 5 {
		t.Fatalf("Unmap(3,5) = %d, want 5", got)
	}
	// 3 one-page leaves + 7 eight-page leaves, all with A=D=1.
	dirty := m.ScanDirty()
	if len(dirty) != 10 {
		t.Fatalf("ScanDirty after split returned %d leaves, want 10", len(dirty))
	}
	for _, l := range dirty {
		if !l.A || !l.D || !l.W {
			t.Fatalf("split child lost inherited bits: %+v", l)
		}
	}
	// A rescan immediately after is empty.
	if again := m.ScanDirty(); len(again) != 0 {
		t.Fatalf("second ScanDirty returned %d leaves, want 0", len(again))
	}
	// A survived the scan (only D is cleared).
	tr, ok := m.Translate(0)
	if !ok || !tr.A || tr.D {
		t.Fatalf("Translate(0) = %+v,%v after ScanDirty", tr, ok)
	}
}

// TestUnmapSplitKeepsUnmappedHoles: splitting only materializes children of
// the split leaf; pages outside the leaf stay unmapped.
func TestUnmapSplitKeepsUnmappedHoles(t *testing.T) {
	m := mustNew(t, 4)
	mustMap(t, m, 8, 8, 64, true)
	if got := mustUnmap(t, m, 9, 6); got != 6 {
		t.Fatalf("Unmap(9,6) = %d, want 6", got)
	}
	checkState(t, m, 2, 2, 2)
	checkLeaves(t, m, []LeafInfo{
		{Start: 8, Size: 1, PFN: 64, W: true},
		{Start: 15, Size: 1, PFN: 71, W: true},
	})
	checkTranslate(t, m, 0, Translation{}, false)
	checkTranslate(t, m, 7, Translation{}, false)
	checkTranslate(t, m, 16, Translation{}, false)
}

func TestTouch(t *testing.T) {
	m := mustNew(t, 4)
	mustMap(t, m, 0, 8, 16, true)
	mustMap(t, m, 8, 8, 24, false) // read-only leaf
	// Invalid vpn.
	for _, vpn := range []int{-1, 512, 1000} {
		if err := m.Touch(vpn, false); !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("Touch(%d) err = %v, want ErrInvalidParam", vpn, err)
		}
	}
	// Unmapped page.
	if err := m.Touch(100, false); !errors.Is(err, ErrUnmapped) {
		t.Fatalf("Touch(100) err = %v, want ErrUnmapped", err)
	}
	// Write touch on a read-only leaf is rejected and leaves A unchanged.
	if err := m.Touch(9, true); !errors.Is(err, ErrWriteProtected) {
		t.Fatalf("Touch(9,write) err = %v, want ErrWriteProtected", err)
	}
	tr, _ := m.Translate(9)
	if tr.A || tr.D {
		t.Fatalf("rejected write touch set bits: %+v", tr)
	}
	// Read touch on the read-only leaf sets A but not D.
	mustTouch(t, m, 9, false)
	tr, _ = m.Translate(9)
	if !tr.A || tr.D {
		t.Fatalf("read touch: %+v", tr)
	}
	// Write touch on a writable leaf sets A and D.
	mustTouch(t, m, 3, true)
	tr, _ = m.Translate(3)
	if !tr.A || !tr.D {
		t.Fatalf("write touch: %+v", tr)
	}
	// Touch never changes the epoch.
	checkState(t, m, 1, 16, 2)
}

func TestScanDirtyOrderAndClear(t *testing.T) {
	m := mustNew(t, 4)
	mustMap(t, m, 16, 8, 32, true)
	mustMap(t, m, 0, 8, 16, true)
	mustTouch(t, m, 20, true)
	mustTouch(t, m, 1, true)
	dirty := m.ScanDirty()
	if len(dirty) != 2 || dirty[0].Start != 0 || dirty[1].Start != 16 {
		t.Fatalf("ScanDirty = %v, want leaves at 0 and 16 in order", dirty)
	}
	if got := m.ScanDirty(); len(got) != 0 {
		t.Fatalf("ScanDirty after clear = %v, want empty", got)
	}
	checkState(t, m, 1, 16, 2) // ScanDirty does not bump the epoch
}
