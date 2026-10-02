package ontology

import (
	"errors"
	"testing"
)

func TestSpecExample(t *testing.T) {
	m, err := New(1, 64, 8, 2, 20)
	if err != nil {
		t.Fatal(err)
	}
	rw := 3
	r := 1

	mustMmap(t, m, 0, 10, rw, 0, 0, 0, 54)
	mustMmap(t, m, 0, 5, rw, 0, 0, 0, 49)
	mustMmap(t, m, 0, 4, r, 0, 0, 0, 45)
	mustMmap(t, m, 0, 3, rw, GrowsDown, 0, 0, 42)
	mustMmap(t, m, 0, 10, rw, 0, 0, 0, 30)

	if got := m.Count(); got != 4 {
		t.Fatalf("count before mprotect = %d, want 4", got)
	}
	if err := m.Mprotect(50, 4, r); err != nil {
		t.Fatalf("mprotect: %v", err)
	}
	want := []VMA{
		{Start: 30, End: 40, Perm: 3, Anonymous: true},
		{Start: 42, End: 45, Perm: 3, GrowsDown: true, Anonymous: true},
		{Start: 45, End: 49, Perm: 1, Anonymous: true},
		{Start: 49, End: 50, Perm: 3, Anonymous: true},
		{Start: 50, End: 54, Perm: 1, Anonymous: true},
		{Start: 54, End: 64, Perm: 3, Anonymous: true},
	}
	assertVMAs(t, m, want)

	pages, err := m.Munmap(51, 2)
	if err != nil || pages != 2 {
		t.Fatalf("munmap 51 = (%d,%v), want 2,nil", pages, err)
	}
	_, err = m.Munmap(31, 2)
	if !errors.Is(err, ErrTooMany) {
		t.Fatalf("munmap 31 error = %v, want ErrTooMany", err)
	}

	_, err = m.Grow(41)
	if !errors.Is(err, ErrNoRoom) {
		t.Fatalf("grow 41 = %v, want ErrNoRoom", err)
	}
}

func mustMmap(t *testing.T, m *Manager, hint, length int64, perm, flags int, file, off, want int64) {
	t.Helper()
	got, err := m.Mmap(hint, length, perm, flags, file, off)
	if err != nil || got != want {
		t.Fatalf("Mmap(%d,%d,flags=%b) = (%d,%v), want %d", hint, length, flags, got, err, want)
	}
}

func assertVMAs(t *testing.T, m *Manager, want []VMA) {
	t.Helper()
	got := m.VMAs()
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("vma[%d] = %#v, want %#v", i, got[i], want[i])
		}
	}
}
