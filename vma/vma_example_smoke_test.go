package vma

import (
	"errors"
	"testing"
)

func TestSmokeWorkedExample(t *testing.T) {
	m, err := New(1, 64, 8, 2, 20)
	if err != nil {
		t.Fatal(err)
	}
	rw := 3
	r := 1

	check := func(got int64, gerr error, want int64) {
		t.Helper()
		if gerr != nil || got != want {
			t.Fatalf("got %d,%v want %d; table=%v", got, gerr, want, m.VMAs())
		}
	}

	s, e := m.Mmap(0, 10, rw, 0, 0, 0)
	check(s, e, 54)
	s, e = m.Mmap(0, 5, rw, 0, 0, 0)
	check(s, e, 49)
	s, e = m.Mmap(0, 4, r, 0, 0, 0)
	check(s, e, 45)
	s, e = m.Mmap(0, 3, rw, FlagGrowsDown, 0, 0)
	check(s, e, 42)
	s, e = m.Mmap(0, 10, rw, 0, 0, 0)
	check(s, e, 30)
	if m.Count() != 4 {
		t.Fatalf("count=%d table=%v", m.Count(), m.VMAs())
	}

	if err := m.Mprotect(50, 4, r); err != nil {
		t.Fatal(err)
	}
	want := []VMA{
		{Start: 30, End: 40, Perm: rw},
		{Start: 42, End: 45, Perm: rw, GrowsDown: true},
		{Start: 45, End: 49, Perm: r},
		{Start: 49, End: 50, Perm: rw},
		{Start: 50, End: 54, Perm: r},
		{Start: 54, End: 64, Perm: rw},
	}
	got := m.VMAs()
	if len(got) != len(want) {
		t.Fatalf("after mprotect table=%v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("[%d] got %+v want %+v", i, got[i], want[i])
		}
	}

	n, err := m.Munmap(51, 2)
	if err != nil || n != 2 {
		t.Fatalf("munmap %d %v", n, err)
	}
	if m.Count() != 7 {
		t.Fatalf("count=%d table=%v", m.Count(), m.VMAs())
	}

	_, err = m.Munmap(31, 2)
	if !errors.Is(err, ErrTooMany) {
		t.Fatalf("got %v want ErrTooMany; table=%v", err, m.VMAs())
	}

	_, err = m.Grow(41)
	if !errors.Is(err, ErrNoRoom) {
		t.Fatalf("got %v want ErrNoRoom", err)
	}
}
