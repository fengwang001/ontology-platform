package topk

import (
	"errors"
	"fmt"
	"math"
	"testing"
)

func TestNonPositiveCapacityReturnsError(t *testing.T) {
	for _, k := range []int{0, -1, -100} {
		s, err := New(k, Desc)
		if !errors.Is(err, ErrNonPositiveCapacity) {
			t.Fatalf("New(%d): err=%v, want ErrNonPositiveCapacity", k, err)
		}
		if s != nil {
			t.Fatalf("New(%d): selector=%v, want nil", k, s)
		}
	}
	if _, err := New(0, Asc); !errors.Is(err, ErrNonPositiveCapacity) {
		t.Fatalf("New(0, Asc): err=%v, want ErrNonPositiveCapacity", err)
	}
}

func TestNaNScoresAreSkippedAndCounted(t *testing.T) {
	s, err := New(3, Desc)
	if err != nil {
		t.Fatal(err)
	}
	s.Push("nan1", math.NaN())
	s.Push("ok", 1.0)
	s.Push("nan2", math.NaN())
	if got := s.Skipped(); got != 2 {
		t.Fatalf("Skipped()=%d, want 2", got)
	}
	assertOrder(t, s, []string{"ok"})
	if s.Size() != 1 {
		t.Fatalf("Size()=%d, want 1", s.Size())
	}
}

// +0.0 and -0.0 compare equal and must be ordered by ID, never by the
// sign bit.
func TestPositiveAndNegativeZeroAreEqual(t *testing.T) {
	s, err := New(2, Desc)
	if err != nil {
		t.Fatal(err)
	}
	s.Push("b", 0.0)
	s.Push("a", math.Copysign(0, -1))
	assertOrder(t, s, []string{"a", "b"})

	s2, err := New(2, Asc)
	if err != nil {
		t.Fatal(err)
	}
	s2.Push("b", math.Copysign(0, -1))
	s2.Push("a", 0.0)
	assertOrder(t, s2, []string{"a", "b"})
}

// Infinities are legal scores and rank normally.
func TestInfinitiesParticipate(t *testing.T) {
	s, err := New(2, Desc)
	if err != nil {
		t.Fatal(err)
	}
	s.Push("inf", math.Inf(1))
	s.Push("big", 1e300)
	s.Push("neginf", math.Inf(-1))
	assertOrder(t, s, []string{"inf", "big"})
}

// Re-pushing an ID overwrites its score. The new score is reflected in the
// ranking immediately: the element sinks to its new position, and a worse
// score lets a previously rejected arrival evict it from the top K. No ID
// ever appears twice.
func TestOverwriteCanDropOutOfTopK(t *testing.T) {
	s, err := New(2, Desc)
	if err != nil {
		t.Fatal(err)
	}
	s.Push("a", 10.0)
	s.Push("b", 9.0)
	s.Push("c", 8.0)
	assertOrder(t, s, []string{"a", "b"})

	s.Push("a", 1.0) // overwrite with a much worse score: sinks at once
	assertOrder(t, s, []string{"b", "a"})

	s.Push("d", 5.0) // would have lost to the old a=10.0; now evicts it
	assertOrder(t, s, []string{"b", "d"})

	s.Push("a", 100.0) // overwrite again, back on top
	assertOrder(t, s, []string{"a", "b"})

	for _, e := range s.Snapshot() {
		if e.ID == "a" && e.Score != 100.0 {
			t.Fatalf("stale score for overwritten ID: %+v", e)
		}
	}
}

// K larger than the number of pushed elements returns everything.
func TestCapacityLargerThanStream(t *testing.T) {
	s, err := New(10, Asc)
	if err != nil {
		t.Fatal(err)
	}
	s.Push("x", 2.0)
	s.Push("y", 1.0)
	assertOrder(t, s, []string{"y", "x"})
}

// On a long stream the number of held elements must never exceed K.
func TestSizeNeverExceedsCapacity(t *testing.T) {
	const k = 7
	s, err := New(k, Desc)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100000; i++ {
		s.Push(fmt.Sprintf("id-%d", i), float64(i%997))
		if n := s.Size(); n > k {
			t.Fatalf("after %d pushes Size()=%d exceeds K=%d", i+1, n, k)
		}
	}
	if s.Size() != k {
		t.Fatalf("final Size()=%d, want %d", s.Size(), k)
	}
}
