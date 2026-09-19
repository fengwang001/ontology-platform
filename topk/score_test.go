package topk

import (
	"errors"
	"math"
	"testing"
)

// NaN scores are rejected from ranking and counted by Skipped.
func TestNaNRejectedAndCounted(t *testing.T) {
	s := mustNew(t, 3, Desc)
	s.Push("ok", 1)
	s.Push("nan-1", math.NaN())
	s.Push("nan-2", math.NaN())
	s.Push("also-ok", 2)
	if got := s.Skipped(); got != 2 {
		t.Fatalf("Skipped() = %d, want 2", got)
	}
	if got := idsOf(s.Snapshot()); len(got) != 2 || got[0] != "also-ok" || got[1] != "ok" {
		t.Fatalf("snapshot ids = %v, want [also-ok ok]", got)
	}
	if s.Len() != 2 {
		t.Fatalf("Len() = %d, want 2", s.Len())
	}
}

// +0.0 and -0.0 compare equal and fall through to the ID tie-break.
func TestSignedZeroTreatedEqual(t *testing.T) {
	for _, dir := range []Direction{Desc, Asc} {
		s := mustNew(t, 2, dir)
		s.Push("b", 0.0)
		s.Push("a", math.Copysign(0, -1))
		got := s.Snapshot()
		if got[0].ID != "a" || got[1].ID != "b" {
			t.Fatalf("dir=%v: snapshot ids = %v, want [a b]", dir, idsOf(got))
		}
		// Overwriting a +0.0 score with -0.0 must find the same entry.
		s.Push("b", math.Copysign(0, -1))
		if s.Len() != 2 {
			t.Fatalf("dir=%v: Len() = %d after re-push, want 2", dir, s.Len())
		}
	}
}

// Infinities are legal scores and rank normally.
func TestInfinityScoresAllowed(t *testing.T) {
	s := mustNew(t, 2, Desc)
	s.Push("inf", math.Inf(1))
	s.Push("big", 1e300)
	s.Push("neg-inf", math.Inf(-1))
	got := idsOf(s.Snapshot())
	if got[0] != "inf" || got[1] != "big" {
		t.Fatalf("Desc snapshot ids = %v, want [inf big]", got)
	}
	a := mustNew(t, 2, Asc)
	a.Push("inf", math.Inf(1))
	a.Push("big", 1e300)
	a.Push("neg-inf", math.Inf(-1))
	if got := idsOf(a.Snapshot()); got[0] != "neg-inf" || got[1] != "big" {
		t.Fatalf("Asc snapshot ids = %v, want [neg-inf big]", got)
	}
}

// Re-pushing an ID overwrites its score; a score that becomes worse
// re-ranks immediately and can let a newcomer evict the ID entirely.
func TestOverwriteDropsOutOfTopK(t *testing.T) {
	s := mustNew(t, 2, Desc)
	s.Push("a", 5)
	s.Push("b", 4)
	s.Push("c", 3)
	if got := idsOf(s.Snapshot()); got[0] != "a" || got[1] != "b" {
		t.Fatalf("snapshot ids = %v, want [a b]", got)
	}
	s.Push("a", 2) // a drops behind b at once
	if got := idsOf(s.Snapshot()); got[0] != "b" || got[1] != "a" {
		t.Fatalf("after overwrite snapshot ids = %v, want [b a]", got)
	}
	s.Push("d", 3) // d only fits because a fell to 2; a is evicted
	got := idsOf(s.Snapshot())
	if got[0] != "b" || got[1] != "d" {
		t.Fatalf("final snapshot ids = %v, want [b d]", got)
	}
}

// Overwriting must never duplicate an ID in the snapshot.
func TestOverwriteNeverDuplicates(t *testing.T) {
	s := mustNew(t, 3, Asc)
	for i := 0; i < 10; i++ {
		s.Push("solo", float64(i))
		s.Push("other", 100)
	}
	if s.Len() != 2 {
		t.Fatalf("Len() = %d, want 2", s.Len())
	}
	seen := map[string]int{}
	for _, e := range s.Snapshot() {
		seen[e.ID]++
	}
	if seen["solo"] != 1 || seen["other"] != 1 {
		t.Fatalf("duplicate IDs in snapshot: %v", seen)
	}
	// The last write wins: solo's score is 9, not any earlier value.
	for _, e := range s.Snapshot() {
		if e.ID == "solo" && e.Score != 9 {
			t.Fatalf("solo score = %v, want 9 (last write wins)", e.Score)
		}
	}
}

// K <= 0 yields a detectable error, never a panic.
func TestInvalidCapacityError(t *testing.T) {
	for _, k := range []int{0, -1, -100} {
		s, err := New(k, Desc)
		if !errors.Is(err, ErrInvalidCapacity) {
			t.Fatalf("New(%d): err = %v, want ErrInvalidCapacity", k, err)
		}
		if s != nil {
			t.Fatalf("New(%d): selector = %v, want nil", k, s)
		}
	}
}
