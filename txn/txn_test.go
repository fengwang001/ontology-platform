package txn

import (
	"fmt"
	"testing"
)

func TestCommitAndRollback(t *testing.T) {
	s := NewStore()

	tx := s.Begin()
	tx.Apply("A", "1", Value{"n": 1})
	tx.Apply("A", "2", Value{"n": 2})
	tx.Rollback()
	if s.CountCommitted("A") != 0 {
		t.Fatalf("rollback left traces: %d committed", s.CountCommitted("A"))
	}

	tx = s.Begin()
	tx.Apply("A", "1", Value{"n": 1})
	tx.Apply("A", "2", Value{"n": 2})
	tx.Apply("A", "2", nil) // delete staged create
	tx.Commit()
	if s.CountCommitted("A") != 1 {
		t.Fatalf("commit: want 1 committed, got %d", s.CountCommitted("A"))
	}
	if v, ok := s.Get("A", "1"); !ok || v["n"] != 1 {
		t.Fatalf("committed value mismatch: %v %v", v, ok)
	}
}

func TestSnapshotSeesStagedWrites(t *testing.T) {
	s := NewStore()
	base := s.Begin()
	base.Apply("A", "committed", Value{"n": 0})
	base.Commit()

	tx := s.Begin()
	tx.Apply("A", "staged", Value{"n": 1})
	tx.Apply("A", "committed", nil)

	v := tx.Snapshot()
	if _, ok := v.Get("A", "staged"); !ok {
		t.Fatal("snapshot must see staged create")
	}
	if _, ok := v.Get("A", "committed"); ok {
		t.Fatal("snapshot must see staged delete")
	}
	if got := v.Count("A"); got != 1 {
		t.Fatalf("count: want 1, got %d", got)
	}
	// Committed state untouched until Commit.
	if _, ok := s.Get("A", "committed"); !ok {
		t.Fatal("committed store mutated before commit")
	}
}

// TestSnapshotConstructionIsO1 proves snapshot construction cost does not
// grow with the number of applied writes: Snapshot performs zero map
// copies (instrumented by copyOps) at every scale. See also
// BenchmarkSnapshotConstruction.
func TestSnapshotConstructionIsO1(t *testing.T) {
	for _, writes := range []int{0, 1, 1_000, 100_000} {
		s := NewStore()
		tx := s.Begin()
		for i := 0; i < writes; i++ {
			tx.Apply("A", fmt.Sprintf("id-%d", i), Value{"n": i})
		}
		_ = tx.Snapshot()
		if got := tx.CopyOps(); got != 0 {
			t.Fatalf("writes=%d: snapshot copied %d maps, want 0", writes, got)
		}
	}
}

// BenchmarkSnapshotConstruction shows constant ns/op as applied writes
// grow; run with: go test -bench Snapshot -benchtime 100x ./txn
func BenchmarkSnapshotConstruction(b *testing.B) {
	for _, writes := range []int{0, 1_000, 100_000} {
		b.Run(fmt.Sprintf("writes=%d", writes), func(b *testing.B) {
			s := NewStore()
			tx := s.Begin()
			for i := 0; i < writes; i++ {
				tx.Apply("A", fmt.Sprintf("id-%d", i), Value{"n": i})
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = tx.Snapshot()
			}
		})
	}
}
