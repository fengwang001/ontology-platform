package ontology

import (
	"errors"
	"testing"
)

func newBatchStore(t *testing.T) *Store {
	t.Helper()
	s := New(NormOptions{TrimSpace: true, CaseFold: true}, nameConstraint)
	if err := s.Insert("old", map[string]Value{"name": Str("Alice")}); err != nil {
		t.Fatalf("seed insert: %v", err)
	}
	return s
}

// Deferred checking: delete then re-insert of an equivalent key in
// one batch succeeds.
func TestBatchDeleteThenInsert(t *testing.T) {
	s := newBatchStore(t)
	err := s.Apply(
		DeleteOp("old"),
		InsertOp("new", map[string]Value{"name": Str("  ALICE  ")}),
	)
	if err != nil {
		t.Fatalf("delete-then-insert batch should succeed: %v", err)
	}
	if _, ok := s.Get("old"); ok {
		t.Error("old record should be gone")
	}
	rec, ok := s.Get("new")
	if !ok || rec.Props["name"].String() != "  ALICE  " {
		t.Errorf("new record missing or altered: %v", rec.Props["name"])
	}
	if err := s.SelfCheck(); err != nil {
		t.Errorf("SelfCheck: %v", err)
	}
}

// Two inserts of equivalent keys in one batch are rejected, and
// the error identifies both op indexes.
func TestBatchDoubleInsertRejected(t *testing.T) {
	s := newBatchStore(t)
	err := s.Apply(
		InsertOp("n1", map[string]Value{"name": Str("Bob")}),
		InsertOp("n2", map[string]Value{"name": Str("  bob  ")}),
	)
	be, ok := err.(*BatchError)
	if !ok {
		t.Fatalf("expected *BatchError, got %T: %v", err, err)
	}
	if be.Index != 1 || be.OtherIndex != 0 {
		t.Errorf("Index=%d OtherIndex=%d, want 1 and 0", be.Index, be.OtherIndex)
	}
	var ce *ConflictError
	if !errors.As(err, &ce) {
		t.Fatalf("BatchError should unwrap to *ConflictError: %v", err)
	}
	if ce.ExistingID != "n1" {
		t.Errorf("conflict should point to n1, got %q", ce.ExistingID)
	}
	if ce.Key != "bob" {
		t.Errorf("Key = %q, want %q", ce.Key, "bob")
	}
}

// A conflict against a pre-existing record reports OtherIndex=-1.
func TestBatchConflictWithExisting(t *testing.T) {
	s := newBatchStore(t)
	err := s.Apply(InsertOp("n1", map[string]Value{"name": Str("alice")}))
	be, ok := err.(*BatchError)
	if !ok {
		t.Fatalf("expected *BatchError, got %v", err)
	}
	if be.Index != 0 || be.OtherIndex != -1 {
		t.Errorf("Index=%d OtherIndex=%d, want 0 and -1", be.Index, be.OtherIndex)
	}
	if be.Err.ExistingID != "old" {
		t.Errorf("ExistingID = %q, want %q", be.Err.ExistingID, "old")
	}
}

// Any conflict aborts the whole batch: earlier ops are rolled
// back and existing records are untouched.
func TestBatchAtomicity(t *testing.T) {
	s := newBatchStore(t)
	err := s.Apply(
		InsertOp("n1", map[string]Value{"name": Str("Bob")}),
		DeleteOp("old"),
		InsertOp("n2", map[string]Value{"name": Str("BOB")}),
	)
	if err == nil {
		t.Fatal("batch should be rejected")
	}
	if _, ok := s.Get("n1"); ok {
		t.Error("n1 from the aborted batch must not exist")
	}
	rec, ok := s.Get("old")
	if !ok || rec.Props["name"].String() != "Alice" {
		t.Error("pre-existing record must be untouched")
	}
	if err := s.SelfCheck(); err != nil {
		t.Errorf("SelfCheck after aborted batch: %v", err)
	}
}
