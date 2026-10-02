package ontology

import (
	"errors"
	"testing"
)

func TestPlacementAndGuards(t *testing.T) {
	m, _ := New(1, 40, 20, 2, 20)
	m.Mmap(10, 4, 1, Fixed, 0, 0)
	m.Mmap(30, 4, 1, Fixed, 0, 0)
	if start, _ := m.Mmap(0, 6, 1, 0, 0, 0); start != 34 {
		t.Fatalf("highest free start = %d, want 34", start)
	}
	stack, _ := m.Mmap(0, 2, 3, GrowsDown, 0, 0)
	if stack != 28 {
		t.Fatalf("stack start = %d, want 28", stack)
	}
	if start, err := m.Mmap(20, 2, 1, 0, 0, 0); err != nil || start != 20 {
		t.Fatalf("hint in real gap = (%d,%v)", start, err)
	}
	guardOnly, _ := New(1, 40, 20, 2, 20)
	guardOnly.Mmap(30, 2, 3, Fixed|GrowsDown, 0, 0)
	if start, err := guardOnly.Mmap(28, 2, 1, 0, 0, 0); err != nil || start != 38 {
		t.Fatalf("blocked hint falls back to highest free = (%d,%v), want 38", start, err)
	}
}

func TestMergeRules(t *testing.T) {
	m, _ := New(1, 80, 20, 1, 40)
	first, _ := m.Mmap(10, 4, 1, Fixed, 7, 100)
	second, _ := m.Mmap(14, 4, 1, Fixed, 7, 104)
	if first != 10 || second != 14 || m.Count() != 1 {
		t.Fatalf("continuous file mappings did not merge: %d,%d count=%d", first, second, m.Count())
	}
	if _, err := m.Mmap(18, 4, 1, Fixed, 7, 109); err != nil || m.Count() != 2 {
		t.Fatalf("offset gap should remain separate, count=%d err=%v", m.Count(), err)
	}
	if _, err := m.Mmap(22, 4, 1, Fixed|GrowsDown, 0, 0); err != nil || m.Count() != 3 {
		t.Fatalf("different growsdown flag should remain separate, count=%d err=%v", m.Count(), err)
	}
}

func TestFixedAndNoReplace(t *testing.T) {
	m, _ := New(1, 40, 8, 1, 40)
	m.Mmap(10, 10, 1, 0, 0, 0)
	_, err := m.Mmap(15, 2, 1, Fixed|NoReplace, 0, 0)
	if !errors.Is(err, ErrExists) || m.Count() != 1 {
		t.Fatalf("NOREPLACE = %v count=%d", err, m.Count())
	}
	start, err := m.Mmap(15, 2, 3, Fixed, 0, 0)
	if err != nil || start != 15 || m.Count() != 3 {
		t.Fatalf("FIXED split = (%d,%v) count=%d", start, err, m.Count())
	}
	_, err = m.Mmap(10, 20, 2, Fixed, 0, 0)
	if err != nil || m.Count() != 1 {
		t.Fatalf("FIXED full cover = %v count=%d", err, m.Count())
	}
}

func TestPeakLimits(t *testing.T) {
	m, _ := New(1, 40, 2, 1, 40)
	m.Mmap(10, 10, 1, Fixed, 0, 0)
	_, err := m.Munmap(12, 6)
	if !errors.Is(err, ErrTooMany) || m.Count() != 1 {
		t.Fatalf("munmap peak = %v count=%d", err, m.Count())
	}
	err = m.Mprotect(12, 6, 2)
	if !errors.Is(err, ErrTooMany) || m.Count() != 1 {
		t.Fatalf("mprotect peak = %v count=%d", err, m.Count())
	}
	m.Mmap(30, 10, 1, Fixed, 0, 0)
	_, err = m.Mmap(15, 20, 2, Fixed, 0, 0)
	if !errors.Is(err, ErrTooMany) || m.Count() != 2 {
		t.Fatalf("fixed mmap peak = %v count=%d", err, m.Count())
	}
}

func TestSplitButNoNetIncreasePeak(t *testing.T) {
	m, _ := New(1, 40, 2, 1, 40)
	m.Mmap(10, 10, 1, Fixed, 0, 0)
	err := m.Mprotect(12, 6, 2)
	if !errors.Is(err, ErrTooMany) || m.Count() != 1 {
		t.Fatalf("no-net-increase peak = %v count=%d", err, m.Count())
	}
}

func TestMprotectCoverageAndMerge(t *testing.T) {
	m, _ := New(1, 40, 20, 1, 40)
	m.Mmap(10, 10, 1, Fixed, 0, 0)
	if pages, err := m.Munmap(25, 2); err != nil || pages != 0 || m.Count() != 1 {
		t.Fatalf("empty munmap = (%d,%v) count=%d", pages, err, m.Count())
	}
	if err := m.Mprotect(15, 2, 1); err != nil || m.Count() != 1 {
		t.Fatalf("same perm changed table: %v count=%d", err, m.Count())
	}
	if err := m.Mprotect(15, 4, 2); err != nil || m.Count() != 3 {
		t.Fatalf("mprotect split count=%d err=%v", m.Count(), err)
	}
	if err := m.Mprotect(10, 9, 1); err != nil || m.Count() != 1 {
		t.Fatalf("mprotect merge count=%d err=%v", m.Count(), err)
	}
	m.Munmap(14, 2)
	if err := m.Mprotect(13, 4, 1); !errors.Is(err, ErrNoMem) || m.Count() != 2 {
		t.Fatalf("hole mprotect = %v count=%d", err, m.Count())
	}
}

func TestGrow(t *testing.T) {
	m, _ := New(1, 40, 20, 3, 8)
	m.Mmap(20, 4, 3, Fixed|GrowsDown, 0, 0)
	m.Mmap(10, 1, 3, Fixed, 0, 0)
	if _, err := m.Grow(20); !errors.Is(err, ErrMapped) {
		t.Fatalf("mapped grow = %v", err)
	}
	if _, err := m.Grow(2); !errors.Is(err, ErrSegv) {
		t.Fatalf("no stack = %v", err)
	}
	if _, err := m.Grow(15); !errors.Is(err, ErrStackLimit) {
		t.Fatalf("stack limit = %v", err)
	}
	roomManager, _ := New(1, 40, 20, 3, 20)
	roomManager.Mmap(20, 4, 3, Fixed|GrowsDown, 0, 0)
	roomManager.Mmap(10, 1, 3, Fixed, 0, 0)
	if _, err := roomManager.Grow(13); !errors.Is(err, ErrNoRoom) {
		t.Fatalf("guard room = %v", err)
	}
	successManager, _ := New(1, 40, 20, 3, 20)
	successManager.Mmap(20, 4, 3, Fixed|GrowsDown, 0, 0)
	if addr, err := successManager.Grow(16); err != nil || addr != 16 || successManager.Count() != 1 {
		t.Fatalf("grow = (%d,%v) count=%d", addr, err, successManager.Count())
	}
	if v, ok := successManager.Find(16); !ok || v.Start != 16 || v.End != 24 {
		t.Fatalf("grown vma = %#v ok=%v", v, ok)
	}
}
