package pagetable

import (
	"errors"
	"testing"
)

func mustNew(t *testing.T, quota int) *Mapper {
	t.Helper()
	m, err := New(quota)
	if err != nil {
		t.Fatalf("New(%d) failed: %v", quota, err)
	}
	return m
}

func mustMap(t *testing.T, m *Mapper, vpn, size, pfn int, w bool) {
	t.Helper()
	if err := m.Map(vpn, size, pfn, w); err != nil {
		t.Fatalf("Map(%d,%d,%d,%v) failed: %v", vpn, size, pfn, w, err)
	}
}

func mustUnmap(t *testing.T, m *Mapper, vpn, n int) int {
	t.Helper()
	got, err := m.Unmap(vpn, n)
	if err != nil {
		t.Fatalf("Unmap(%d,%d) failed: %v", vpn, n, err)
	}
	return got
}

func mustTouch(t *testing.T, m *Mapper, vpn int, write bool) {
	t.Helper()
	if err := m.Touch(vpn, write); err != nil {
		t.Fatalf("Touch(%d,%v) failed: %v", vpn, write, err)
	}
}

func mustPromote(t *testing.T, m *Mapper, vpn, size int) {
	t.Helper()
	if err := m.Promote(vpn, size); err != nil {
		t.Fatalf("Promote(%d,%d) failed: %v", vpn, size, err)
	}
}

func checkState(t *testing.T, m *Mapper, tables, mapped, epoch int) {
	t.Helper()
	if got := m.Tables(); got != tables {
		t.Fatalf("Tables() = %d, want %d", got, tables)
	}
	if got := m.Mapped(); got != mapped {
		t.Fatalf("Mapped() = %d, want %d", got, mapped)
	}
	if got := m.Epoch(); got != epoch {
		t.Fatalf("Epoch() = %d, want %d", got, epoch)
	}
}

func checkLeaves(t *testing.T, m *Mapper, want []LeafInfo) {
	t.Helper()
	got := m.Leaves()
	if len(got) != len(want) {
		t.Fatalf("Leaves() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Leaves()[%d] = %+v, want %+v (all: %v)", i, got[i], want[i], got)
		}
	}
}

func checkTranslate(t *testing.T, m *Mapper, vpn int, want Translation, wantOK bool) {
	t.Helper()
	got, ok := m.Translate(vpn)
	if ok != wantOK {
		t.Fatalf("Translate(%d) ok = %v, want %v", vpn, ok, wantOK)
	}
	if ok && got != want {
		t.Fatalf("Translate(%d) = %+v, want %+v", vpn, got, want)
	}
}

// TestWorkedExampleQuota4 replays the P=4 example from the specification.
func TestWorkedExampleQuota4(t *testing.T) {
	m := mustNew(t, 4)
	mustMap(t, m, 0, 64, 128, true)
	checkState(t, m, 0, 64, 1)

	mustTouch(t, m, 10, true)
	tr, ok := m.Translate(10)
	if !ok || !tr.A || !tr.D || !tr.W || tr.PFN != 138 || tr.Size != 64 {
		t.Fatalf("Translate(10) = %+v, %v", tr, ok)
	}

	// [3,8) partially intersects the 64-leaf: split to 8x8, then the [0,8)
	// child splits to 8x1; s=2 new tables, U=2 <= 4. Pages 3..7 removed.
	if got := mustUnmap(t, m, 3, 5); got != 5 {
		t.Fatalf("Unmap(3,5) = %d, want 5", got)
	}
	checkState(t, m, 2, 59, 2)
	if m.cUnmap > 40 {
		t.Fatalf("Unmap examined %d slots, want <= 40", m.cUnmap)
	}
	want := []LeafInfo{{Start: 0, Size: 1, PFN: 128, W: true, A: true, D: true},
		{Start: 1, Size: 1, PFN: 129, W: true, A: true, D: true},
		{Start: 2, Size: 1, PFN: 130, W: true, A: true, D: true}}
	for i := 1; i < 8; i++ {
		want = append(want, LeafInfo{Start: i * 8, Size: 8, PFN: 128 + i*8, W: true, A: true, D: true})
	}
	checkLeaves(t, m, want)

	// Unmap(0,3) empties the L0 table; it is reclaimed, U=1.
	if got := mustUnmap(t, m, 0, 3); got != 3 {
		t.Fatalf("Unmap(0,3) = %d, want 3", got)
	}
	checkState(t, m, 1, 56, 3)

	// Unmap(8,56) removes the remaining 7 eight-page leaves; the L1 table
	// becomes empty and is reclaimed, U=0.
	if got := mustUnmap(t, m, 8, 56); got != 56 {
		t.Fatalf("Unmap(8,56) = %d, want 56", got)
	}
	checkState(t, m, 0, 0, 4)
	checkLeaves(t, m, nil)
}

// TestWorkedExampleQuota1Rejected checks the same split is rejected when
// U+s exceeds the quota, leaving all state untouched.
func TestWorkedExampleQuota1Rejected(t *testing.T) {
	m := mustNew(t, 1)
	mustMap(t, m, 0, 64, 128, true)
	mustTouch(t, m, 10, true)
	before := m.Leaves()
	if _, err := m.Unmap(3, 5); !errors.Is(err, ErrQuota) {
		t.Fatalf("Unmap(3,5) err = %v, want ErrQuota", err)
	}
	checkState(t, m, 0, 64, 1)
	checkLeaves(t, m, before)
	tr, _ := m.Translate(10)
	if !tr.A || !tr.D {
		t.Fatalf("rejected Unmap changed A/D: %+v", tr)
	}
}

// TestWorkedExamplePromote replays the P=2 promote example.
func TestWorkedExamplePromote(t *testing.T) {
	m := mustNew(t, 2)
	for i := 0; i < 8; i++ {
		mustMap(t, m, i, 1, 16+i, true)
	}
	checkState(t, m, 2, 8, 8)
	mustTouch(t, m, 2, false) // A only on page 2
	mustTouch(t, m, 5, true)  // A and D only on page 5
	mustPromote(t, m, 0, 8)
	checkState(t, m, 1, 8, 9)
	checkLeaves(t, m, []LeafInfo{{Start: 0, Size: 8, PFN: 16, W: true, A: true, D: true}})
	if m.cPromote > 8 {
		t.Fatalf("Promote examined %d slots, want <= 8", m.cPromote)
	}
}

func TestNewValidation(t *testing.T) {
	for _, p := range []int{0, -1, MaxQuota + 1} {
		if _, err := New(p); !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("New(%d) err = %v, want ErrInvalidParam", p, err)
		}
	}
	if _, err := New(1); err != nil {
		t.Fatalf("New(1) failed: %v", err)
	}
	if _, err := New(MaxQuota); err != nil {
		t.Fatalf("New(MaxQuota) failed: %v", err)
	}
}

func TestMapThreeSizesAndAlignment(t *testing.T) {
	m := mustNew(t, 10)
	mustMap(t, m, 0, 64, 0, true)
	mustMap(t, m, 64, 8, 64, false)
	mustMap(t, m, 72, 1, 72, true)
	checkState(t, m, 2, 73, 3)
	checkTranslate(t, m, 0, Translation{PFN: 0, Size: 64, W: true}, true)
	checkTranslate(t, m, 63, Translation{PFN: 63, Size: 64, W: true}, true)
	checkTranslate(t, m, 64, Translation{PFN: 64, Size: 8}, true)
	checkTranslate(t, m, 71, Translation{PFN: 71, Size: 8}, true)
	checkTranslate(t, m, 72, Translation{PFN: 72, Size: 1, W: true}, true)
	checkTranslate(t, m, 73, Translation{}, false)
	checkTranslate(t, m, 511, Translation{}, false)
	checkTranslate(t, m, -1, Translation{}, false)
	checkTranslate(t, m, 512, Translation{}, false)

	// Invalid size and out-of-range vpn/pfn.
	for _, tc := range []struct{ vpn, size, pfn int }{
		{128, 2, 128}, {128, 0, 128}, {128, 16, 128},
		{-1, 1, 0}, {512, 1, 0}, {128, 1, -1}, {128, 1, MaxPFN + 1},
	} {
		if err := m.Map(tc.vpn, tc.size, tc.pfn, true); !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("Map(%d,%d,%d) err = %v, want ErrInvalidParam", tc.vpn, tc.size, tc.pfn, err)
		}
	}
	// vpn aligned but pfn not, and vice versa.
	for _, tc := range []struct{ vpn, size, pfn int }{
		{128, 8, 129}, // pfn misaligned for size 8
		{129, 8, 128}, // vpn misaligned for size 8
		{128, 64, 96}, // pfn misaligned for size 64
		{72, 64, 128}, // vpn misaligned for size 64
	} {
		if err := m.Map(tc.vpn, tc.size, tc.pfn, true); !errors.Is(err, ErrMisaligned) {
			t.Fatalf("Map(%d,%d,%d) err = %v, want ErrMisaligned", tc.vpn, tc.size, tc.pfn, err)
		}
	}
}

func TestMapOverlap(t *testing.T) {
	m := mustNew(t, 10)
	mustMap(t, m, 0, 64, 128, true)
	// Inside an existing 64-leaf at every level.
	for _, tc := range []struct{ vpn, size int }{
		{0, 64}, {0, 8}, {8, 8}, {0, 1}, {63, 1},
	} {
		if err := m.Map(tc.vpn, tc.size, 256, true); !errors.Is(err, ErrOverlap) {
			t.Fatalf("Map(%d,%d) err = %v, want ErrOverlap", tc.vpn, tc.size, err)
		}
	}
	// A big leaf covering existing small leaves.
	mustMap(t, m, 72, 1, 72, true)
	if err := m.Map(64, 64, 256, true); !errors.Is(err, ErrOverlap) {
		t.Fatalf("Map(64,64) err = %v, want ErrOverlap", err)
	}
	if err := m.Map(64, 8, 256, true); err != nil {
		t.Fatalf("Map(64,8) err = %v, want nil (adjacent, not overlapping)", err)
	}
	checkState(t, m, 2, 73, 3)
}

func TestMapQuotaExactAndShort(t *testing.T) {
	// A 1-page map needs L1+L0: quota 2 exactly fits, quota 1 is short by 1.
	m := mustNew(t, 2)
	mustMap(t, m, 0, 1, 16, true)
	checkState(t, m, 2, 1, 1)
	if err := m.Map(8, 1, 24, true); !errors.Is(err, ErrQuota) {
		t.Fatalf("Map(8,1) err = %v, want ErrQuota", err)
	}
	// But a sibling 1-page map inside the existing tables needs no new table.
	mustMap(t, m, 1, 1, 17, true)
	checkState(t, m, 2, 2, 2)

	m1 := mustNew(t, 1)
	if err := m1.Map(0, 1, 16, true); !errors.Is(err, ErrQuota) {
		t.Fatalf("Map(0,1) with P=1 err = %v, want ErrQuota", err)
	}
	checkState(t, m1, 0, 0, 0)
	// An 8-page map needs exactly one table.
	mustMap(t, m1, 0, 8, 16, true)
	checkState(t, m1, 1, 8, 1)
	// A 64-page map needs no table at all.
	m0 := mustNew(t, 1)
	mustMap(t, m0, 0, 64, 0, true)
	checkState(t, m0, 0, 64, 1)
}
