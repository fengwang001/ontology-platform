package pagetable

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"testing"
)

func mustErrCode(t *testing.T, err error, want ErrCode) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error %v, got nil", want)
	}
	var pe *Error
	if !errors.As(err, &pe) {
		t.Fatalf("expected *Error, got %T: %v", err, err)
	}
	if pe.Code != want {
		t.Fatalf("expected code %v, got %v (%v)", want, pe.Code, err)
	}
}

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func newMapper(t *testing.T, quota int) *Mapper {
	t.Helper()
	m, err := New(quota)
	if err != nil {
		t.Fatalf("New(%d): %v", quota, err)
	}
	return m
}

// checkInvariants verifies the structural invariants that must hold at all times.
func checkInvariants(t *testing.T, m *Mapper) {
	t.Helper()
	leaves := m.Leaves()
	for i, lf := range leaves {
		if lf.Size != 1 && lf.Size != 8 && lf.Size != 64 {
			t.Fatalf("leaf %d has bad size %d", i, lf.Size)
		}
		if lf.Start%lf.Size != 0 {
			t.Fatalf("leaf %d start %d not aligned to size %d", i, lf.Start, lf.Size)
		}
		if i > 0 && leaves[i-1].Start+leaves[i-1].Size > lf.Start {
			t.Fatalf("leaves %d and %d overlap or out of order", i-1, i)
		}
	}
	if got, want := m.Tables(), deriveTables(leaves); got != want {
		t.Fatalf("Tables()=%d, derived from leaves=%d", got, want)
	}
	sum := 0
	for _, lf := range leaves {
		sum += lf.Size
	}
	if got := m.Mapped(); got != sum {
		t.Fatalf("Mapped()=%d, sum of leaf sizes=%d", got, sum)
	}
	for vpn := 0; vpn < NumPages; vpn++ {
		tr, ok := m.Translate(vpn)
		lf := findLeafIn(leaves, vpn)
		if lf == nil {
			if ok {
				t.Fatalf("Translate(%d) mapped but no leaf covers it", vpn)
			}
			continue
		}
		if !ok {
			t.Fatalf("Translate(%d) unmapped but leaf %+v covers it", vpn, *lf)
		}
		want := Translation{Pfn: lf.Pfn + vpn - lf.Start, Size: lf.Size, W: lf.W, A: lf.A, D: lf.D}
		if tr != want {
			t.Fatalf("Translate(%d)=%+v, want %+v", vpn, tr, want)
		}
	}
}

// deriveTables computes the table count uniquely determined by the leaf set.
func deriveTables(leaves []Leaf) int {
	l1 := map[int]bool{}
	l0 := map[int]bool{}
	for _, lf := range leaves {
		if lf.Size < 64 {
			l1[lf.Start/64] = true
		}
		if lf.Size == 1 {
			l0[lf.Start/8] = true
		}
	}
	return len(l1) + len(l0)
}

func findLeafIn(leaves []Leaf, vpn int) *Leaf {
	for i := range leaves {
		if leaves[i].Start <= vpn && vpn < leaves[i].Start+leaves[i].Size {
			return &leaves[i]
		}
	}
	return nil
}

func TestNewQuotaValidation(t *testing.T) {
	for _, q := range []int{0, -1, MaxQuota + 1} {
		if _, err := New(q); err == nil {
			t.Fatalf("New(%d) should fail", q)
		} else {
			mustErrCode(t, err, ErrInvalidArgument)
		}
	}
	for _, q := range []int{1, 72, MaxQuota} {
		if _, err := New(q); err != nil {
			t.Fatalf("New(%d): %v", q, err)
		}
	}
}

// TestSpecExampleP4 replays the P=4 scenario from the specification.
func TestSpecExampleP4(t *testing.T) {
	m := newMapper(t, 4)
	mustOK(t, m.Map(0, 64, 128, true))
	if got := m.Tables(); got != 0 {
		t.Fatalf("Tables=%d, want 0", got)
	}
	mustOK(t, m.Touch(10, true))
	tr, ok := m.Translate(10)
	if !ok || !tr.A || !tr.D {
		t.Fatalf("after Touch: %+v ok=%v, want A=1 D=1", tr, ok)
	}
	n, err := m.Unmap(3, 5)
	mustOK(t, err)
	if n != 5 {
		t.Fatalf("Unmap(3,5)=%d, want 5", n)
	}
	if got := m.Tables(); got != 2 {
		t.Fatalf("Tables=%d, want 2", got)
	}
	if got := m.Mapped(); got != 59 {
		t.Fatalf("Mapped=%d, want 59", got)
	}
	if got := m.Epoch(); got != 2 {
		t.Fatalf("Epoch=%d, want 2", got)
	}
	// Split leaves inherit W, A, D and contiguous pfns.
	leaves := m.Leaves()
	var sizes []int
	for _, lf := range leaves {
		sizes = append(sizes, lf.Size)
		if !lf.W || !lf.A || !lf.D {
			t.Fatalf("leaf %+v did not inherit W/A/D", lf)
		}
	}
	wantSizes := []int{1, 1, 1, 8, 8, 8, 8, 8, 8, 8}
	if !reflect.DeepEqual(sizes, wantSizes) {
		t.Fatalf("leaf sizes=%v, want %v", sizes, wantSizes)
	}
	if leaves[0].Pfn != 128 || leaves[3].Pfn != 136 || leaves[9].Pfn != 184 {
		t.Fatalf("split pfns wrong: %+v", leaves)
	}
	n, err = m.Unmap(0, 3)
	mustOK(t, err)
	if n != 3 {
		t.Fatalf("Unmap(0,3)=%d, want 3", n)
	}
	if got := m.Tables(); got != 1 {
		t.Fatalf("Tables=%d, want 1 (L0 reclaimed)", got)
	}
	if got := m.Epoch(); got != 3 {
		t.Fatalf("Epoch=%d, want 3", got)
	}
	n, err = m.Unmap(8, 56)
	mustOK(t, err)
	if n != 56 {
		t.Fatalf("Unmap(8,56)=%d, want 56", n)
	}
	if got := m.Tables(); got != 0 {
		t.Fatalf("Tables=%d, want 0 (L1 reclaimed)", got)
	}
	if got := m.Mapped(); got != 0 {
		t.Fatalf("Mapped=%d, want 0", got)
	}
	if got := m.Epoch(); got != 4 {
		t.Fatalf("Epoch=%d, want 4", got)
	}
	checkInvariants(t, m)
}

// TestSpecExampleP1Quota replays the P=1 rejection from the specification.
func TestSpecExampleP1Quota(t *testing.T) {
	m := newMapper(t, 1)
	mustOK(t, m.Map(0, 64, 128, true))
	mustOK(t, m.Touch(10, true))
	before := m.Leaves()
	n, err := m.Unmap(3, 5)
	if err == nil {
		t.Fatalf("Unmap(3,5) with P=1 should fail, got n=%d", n)
	}
	mustErrCode(t, err, ErrQuotaExceeded)
	if n != 0 {
		t.Fatalf("rejected Unmap returned n=%d, want 0", n)
	}
	if got := m.Epoch(); got != 1 {
		t.Fatalf("Epoch=%d, want 1 (rejection must not count)", got)
	}
	if got := m.Tables(); got != 0 {
		t.Fatalf("Tables=%d, want 0", got)
	}
	if !reflect.DeepEqual(m.Leaves(), before) {
		t.Fatalf("rejected Unmap changed leaves")
	}
	tr, _ := m.Translate(10)
	if !tr.A || !tr.D {
		t.Fatalf("rejected Unmap changed A/D: %+v", tr)
	}
}

// TestSpecExampleP2Promote replays the P=2 promote scenario from the specification.
func TestSpecExampleP2Promote(t *testing.T) {
	m := newMapper(t, 2)
	for i := 0; i < 8; i++ {
		mustOK(t, m.Map(i, 1, 16+i, true))
	}
	if got := m.Tables(); got != 2 {
		t.Fatalf("Tables=%d, want 2", got)
	}
	mustOK(t, m.Touch(2, false))
	mustOK(t, m.Touch(5, true))
	mustOK(t, m.Promote(0, 8))
	leaves := m.Leaves()
	if len(leaves) != 1 {
		t.Fatalf("leaves=%v, want single 8-page leaf", leaves)
	}
	lf := leaves[0]
	if lf.Start != 0 || lf.Size != 8 || lf.Pfn != 16 {
		t.Fatalf("leaf=%+v, want start=0 size=8 pfn=16", lf)
	}
	if !lf.A || !lf.D || !lf.W {
		t.Fatalf("merged leaf=%+v, want A=1 D=1 W=1", lf)
	}
	if got := m.Tables(); got != 1 {
		t.Fatalf("Tables=%d, want 1", got)
	}
	if got := m.Epoch(); got != 9 {
		t.Fatalf("Epoch=%d, want 9", got)
	}
	checkInvariants(t, m)
}

func TestMapSizesAndAlignment(t *testing.T) {
	m := newMapper(t, 10)
	mustOK(t, m.Map(0, 1, 7, true))   // pfn need only align to 1
	mustOK(t, m.Map(8, 8, 16, false)) // 8-aligned
	mustOK(t, m.Map(64, 64, 0, true)) // 64-aligned, pfn 0
	checkInvariants(t, m)
	// vpn aligned but pfn misaligned.
	mustErrCode(t, m.Map(128, 8, 130, true), ErrMisaligned)
	mustErrCode(t, m.Map(192, 64, 96, true), ErrMisaligned)
	// vpn misaligned.
	mustErrCode(t, m.Map(3, 8, 8, true), ErrMisaligned)
	mustErrCode(t, m.Map(9, 64, 128, true), ErrMisaligned)
}

func TestMapInvalidArguments(t *testing.T) {
	m := newMapper(t, 10)
	mustErrCode(t, m.Map(0, 2, 0, true), ErrInvalidArgument)
	mustErrCode(t, m.Map(0, 16, 0, true), ErrInvalidArgument)
	mustErrCode(t, m.Map(-1, 1, 0, true), ErrInvalidArgument)
	mustErrCode(t, m.Map(512, 1, 0, true), ErrInvalidArgument)
	mustErrCode(t, m.Map(0, 1, -1, true), ErrInvalidArgument)
	mustErrCode(t, m.Map(0, 1, MaxPfn, true), ErrInvalidArgument)
	// Invalid argument reported before misalignment.
	mustErrCode(t, m.Map(3, 2, 5, true), ErrInvalidArgument)
	// Misalignment reported before overlap.
	mustOK(t, m.Map(0, 64, 0, true))
	mustErrCode(t, m.Map(64, 8, 9, true), ErrMisaligned)
}

func TestMapOverlap(t *testing.T) {
	m := newMapper(t, 10)
	mustOK(t, m.Map(0, 64, 0, true))
	// A disjoint 64-page leaf next to the first one is fine.
	mustOK(t, m.Map(64, 64, 64, true))
	if got := m.Mapped(); got != 128 {
		t.Fatalf("Mapped=%d, want 128", got)
	}
	checkInvariants(t, m)
}

func TestMapOverlapCases(t *testing.T) {
	m := newMapper(t, 20)
	mustOK(t, m.Map(0, 64, 0, true))
	// Inside the 64-page leaf.
	mustErrCode(t, m.Map(8, 8, 64, true), ErrOverlap)
	mustErrCode(t, m.Map(63, 1, 1, true), ErrOverlap)
	mustErrCode(t, m.Map(0, 64, 64, true), ErrOverlap)
	// Covering existing small pages with a bigger leaf.
	m2 := newMapper(t, 20)
	mustOK(t, m2.Map(8, 8, 64, true))
	mustErrCode(t, m2.Map(0, 64, 0, true), ErrOverlap)
	mustOK(t, m2.Map(0, 8, 0, false))
	mustErrCode(t, m2.Map(0, 64, 128, true), ErrOverlap)
	// Leaf exactly covering an existing leaf.
	mustErrCode(t, m2.Map(0, 8, 128, true), ErrOverlap)
	// 1-page map where an L0 table exists.
	m3 := newMapper(t, 20)
	mustOK(t, m3.Map(3, 1, 30, true))
	mustErrCode(t, m3.Map(0, 8, 0, true), ErrOverlap)
	mustErrCode(t, m3.Map(3, 1, 31, true), ErrOverlap)
	mustOK(t, m3.Map(4, 1, 40, true))
	checkInvariants(t, m)
	checkInvariants(t, m2)
	checkInvariants(t, m3)
}

func TestMapQuotaExactAndShort(t *testing.T) {
	// Exactly enough: 1-page map needs L1+L0 = 2 tables.
	m := newMapper(t, 2)
	mustOK(t, m.Map(0, 1, 0, true))
	if got := m.Tables(); got != 2 {
		t.Fatalf("Tables=%d, want 2", got)
	}
	// One short.
	m2 := newMapper(t, 1)
	mustErrCode(t, m2.Map(0, 1, 0, true), ErrQuotaExceeded)
	if got := m2.Tables(); got != 0 {
		t.Fatalf("rejected Map changed Tables to %d", got)
	}
	if got := m2.Epoch(); got != 0 {
		t.Fatalf("rejected Map changed Epoch to %d", got)
	}
	// 8-page map needs exactly one L1.
	m3 := newMapper(t, 1)
	mustOK(t, m3.Map(0, 8, 0, true))
	mustErrCode(t, m3.Map(64, 8, 64, true), ErrQuotaExceeded)
	// 64-page map never needs tables.
	m4 := newMapper(t, 1)
	mustOK(t, m4.Map(0, 64, 0, true))
	mustOK(t, m4.Map(64, 64, 64, true))
	// Quota check happens after overlap: overlapping map on full quota reports overlap.
	m5 := newMapper(t, 1)
	mustOK(t, m5.Map(0, 8, 0, true))
	mustErrCode(t, m5.Map(0, 8, 8, true), ErrOverlap)
}

// TestUnmapPeakQuotaTwoEnds splits at both range ends (two levels deep) and
// verifies the peak quota check: tables reclaimed later in the same operation
// must not offset the split tables.
func TestUnmapPeakQuotaTwoEnds(t *testing.T) {
	build := func(quota int) *Mapper {
		m := newMapper(t, quota)
		mustOK(t, m.Map(0, 64, 0, true)) // 64-page leaf at [0,64)
		for i := 64; i < 72; i++ {       // 1-page leaves at [64,72): L1 + L0, U=2
			mustOK(t, m.Map(i, 1, 1000+i, true))
		}
		if got := m.Tables(); got != 2 {
			t.Fatalf("setup Tables=%d, want 2", got)
		}
		return m
	}
	// Unmap [3,72): splits the 64-leaf (new L1) and its [0,8) child (new L0)
	// => s=2; also wholesale-frees the L1+L0 holding [64,72). Peak U+s=4.
	m := build(3)
	before := m.Leaves()
	epochBefore := m.Epoch()
	n, err := m.Unmap(3, 69)
	mustErrCode(t, err, ErrQuotaExceeded)
	if n != 0 {
		t.Fatalf("rejected Unmap returned %d", n)
	}
	if got := m.Tables(); got != 2 {
		t.Fatalf("rejected Unmap changed Tables to %d", got)
	}
	if got := m.Epoch(); got != epochBefore {
		t.Fatalf("rejected Unmap changed Epoch to %d", got)
	}
	if !reflect.DeepEqual(m.Leaves(), before) {
		t.Fatalf("rejected Unmap changed leaves")
	}
	// With quota 4 the same operation succeeds.
	m = build(4)
	n, err = m.Unmap(3, 69)
	mustOK(t, err)
	if n != 69 {
		t.Fatalf("Unmap(3,69)=%d, want 69", n)
	}
	// Remaining: 1-page leaves 0,1,2 (in new L0 inside new L1) => U=2.
	if got := m.Tables(); got != 2 {
		t.Fatalf("Tables=%d, want 2", got)
	}
	if got := m.Mapped(); got != 3 {
		t.Fatalf("Mapped=%d, want 3", got)
	}
	leaves := m.Leaves()
	if len(leaves) != 3 {
		t.Fatalf("leaves=%v, want 3 one-page leaves", leaves)
	}
	for i, lf := range leaves {
		if lf.Start != i || lf.Size != 1 || lf.Pfn != i {
			t.Fatalf("leaf %d = %+v, want start=%d size=1 pfn=%d", i, lf, i, i)
		}
	}
	checkInvariants(t, m)
}

func TestUnmapInvalidArguments(t *testing.T) {
	m := newMapper(t, 4)
	for _, args := range [][2]int{{-1, 1}, {512, 1}, {0, 0}, {0, -3}, {500, 13}, {511, 2}} {
		if _, err := m.Unmap(args[0], args[1]); err == nil {
			t.Fatalf("Unmap(%d,%d) should fail", args[0], args[1])
		} else {
			mustErrCode(t, err, ErrInvalidArgument)
		}
	}
	if _, err := m.Unmap(511, 1); err != nil {
		t.Fatalf("Unmap(511,1) on empty mapper: %v", err)
	}
}

func TestUnmapHoleNoStateChange(t *testing.T) {
	m := newMapper(t, 4)
	mustOK(t, m.Map(0, 8, 0, true))
	mustOK(t, m.Touch(0, true))
	before := m.Leaves()
	epochBefore := m.Epoch()
	// Ranges entirely over holes.
	for _, args := range [][2]int{{8, 8}, {100, 50}, {504, 8}} {
		n, err := m.Unmap(args[0], args[1])
		mustOK(t, err)
		if n != 0 {
			t.Fatalf("Unmap(%d,%d)=%d, want 0", args[0], args[1], n)
		}
	}
	if got := m.Epoch(); got != epochBefore {
		t.Fatalf("Epoch=%d, want %d (hole unmaps must not count)", got, epochBefore)
	}
	if got := m.Tables(); got != 1 {
		t.Fatalf("Tables=%d, want 1", got)
	}
	if !reflect.DeepEqual(m.Leaves(), before) {
		t.Fatalf("hole unmap changed leaves")
	}
}

func TestUnmapCascadeReclaim(t *testing.T) {
	m := newMapper(t, 4)
	for i := 0; i < 8; i++ {
		mustOK(t, m.Map(i, 1, 10+i, true))
	}
	if got := m.Tables(); got != 2 {
		t.Fatalf("Tables=%d, want 2", got)
	}
	// Removing all 8 pages empties L0, which empties L1: both reclaimed.
	n, err := m.Unmap(0, 8)
	mustOK(t, err)
	if n != 8 {
		t.Fatalf("Unmap(0,8)=%d, want 8", n)
	}
	if got := m.Tables(); got != 0 {
		t.Fatalf("Tables=%d, want 0 (cascading reclaim)", got)
	}
	// Partial reclaim: remove 7 pages, L0 survives with one leaf.
	m2 := newMapper(t, 4)
	for i := 0; i < 8; i++ {
		mustOK(t, m2.Map(i, 1, 10+i, true))
	}
	n, err = m2.Unmap(1, 7)
	mustOK(t, err)
	if n != 7 {
		t.Fatalf("Unmap(1,7)=%d, want 7", n)
	}
	if got := m2.Tables(); got != 2 {
		t.Fatalf("Tables=%d, want 2 (L0 not empty)", got)
	}
	checkInvariants(t, m)
	checkInvariants(t, m2)
}

func TestUnmapSplitInheritsAD(t *testing.T) {
	m := newMapper(t, 10)
	mustOK(t, m.Map(0, 64, 128, true))
	mustOK(t, m.Touch(10, true)) // big leaf: A=1 D=1
	n, err := m.Unmap(3, 5)
	mustOK(t, err)
	if n != 5 {
		t.Fatalf("Unmap(3,5)=%d, want 5", n)
	}
	// Every split child inherits A=1 D=1; ScanDirty must report exactly the
	// children that inherited D.
	leaves := m.Leaves()
	dirty := m.ScanDirty()
	if len(dirty) != len(leaves) {
		t.Fatalf("ScanDirty returned %d leaves, want %d (all children inherited D)", len(dirty), len(leaves))
	}
	for _, lf := range m.Leaves() {
		if !lf.A {
			t.Fatalf("leaf %+v lost inherited A", lf)
		}
		if lf.D {
			t.Fatalf("leaf %+v still dirty after ScanDirty", lf)
		}
	}
	// Immediately rescanning finds nothing.
	if again := m.ScanDirty(); len(again) != 0 {
		t.Fatalf("second ScanDirty returned %v, want empty", again)
	}
	// Epoch unchanged by Touch/ScanDirty.
	if got := m.Epoch(); got != 2 {
		t.Fatalf("Epoch=%d, want 2", got)
	}
	checkInvariants(t, m)
}

func TestTouchErrors(t *testing.T) {
	m := newMapper(t, 4)
	mustErrCode(t, m.Touch(-1, false), ErrInvalidArgument)
	mustErrCode(t, m.Touch(512, true), ErrInvalidArgument)
	mustErrCode(t, m.Touch(0, false), ErrUnmapped)
	mustOK(t, m.Map(0, 8, 0, false)) // not writable
	// Write to a non-writable leaf is rejected and A stays 0.
	mustErrCode(t, m.Touch(3, true), ErrWriteProtected)
	tr, ok := m.Translate(3)
	if !ok || tr.A || tr.D {
		t.Fatalf("after rejected Touch: %+v, want A=0 D=0", tr)
	}
	mustOK(t, m.Touch(3, false))
	tr, _ = m.Translate(3)
	if !tr.A || tr.D {
		t.Fatalf("after read Touch: %+v, want A=1 D=0", tr)
	}
	// Epoch untouched by Touch.
	if got := m.Epoch(); got != 1 {
		t.Fatalf("Epoch=%d, want 1", got)
	}
	mustErrCode(t, m.Touch(300, false), ErrUnmapped)
}

func TestScanDirtyOrderAndScope(t *testing.T) {
	m := newMapper(t, 80)
	mustOK(t, m.Map(64, 64, 64, true))
	mustOK(t, m.Map(8, 8, 8, true))
	mustOK(t, m.Map(0, 1, 1, true))
	mustOK(t, m.Touch(0, true))
	mustOK(t, m.Touch(70, true))
	mustOK(t, m.Touch(9, false)) // A only, no D
	dirty := m.ScanDirty()
	if len(dirty) != 2 {
		t.Fatalf("ScanDirty=%v, want 2 leaves", dirty)
	}
	if dirty[0].Start != 0 || dirty[0].Size != 1 {
		t.Fatalf("dirty[0]=%+v, want start=0 size=1", dirty[0])
	}
	if dirty[1].Start != 64 || dirty[1].Size != 64 {
		t.Fatalf("dirty[1]=%+v, want start=64 size=64", dirty[1])
	}
	if got := m.ScanDirty(); len(got) != 0 {
		t.Fatalf("rescan=%v, want empty", got)
	}
	// ScanDirty must not change anything else.
	tr, _ := m.Translate(9)
	if !tr.A {
		t.Fatalf("ScanDirty changed A of leaf at 8")
	}
	checkInvariants(t, m)
}

func TestPromoteInvalidAndMisaligned(t *testing.T) {
	m := newMapper(t, 10)
	mustErrCode(t, m.Promote(0, 1), ErrInvalidArgument)
	mustErrCode(t, m.Promote(0, 4), ErrInvalidArgument)
	mustErrCode(t, m.Promote(0, 512), ErrInvalidArgument)
	mustErrCode(t, m.Promote(-1, 8), ErrInvalidArgument)
	mustErrCode(t, m.Promote(512, 8), ErrInvalidArgument)
	// Misaligned vpn (checked after argument validation).
	mustErrCode(t, m.Promote(3, 8), ErrMisaligned)
	mustErrCode(t, m.Promote(8, 64), ErrMisaligned)
	// Invalid argument reported before misalignment.
	mustErrCode(t, m.Promote(3, 4), ErrInvalidArgument)
}

func TestPromoteNotATable(t *testing.T) {
	m := newMapper(t, 10)
	// Empty slot.
	mustErrCode(t, m.Promote(0, 8), ErrNotATable)
	mustErrCode(t, m.Promote(0, 64), ErrNotATable)
	// Slot covered by a leaf of the same size.
	mustOK(t, m.Map(0, 8, 0, true))
	mustErrCode(t, m.Promote(0, 8), ErrNotATable)
	// Slot covered by a larger leaf.
	mustOK(t, m.Map(64, 64, 64, true))
	mustErrCode(t, m.Promote(64, 8), ErrNotATable)
	mustErrCode(t, m.Promote(64, 64), ErrNotATable)
	// L1 exists but the L1 slot itself is an 8-page leaf.
	mustErrCode(t, m.Promote(0, 8), ErrNotATable)
	checkInvariants(t, m)
}

func TestPromoteNotAllLeaves(t *testing.T) {
	// Some sub-slots empty.
	m := newMapper(t, 10)
	for i := 0; i < 7; i++ {
		mustOK(t, m.Map(i, 1, 16+i, true))
	}
	mustErrCode(t, m.Promote(0, 8), ErrNotAllLeaves)
	// A sub-slot is itself a table (holds smaller leaves).
	m2 := newMapper(t, 10)
	mustOK(t, m2.Map(0, 1, 100, true))
	for i := 1; i < 8; i++ {
		mustOK(t, m2.Map(i*8, 8, 16+i*8, true))
	}
	// Promote(0,64): root slot 0 is a table whose slot 0 is an L0 table.
	mustErrCode(t, m2.Promote(0, 64), ErrNotAllLeaves)
	checkInvariants(t, m)
	checkInvariants(t, m2)
}

func TestPromotePfnMismatch(t *testing.T) {
	// Contiguous but base pfn not aligned to size.
	m := newMapper(t, 10)
	for i := 0; i < 8; i++ {
		mustOK(t, m.Map(i, 1, 20+i, true))
	}
	mustErrCode(t, m.Promote(0, 8), ErrPfnMismatch)
	// Aligned base but a gap in the sequence.
	m2 := newMapper(t, 10)
	for i := 0; i < 8; i++ {
		pfn := 16 + i
		if i == 5 {
			pfn = 16 + i + 1 // break contiguity
		}
		mustOK(t, m2.Map(i, 1, pfn, true))
	}
	mustErrCode(t, m2.Promote(0, 8), ErrPfnMismatch)
	// 64-page promote with contiguous but misaligned base pfn.
	m3 := newMapper(t, 80)
	for i := 0; i < 8; i++ {
		mustOK(t, m3.Map(i*8, 8, 8+i*8, true))
	}
	mustErrCode(t, m3.Promote(0, 64), ErrPfnMismatch)
	checkInvariants(t, m)
	checkInvariants(t, m2)
	checkInvariants(t, m3)
}

func TestPromoteWritableMismatch(t *testing.T) {
	m := newMapper(t, 10)
	for i := 0; i < 8; i++ {
		mustOK(t, m.Map(i, 1, 16+i, i != 4))
	}
	mustErrCode(t, m.Promote(0, 8), ErrWritableMismatch)
	// W mismatch reported after pfn checks: both bad -> pfn error first.
	m2 := newMapper(t, 10)
	for i := 0; i < 8; i++ {
		mustOK(t, m2.Map(i, 1, 20+i, i != 4)) // misaligned base pfn AND mixed W
	}
	mustErrCode(t, m2.Promote(0, 8), ErrPfnMismatch)
	checkInvariants(t, m)
}

func TestPromoteMergeADKeepsW(t *testing.T) {
	m := newMapper(t, 10)
	for i := 0; i < 8; i++ {
		mustOK(t, m.Map(i, 1, 16+i, false))
	}
	mustOK(t, m.Touch(1, false)) // A on leaf 1
	mustOK(t, m.Touch(6, false))
	// Leaf 6 not writable: write-touch rejected, so D stays 0 everywhere.
	mustErrCode(t, m.Touch(6, true), ErrWriteProtected)
	mustOK(t, m.Promote(0, 8))
	leaves := m.Leaves()
	if len(leaves) != 1 {
		t.Fatalf("leaves=%v, want one", leaves)
	}
	lf := leaves[0]
	if !lf.A || lf.D || lf.W {
		t.Fatalf("merged leaf=%+v, want A=1 D=0 W=0", lf)
	}
	// Promote of 64 with D set on one child.
	m2 := newMapper(t, 80)
	for i := 0; i < 8; i++ {
		mustOK(t, m2.Map(i*8, 8, 128+i*8, true))
	}
	mustOK(t, m2.Touch(40, true)) // D on the [40,48) leaf only
	mustOK(t, m2.Promote(0, 64))
	lf = m2.Leaves()[0]
	if lf.Size != 64 || !lf.A || !lf.D || !lf.W || lf.Pfn != 128 {
		t.Fatalf("merged 64-leaf=%+v, want size=64 A=1 D=1 W=1 pfn=128", lf)
	}
	if got := m2.Tables(); got != 0 {
		t.Fatalf("Tables=%d, want 0 after promoting the only table", got)
	}
	checkInvariants(t, m)
	checkInvariants(t, m2)
}

// TestSplitThenPromoteDoesNotRestoreAD: splitting inherits A/D to children,
// but a later merge takes the OR of the children's current bits; the original
// big leaf's bits are not restored.
func TestSplitThenPromoteDoesNotRestoreAD(t *testing.T) {
	m := newMapper(t, 10)
	mustOK(t, m.Map(0, 8, 16, true)) // A=0 D=0 originally
	n, err := m.Unmap(3, 1)          // splits into 1-page leaves, removes page 3
	mustOK(t, err)
	if n != 1 {
		t.Fatalf("Unmap(3,1)=%d, want 1", n)
	}
	mustOK(t, m.Map(3, 1, 19, true)) // refill to make promotion possible
	mustOK(t, m.Touch(1, false))     // A on leaf 1
	mustOK(t, m.Touch(5, true))      // A and D on leaf 5
	mustOK(t, m.Promote(0, 8))
	lf := m.Leaves()[0]
	if !lf.A || !lf.D {
		t.Fatalf("merged leaf=%+v, want A=1 D=1 (OR of children, not the original 0/0)", lf)
	}
	// And the converse: original bits do not survive a split+merge round trip.
	m2 := newMapper(t, 10)
	mustOK(t, m2.Map(0, 8, 16, true))
	mustOK(t, m2.Touch(2, true)) // original leaf A=1 D=1
	if _, err := m2.Unmap(3, 1); err != nil {
		t.Fatal(err)
	}
	mustOK(t, m2.Map(3, 1, 19, true)) // fresh leaf has A=0 D=0
	if got := len(m2.ScanDirty()); got != 7 {
		t.Fatalf("ScanDirty after split found %d dirty leaves, want 7 (all but the fresh one)", got)
	}
	mustOK(t, m2.Promote(0, 8))
	lf = m2.Leaves()[0]
	if !lf.A || lf.D {
		t.Fatalf("merged leaf=%+v, want A=1 D=0 (D was scanned off the children)", lf)
	}
	checkInvariants(t, m)
	checkInvariants(t, m2)
}

// TestRejectedOpsKeepState snapshots the full state around rejected operations.
func TestRejectedOpsKeepState(t *testing.T) {
	snapshot := func(m *Mapper) string {
		return fmt.Sprintf("%v|%d|%d|%d", m.Leaves(), m.Tables(), m.Mapped(), m.Epoch())
	}
	m := newMapper(t, 2)
	mustOK(t, m.Map(0, 8, 16, true))
	mustOK(t, m.Map(64, 64, 64, false))
	mustOK(t, m.Touch(0, true))
	before := snapshot(m)
	rejections := []func() error{
		func() error { return m.Map(0, 2, 0, true) },         // invalid size
		func() error { return m.Map(8, 8, 9, true) },         // misaligned pfn
		func() error { return m.Map(4, 1, 4, true) },         // overlap
		func() error { return m.Map(128, 1, 0, true) },       // quota (needs 2 tables)
		func() error { return m.Touch(200, false) },          // unmapped
		func() error { return m.Touch(64, true) },            // write protected
		func() error { return m.Promote(0, 8) },              // slot is a leaf
		func() error { return m.Promote(128, 64) },           // empty slot
		func() error { return m.Promote(3, 8) },              // misaligned
		func() error { _, err := m.Unmap(0, 0); return err }, // invalid range
	}
	for i, op := range rejections {
		if err := op(); err == nil {
			t.Fatalf("rejection %d unexpectedly succeeded", i)
		}
		if got := snapshot(m); got != before {
			t.Fatalf("rejection %d changed state: %s != %s", i, got, before)
		}
	}
	// Rejected unmap due to peak quota also keeps state.
	mustOK(t, m.Map(128, 64, 128, true))
	before = snapshot(m)
	if _, err := m.Unmap(130, 3); err == nil { // would split the [128,192) leaf: s=2, U=1+2>3
		t.Fatalf("Unmap(130,3) should exceed peak quota")
	}
	if got := snapshot(m); got != before {
		t.Fatalf("quota-rejected Unmap changed state")
	}
	checkInvariants(t, m)
}

// mapFull1 fills the whole address space with 1-page leaves (8 L1 + 64 L0
// tables, U=72).
func mapFull1(t *testing.T, m *Mapper) {
	t.Helper()
	for i := 0; i < NumPages; i++ {
		mustOK(t, m.Map(i, 1, i, true))
	}
	if got := m.Tables(); got != 72 {
		t.Fatalf("Tables=%d, want 72", got)
	}
}

func TestComplexityTranslate(t *testing.T) {
	m := newMapper(t, 72)
	mustOK(t, m.Map(0, 1, 0, true))
	mustOK(t, m.Map(64, 64, 64, true))
	if _, ok := m.Translate(0); !ok {
		t.Fatal("Translate(0) should hit")
	}
	if got := m.examined; got > 3 {
		t.Fatalf("Translate examined %d slots, want <= 3", got)
	}
	if _, ok := m.Translate(65); !ok {
		t.Fatal("Translate(65) should hit")
	}
	if got := m.examined; got != 1 {
		t.Fatalf("Translate into 64-leaf examined %d slots, want 1", got)
	}
	if _, ok := m.Translate(300); ok {
		t.Fatal("Translate(300) should miss")
	}
	if got := m.examined; got > 3 {
		t.Fatalf("Translate miss examined %d slots, want <= 3", got)
	}
}

func TestComplexityUnmapFull(t *testing.T) {
	m := newMapper(t, 72)
	mapFull1(t, m)
	n, err := m.Unmap(0, 512)
	mustOK(t, err)
	if n != 512 {
		t.Fatalf("Unmap(0,512)=%d, want 512", n)
	}
	if got := m.examined; got > 8 {
		t.Fatalf("Unmap(0,512) on full mapping examined %d slots, want <= 8", got)
	}
	if got := m.Tables(); got != 0 {
		t.Fatalf("Tables=%d, want 0", got)
	}
	if got := m.Mapped(); got != 0 {
		t.Fatalf("Mapped=%d, want 0", got)
	}
	checkInvariants(t, m)
}

func TestComplexityUnmapBounded(t *testing.T) {
	// Deep tables at both ends of the range, big leaves in the middle: the
	// worst case for the traversal.
	m := newMapper(t, 72)
	for i := 0; i < 8; i++ {
		mustOK(t, m.Map(i, 1, i, true))
	}
	for i := 504; i < 512; i++ {
		mustOK(t, m.Map(i, 1, i, true))
	}
	for base := 64; base < 448; base += 64 {
		mustOK(t, m.Map(base, 64, base, true))
	}
	mustOK(t, m.Map(8, 8, 8, true))
	mustOK(t, m.Map(496, 8, 496, true))
	ranges := [][2]int{{1, 510}, {3, 505}, {1, 8}, {7, 490}, {0, 512}, {63, 449}}
	for _, r := range ranges {
		mm := newMapper(t, 72)
		for i := 0; i < 8; i++ {
			mustOK(t, mm.Map(i, 1, i, true))
		}
		for i := 504; i < 512; i++ {
			mustOK(t, mm.Map(i, 1, i, true))
		}
		for base := 64; base < 448; base += 64 {
			mustOK(t, mm.Map(base, 64, base, true))
		}
		mustOK(t, mm.Map(8, 8, 8, true))
		mustOK(t, mm.Map(496, 8, 496, true))
		if _, err := mm.Unmap(r[0], r[1]); err != nil {
			t.Fatalf("Unmap(%d,%d): %v", r[0], r[1], err)
		}
		if got := mm.examined; got > 40 {
			t.Fatalf("Unmap(%d,%d) examined %d slots, want <= 40", r[0], r[1], got)
		}
		checkInvariants(t, mm)
	}
	_ = m
}

func TestComplexityPromote(t *testing.T) {
	m := newMapper(t, 72)
	for i := 0; i < 8; i++ {
		mustOK(t, m.Map(i, 1, 16+i, true))
	}
	mustOK(t, m.Promote(0, 8))
	if got := m.examined; got > 8 {
		t.Fatalf("Promote(0,8) examined %d slots, want <= 8", got)
	}
	m2 := newMapper(t, 72)
	for i := 0; i < 8; i++ {
		mustOK(t, m2.Map(i*8, 8, i*8, true))
	}
	mustOK(t, m2.Promote(0, 64))
	if got := m2.examined; got > 8 {
		t.Fatalf("Promote(0,64) examined %d slots, want <= 8", got)
	}
	// Rejected promotes scan at most 8 slots as well.
	m3 := newMapper(t, 72)
	for i := 0; i < 7; i++ {
		mustOK(t, m3.Map(i, 1, 16+i, true))
	}
	mustErrCode(t, m3.Promote(0, 8), ErrNotAllLeaves)
	if got := m3.examined; got > 8 {
		t.Fatalf("rejected Promote examined %d slots, want <= 8", got)
	}
}

func TestConcurrent(t *testing.T) {
	m := newMapper(t, 72)
	var wg sync.WaitGroup
	worker := func(seed int64) {
		defer wg.Done()
		rng := rand.New(rand.NewSource(seed))
		for i := 0; i < 400; i++ {
			vpn := rng.Intn(NumPages)
			switch rng.Intn(9) {
			case 0:
				size := []int{1, 8, 64}[rng.Intn(3)]
				_ = m.Map(vpn&^(size-1), size, rng.Intn(MaxPfn)&^(size-1), rng.Intn(2) == 0)
			case 1:
				_, _ = m.Unmap(vpn, 1+rng.Intn(NumPages-vpn))
			case 2:
				_ = m.Touch(vpn, rng.Intn(2) == 0)
			case 3:
				_ = m.Promote(vpn&^7, 8)
			case 4:
				_ = m.Promote(vpn&^63, 64)
			case 5:
				_ = m.ScanDirty()
			case 6:
				_, _ = m.Translate(vpn)
			case 7:
				_ = m.Leaves()
			case 8:
				_, _, _ = m.Tables(), m.Mapped(), m.Epoch()
			}
		}
	}
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go worker(int64(g)*7919 + 1)
	}
	wg.Wait()
	// After any serialized execution the structural invariants must hold.
	checkInvariants(t, m)
}
