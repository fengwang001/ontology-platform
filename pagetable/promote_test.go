package pagetable

import (
	"errors"
	"testing"
)

func TestPromoteInvalidAndMisaligned(t *testing.T) {
	m := mustNew(t, 8)
	for _, tc := range []struct{ vpn, size int }{
		{0, 1}, {0, 2}, {0, 16}, {0, 512}, {-1, 8}, {512, 8},
	} {
		if err := m.Promote(tc.vpn, tc.size); !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("Promote(%d,%d) err = %v, want ErrInvalidParam", tc.vpn, tc.size, err)
		}
	}
	for _, tc := range []struct{ vpn, size int }{
		{1, 8}, {7, 8}, {63, 64}, {64, 64}, // 64 is aligned to 64; keep one real misalign
	} {
		err := m.Promote(tc.vpn, tc.size)
		if tc.vpn == 64 && tc.size == 64 {
			if !errors.Is(err, ErrNotTable) {
				t.Fatalf("Promote(64,64) err = %v, want ErrNotTable", err)
			}
			continue
		}
		if !errors.Is(err, ErrMisaligned) {
			t.Fatalf("Promote(%d,%d) err = %v, want ErrMisaligned", tc.vpn, tc.size, err)
		}
	}
}

// TestPromoteNotTable covers: empty slot, slot covered by a same-or-larger
// leaf, and (for size 8) a root slot that is a 64-leaf or empty.
func TestPromoteNotTable(t *testing.T) {
	m := mustNew(t, 8)
	// Empty root slot for size 64, empty L1 slot for size 8.
	if err := m.Promote(0, 64); !errors.Is(err, ErrNotTable) {
		t.Fatalf("Promote(0,64) on empty err = %v, want ErrNotTable", err)
	}
	if err := m.Promote(0, 8); !errors.Is(err, ErrNotTable) {
		t.Fatalf("Promote(0,8) on empty err = %v, want ErrNotTable", err)
	}
	// Root slot is a 64-leaf: both sizes must report not-a-table.
	mustMap(t, m, 0, 64, 128, true)
	if err := m.Promote(0, 64); !errors.Is(err, ErrNotTable) {
		t.Fatalf("Promote(0,64) on 64-leaf err = %v, want ErrNotTable", err)
	}
	if err := m.Promote(0, 8); !errors.Is(err, ErrNotTable) {
		t.Fatalf("Promote(0,8) under 64-leaf err = %v, want ErrNotTable", err)
	}
	// L1 slot is an 8-leaf.
	m2 := mustNew(t, 8)
	mustMap(t, m2, 0, 8, 64, true)
	if err := m2.Promote(0, 8); !errors.Is(err, ErrNotTable) {
		t.Fatalf("Promote(0,8) on 8-leaf err = %v, want ErrNotTable", err)
	}
}

// TestPromoteNotAllLeaves: the table exists but has empty slots or a child
// table instead of 8 leaves.
func TestPromoteNotAllLeaves(t *testing.T) {
	m := mustNew(t, 8)
	for i := 0; i < 7; i++ { // leave slot 7 empty
		mustMap(t, m, i, 1, 16+i, true)
	}
	if err := m.Promote(0, 8); !errors.Is(err, ErrNotAllLeaves) {
		t.Fatalf("Promote with empty child err = %v, want ErrNotAllLeaves", err)
	}
	// Replace the region with a child table inside the L1: 8 one-page leaves
	// under [8,16) make the L1 slot 0 an L0 table only if we use size-1 maps
	// in slot 0; instead build: L1 slot 0 = table with 8 leaves, L1 slot 1 =
	// table too, then try to promote the 64-region (children are tables).
	m2 := mustNew(t, 16)
	for i := 0; i < 8; i++ {
		mustMap(t, m2, i, 1, 16+i, true)
	}
	if err := m2.Promote(0, 64); !errors.Is(err, ErrNotAllLeaves) {
		t.Fatalf("Promote(0,64) with L0-table children err = %v, want ErrNotAllLeaves", err)
	}
}

// TestPromotePFN checks both pfn rejection modes: non-contiguous children,
// and contiguous children whose base pfn is not aligned to size.
func TestPromotePFN(t *testing.T) {
	// Non-contiguous pfn sequence.
	m := mustNew(t, 8)
	for i := 0; i < 8; i++ {
		pfn := 16 + i
		if i == 5 {
			pfn++ // break contiguity
		}
		mustMap(t, m, i, 1, pfn, true)
	}
	if err := m.Promote(0, 8); !errors.Is(err, ErrPFN) {
		t.Fatalf("Promote non-contiguous err = %v, want ErrPFN", err)
	}
	// Contiguous but base pfn not aligned to size: 17..24.
	m2 := mustNew(t, 8)
	for i := 0; i < 8; i++ {
		mustMap(t, m2, i, 1, 17+i, true)
	}
	if err := m2.Promote(0, 8); !errors.Is(err, ErrPFN) {
		t.Fatalf("Promote misaligned base err = %v, want ErrPFN", err)
	}
	// Same for size 64: 8 eight-leaves with contiguous pfn, base 8 (not 64-aligned).
	m3 := mustNew(t, 16)
	for i := 0; i < 8; i++ {
		mustMap(t, m3, i*8, 8, 8+i*8, true)
	}
	if err := m3.Promote(0, 64); !errors.Is(err, ErrPFN) {
		t.Fatalf("Promote(0,64) base 8 err = %v, want ErrPFN", err)
	}
	// And a working 64-promote with base 64.
	m4 := mustNew(t, 16)
	for i := 0; i < 8; i++ {
		mustMap(t, m4, i*8, 8, 64+i*8, true)
	}
	mustPromote(t, m4, 0, 64)
	checkLeaves(t, m4, []LeafInfo{{Start: 0, Size: 64, PFN: 64, W: true}})
	checkState(t, m4, 0, 64, 9)
}

func TestPromoteWInconsistent(t *testing.T) {
	m := mustNew(t, 8)
	for i := 0; i < 8; i++ {
		mustMap(t, m, i, 1, 16+i, i != 3) // child 3 is read-only
	}
	if err := m.Promote(0, 8); !errors.Is(err, ErrWInconsistent) {
		t.Fatalf("Promote mixed W err = %v, want ErrWInconsistent", err)
	}
	checkState(t, m, 2, 8, 8) // rejected: nothing changed
}

// TestPromoteMergesADKeepsW: A and D are the OR of the children; W is kept.
func TestPromoteMergesADKeepsW(t *testing.T) {
	m := mustNew(t, 8)
	for i := 0; i < 8; i++ {
		mustMap(t, m, i, 1, 16+i, false) // all read-only
	}
	mustTouch(t, m, 2, false) // A on child 2
	mustTouch(t, m, 6, false)
	// Force D on child 6 via a writable leaf elsewhere is impossible; use a
	// second mapper where the leaf is writable to set D.
	mustPromote(t, m, 0, 8)
	tr, _ := m.Translate(0)
	if !tr.A || tr.D || tr.W {
		t.Fatalf("promoted leaf = %+v, want A=1 D=0 W=0", tr)
	}

	m2 := mustNew(t, 8)
	for i := 0; i < 8; i++ {
		mustMap(t, m2, i, 1, 16+i, true)
	}
	mustTouch(t, m2, 5, true) // A=D on child 5 only
	mustPromote(t, m2, 0, 8)
	tr, _ = m2.Translate(7)
	if !tr.A || !tr.D || !tr.W || tr.PFN != 23 || tr.Size != 8 {
		t.Fatalf("promoted leaf = %+v, want A=1 D=1 W=1", tr)
	}
	checkState(t, m2, 1, 8, 9) // L0 reclaimed
}

// TestSplitThenPromoteDoesNotRestoreAD: after a split, per-child touches and
// a ScanDirty, promoting merges the *current* child bits, not the original
// big leaf's bits.
func TestSplitThenPromoteDoesNotRestoreAD(t *testing.T) {
	m := mustNew(t, 8)
	mustMap(t, m, 0, 8, 16, true)
	mustTouch(t, m, 0, true) // 8-leaf: A=1 D=1
	// Split the 8-leaf by unmapping page 0: children 1..7 inherit A=D=1.
	if got := mustUnmap(t, m, 0, 1); got != 1 {
		t.Fatalf("Unmap(0,1) = %d, want 1", got)
	}
	// Clear D everywhere, then dirty only child 3.
	if got := m.ScanDirty(); len(got) != 7 {
		t.Fatalf("ScanDirty = %d leaves, want 7", len(got))
	}
	mustTouch(t, m, 3, true)
	// Re-map page 0 with a clean leaf so the table has 8 leaves again.
	mustMap(t, m, 0, 1, 16, true)
	mustPromote(t, m, 0, 8)
	tr, _ := m.Translate(4)
	if !tr.A || !tr.D {
		t.Fatalf("promoted leaf = %+v, want A=1 (all children had A) D=1 (child 3)", tr)
	}
	// D came only from child 3's recent touch; the original big-leaf D was
	// cleared by ScanDirty and must not reappear on a fresh promote.
	m2 := mustNew(t, 8)
	mustMap(t, m2, 0, 8, 16, true)
	mustTouch(t, m2, 0, true)
	if got := m2.ScanDirty(); len(got) != 1 {
		t.Fatalf("ScanDirty = %d, want 1", len(got))
	}
	if got := mustUnmap(t, m2, 0, 1); got != 1 {
		t.Fatalf("Unmap = %d, want 1", got)
	}
	mustMap(t, m2, 0, 1, 16, true)
	mustPromote(t, m2, 0, 8)
	tr, _ = m2.Translate(4)
	if tr.D {
		t.Fatalf("promoted leaf D=1, want 0 (original D was scanned away)")
	}
}

// TestRejectedOpsKeepState: every rejected operation leaves leaves, bits,
// tables, U and epoch untouched.
func TestRejectedOpsKeepState(t *testing.T) {
	m := mustNew(t, 2)
	for i := 0; i < 8; i++ {
		mustMap(t, m, i, 1, 16+i, true)
	}
	mustTouch(t, m, 4, true)
	snapshot := func() string {
		return string(mustJSON(t, m))
	}
	before := snapshot()
	rejections := []func() error{
		func() error { return m.Map(0, 2, 64, true) },  // invalid size
		func() error { return m.Map(3, 8, 64, true) },  // misaligned
		func() error { return m.Map(0, 1, 64, true) },  // overlap
		func() error { return m.Map(64, 1, 64, true) }, // quota (P=2 full)
		func() error { return m.Touch(-1, false) },     // invalid
		func() error { return m.Touch(100, false) },    // unmapped
		func() error { return m.Promote(0, 16) },       // invalid size
		func() error { return m.Promote(3, 8) },        // misaligned
		func() error { return m.Promote(64, 8) },       // not a table
		func() error { return m.Promote(0, 64) },       // not all leaves
	}
	for i, op := range rejections {
		if err := op(); err == nil {
			t.Fatalf("rejection %d unexpectedly succeeded", i)
		}
		if got := snapshot(); got != before {
			t.Fatalf("rejection %d changed state\nbefore: %s\nafter:  %s", i, before, got)
		}
	}
	if _, err := m.Unmap(0, 0); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("Unmap(0,0) err = %v", err)
	}
	if got := snapshot(); got != before {
		t.Fatalf("rejected Unmap changed state")
	}
}

// mustJSON renders the observable state for equality checks.
func mustJSON(t *testing.T, m *Mapper) []byte {
	t.Helper()
	out := []byte{}
	for _, l := range m.Leaves() {
		out = append(out, []byte(string(rune(l.Start))+string(rune(l.Size))+string(rune(l.PFN)))...)
		if l.W {
			out = append(out, 'W')
		}
		if l.A {
			out = append(out, 'A')
		}
		if l.D {
			out = append(out, 'D')
		}
		out = append(out, ';')
	}
	out = append(out, []byte{
		byte(m.Tables()), byte(m.Mapped() % 251), byte(m.Epoch() % 251),
	}...)
	return out
}
