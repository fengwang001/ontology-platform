package pagetable

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

// TestRandomizedDifferential replays random operation sequences against the
// naive simulator and compares every return value plus the full observable
// state after each step. Every operation is logged with its input, output
// and the state used as the decision basis; the log is dumped on failure.
func TestRandomizedDifferential(t *testing.T) {
	const sequences = 2000
	for seq := 0; seq < sequences; seq++ {
		rng := rand.New(rand.NewSource(int64(seq)*7919 + 13))
		quota := 1 + rng.Intn(80)
		if seq%17 == 0 {
			quota = 1 + rng.Intn(MaxQuota) // occasionally a huge quota
		}
		m, err := New(quota)
		if err != nil {
			t.Fatalf("seq %d: New(%d): %v", seq, quota, err)
		}
		s := newSim(quota)
		var log strings.Builder
		fmt.Fprintf(&log, "seq=%d quota=%d\n", seq, quota)
		ops := 30 + rng.Intn(30)
		fail := func(format string, args ...interface{}) {
			t.Fatalf("seq %d mismatch: %s\n--- operation log ---\n%s", seq, fmt.Sprintf(format, args...), log.String())
		}
		for step := 0; step < ops; step++ {
			switch rng.Intn(100) {
			case 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29:
				size := []int{1, 8, 64}[rng.Intn(3)]
				vpn := rng.Intn(512)
				if rng.Intn(4) > 0 {
					vpn = vpn / size * size // usually aligned
				}
				pfn := rng.Intn(1 << 20)
				if rng.Intn(4) > 0 {
					pfn = pfn / size * size
				}
				w := rng.Intn(2) == 0
				errM := m.Map(vpn, size, pfn, w)
				errS := s.mapLeaf(vpn, size, pfn, w)
				fmt.Fprintf(&log, "Map(%d,%d,%d,%v) -> mapper:%s sim:%s | U=%d mapped=%d epoch=%d\n",
					vpn, size, pfn, w, errString(errM), errString(errS), m.Tables(), m.Mapped(), m.Epoch())
				if errString(errM) != errString(errS) {
					fail("Map(%d,%d,%d,%v): mapper=%s sim=%s", vpn, size, pfn, w, errString(errM), errString(errS))
				}
			case 30, 31, 32, 33, 34, 35, 36, 37, 38, 39, 40, 41, 42, 43, 44, 45, 46, 47, 48, 49, 50, 51, 52, 53, 54:
				vpn := rng.Intn(512)
				n := 1 + rng.Intn(512-vpn)
				gotM, errM := m.Unmap(vpn, n)
				gotS, errS := s.unmap(vpn, n)
				fmt.Fprintf(&log, "Unmap(%d,%d) -> mapper:(%d,%s) sim:(%d,%s) | U=%d mapped=%d epoch=%d slots=%d\n",
					vpn, n, gotM, errString(errM), gotS, errString(errS), m.Tables(), m.Mapped(), m.Epoch(), m.cUnmap)
				if gotM != gotS || errString(errM) != errString(errS) {
					fail("Unmap(%d,%d): mapper=(%d,%s) sim=(%d,%s)", vpn, n, gotM, errString(errM), gotS, errString(errS))
				}
				if errM == nil && m.cUnmap > 40 {
					fail("Unmap(%d,%d) examined %d slots, want <= 40", vpn, n, m.cUnmap)
				}
			case 55, 56, 57, 58, 59, 60, 61, 62, 63, 64, 65, 66, 67, 68, 69:
				vpn := rng.Intn(514) - 1 // occasionally invalid
				write := rng.Intn(2) == 0
				errM := m.Touch(vpn, write)
				errS := s.touch(vpn, write)
				fmt.Fprintf(&log, "Touch(%d,%v) -> mapper:%s sim:%s\n", vpn, write, errString(errM), errString(errS))
				if errString(errM) != errString(errS) {
					fail("Touch(%d,%v): mapper=%s sim=%s", vpn, write, errString(errM), errString(errS))
				}
			case 70, 71, 72, 73, 74, 75, 76, 77, 78, 79:
				size := []int{8, 64}[rng.Intn(2)]
				vpn := rng.Intn(512)
				if rng.Intn(3) > 0 {
					vpn = vpn / size * size
				}
				errM := m.Promote(vpn, size)
				errS := s.promote(vpn, size)
				fmt.Fprintf(&log, "Promote(%d,%d) -> mapper:%s sim:%s | U=%d epoch=%d slots=%d\n",
					vpn, size, errString(errM), errString(errS), m.Tables(), m.Epoch(), m.cPromote)
				if errString(errM) != errString(errS) {
					fail("Promote(%d,%d): mapper=%s sim=%s", vpn, size, errString(errM), errString(errS))
				}
				if errM == nil && m.cPromote > 8 {
					fail("Promote(%d,%d) examined %d slots, want <= 8", vpn, size, m.cPromote)
				}
			case 80, 81, 82, 83, 84:
				gotM := m.ScanDirty()
				gotS := s.scanDirty()
				fmt.Fprintf(&log, "ScanDirty() -> mapper:%d sim:%d leaves\n", len(gotM), len(gotS))
				if !leafInfosEqual(gotM, gotS) {
					fail("ScanDirty: mapper=%v sim=%v", gotM, gotS)
				}
			default:
				vpn := rng.Intn(514) - 1
				gotM, okM := m.Translate(vpn)
				gotS, okS := s.translate(vpn)
				fmt.Fprintf(&log, "Translate(%d) -> mapper:(%+v,%v) sim:(%+v,%v) slots=%d\n", vpn, gotM, okM, gotS, okS, m.cTranslate)
				if gotM != gotS || okM != okS {
					fail("Translate(%d): mapper=(%+v,%v) sim=(%+v,%v)", vpn, gotM, okM, gotS, okS)
				}
				if m.cTranslate > 3 {
					fail("Translate(%d) examined %d slots, want <= 3", vpn, m.cTranslate)
				}
			}
			// Full state comparison after every operation.
			if got, want := m.Tables(), s.tablesUsed(); got != want {
				fail("Tables()=%d sim=%d", got, want)
			}
			if got, want := m.Mapped(), s.mapped(); got != want {
				fail("Mapped()=%d sim=%d", got, want)
			}
			if got, want := m.Epoch(), s.epoch; got != want {
				fail("Epoch()=%d sim=%d", got, want)
			}
			if got, want := m.Leaves(), s.leafInfos(); !leafInfosEqual(got, want) {
				fail("Leaves()=%v sim=%v", got, want)
			}
		}
		// End-of-sequence sweep: translate every page and compare.
		for vpn := 0; vpn < 512; vpn++ {
			gotM, okM := m.Translate(vpn)
			gotS, okS := s.translate(vpn)
			if gotM != gotS || okM != okS {
				fail("final Translate(%d): mapper=(%+v,%v) sim=(%+v,%v)", vpn, gotM, okM, gotS, okS)
			}
		}
		if seq < 3 {
			t.Logf("sequence %d log (quota=%d, %d ops):\n%s", seq, quota, ops, log.String())
		}
	}
}

func leafInfosEqual(a, b []LeafInfo) bool {
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

// TestInvariantsAfterRandomOps checks the structural invariants that must
// hold at all times: U equals the table count derived from the leaf set,
// Mapped equals the sum of leaf sizes, no two leaves overlap, and Leaves is
// sorted.
func TestInvariantsAfterRandomOps(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 200; trial++ {
		m, _ := New(1 + rng.Intn(100))
		for step := 0; step < 60; step++ {
			switch rng.Intn(5) {
			case 0:
				size := []int{1, 8, 64}[rng.Intn(3)]
				_ = m.Map(rng.Intn(512)/size*size, size, rng.Intn(1<<20)/size*size, rng.Intn(2) == 0)
			case 1:
				vpn := rng.Intn(512)
				_, _ = m.Unmap(vpn, 1+rng.Intn(512-vpn))
			case 2:
				_ = m.Touch(rng.Intn(512), rng.Intn(2) == 0)
			case 3:
				size := []int{8, 64}[rng.Intn(2)]
				_ = m.Promote(rng.Intn(512)/size*size, size)
			case 4:
				_ = m.ScanDirty()
			}
		}
		leaves := m.Leaves()
		mapped := 0
		prevEnd := 0
		for i, l := range leaves {
			if i > 0 && l.Start < prevEnd {
				t.Fatalf("trial %d: overlapping leaves: %v", trial, leaves)
			}
			if l.Size != 1 && l.Size != 8 && l.Size != 64 {
				t.Fatalf("trial %d: bad leaf size: %+v", trial, l)
			}
			if l.Start%l.Size != 0 || l.PFN%l.Size != 0 {
				t.Fatalf("trial %d: misaligned leaf: %+v", trial, l)
			}
			prevEnd = l.Start + l.Size
			mapped += l.Size
		}
		if got := m.Mapped(); got != mapped {
			t.Fatalf("trial %d: Mapped()=%d, leaf sum=%d", trial, got, mapped)
		}
		// Derive the table count from the leaf set.
		s := &sim{leaves: nil}
		for _, l := range leaves {
			s.leaves = append(s.leaves, simLeaf{l.Start, l.Size, l.PFN, l.W, l.A, l.D})
		}
		if got, want := m.Tables(), s.tablesUsed(); got != want {
			t.Fatalf("trial %d: Tables()=%d, derived=%d", trial, got, want)
		}
	}
}
