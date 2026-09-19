package topk

import (
	"errors"
	"fmt"
	"testing"
)

func TestNonPositiveCapacityReturnsError(t *testing.T) {
	for _, k := range []int{0, -1, -100} {
		s, err := New(k, Desc)
		if !errors.Is(err, ErrNonPositiveCapacity) {
			t.Fatalf("New(%d) err = %v, want ErrNonPositiveCapacity", k, err)
		}
		if s != nil {
			t.Fatalf("New(%d) returned non-nil selector", k)
		}
	}
	if _, err := New(0, Asc); !errors.Is(err, ErrNonPositiveCapacity) {
		t.Fatalf("Asc New(0) err = %v", err)
	}
}

func TestSnapshotReturnsAllWhenFewerThanK(t *testing.T) {
	s, err := New(10, Desc)
	if err != nil {
		t.Fatal(err)
	}
	s.Push("a", 1.0)
	s.Push("b", 2.0)
	if got := len(s.Snapshot()); got != 2 {
		t.Fatalf("Snapshot len = %d, want 2", got)
	}
	if s.Len() != 2 {
		t.Fatalf("Len = %d, want 2", s.Len())
	}
}

func TestLongStreamNeverExceedsK(t *testing.T) {
	const k = 7
	for _, dir := range []Direction{Desc, Asc} {
		s, err := New(k, dir)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 100000; i++ {
			s.Push(fmt.Sprintf("id-%d", i), float64((i*7919)%1000))
			if n := s.Len(); n > k {
				t.Fatalf("dir=%v push %d: Len = %d exceeds K=%d", dir, i, n, k)
			}
		}
		if got := len(s.Snapshot()); got != k {
			t.Fatalf("dir=%v final snapshot len = %d, want %d", dir, got, k)
		}
	}
}

func TestSnapshotIsIndependentCopy(t *testing.T) {
	s, err := New(2, Desc)
	if err != nil {
		t.Fatal(err)
	}
	s.Push("a", 1.0)
	s.Push("b", 2.0)
	snap := s.Snapshot()
	snap[0] = Item{ID: "corrupted", Score: -1}
	got := ids(s.Snapshot())
	if got[0] != "b" || got[1] != "a" {
		t.Fatalf("mutating snapshot affected selector: %v", got)
	}
	s.Push("c", 3.0)
	if ids(s.Snapshot())[0] != "c" {
		t.Fatalf("push after snapshot not reflected")
	}
}
