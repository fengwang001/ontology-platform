package ontology

import "testing"

func batchStore() *Store {
	return NewStore(
		Normalize{TrimSpace: true, CaseFold: true},
		Constraint{Name: "uq_name", Cols: []string{"name"}},
	)
}

// TestBatchDeleteThenInsert: deferred checking lets a batch delete a
// record and insert a new one with the same normalized key.
func TestBatchDeleteThenInsert(t *testing.T) {
	s := batchStore()
	mustNoErr(t, s.Insert("old", rec("name", "Alice")))
	err := s.ApplyBatch([]Op{
		DeleteOp("old"),
		InsertOp("new", rec("name", "  ALICE ")),
	})
	mustNoErr(t, err)
	if _, ok := s.Get("old"); ok {
		t.Fatal("old record should be gone")
	}
	got, ok := s.Get("new")
	if !ok || got["name"].Str != "  ALICE " {
		t.Fatalf("new record missing or altered: %v %v", got, ok)
	}
	mustNoErr(t, s.CheckIndex())
}

// TestBatchDoubleInsert: two inserts with equal normalized keys in one
// batch are rejected, naming both op positions, and nothing is applied.
func TestBatchDoubleInsert(t *testing.T) {
	s := batchStore()
	err := s.ApplyBatch([]Op{
		InsertOp("a", rec("name", "Alice")),
		InsertOp("b", rec("name", "ALICE")),
	})
	be, ok := err.(*BatchError)
	if !ok {
		t.Fatalf("expected *BatchError, got %v (%T)", err, err)
	}
	if be.OpIndex != 1 || be.OtherOpIndex != 0 {
		t.Fatalf("positions = (%d, %d), want (1, 0)", be.OpIndex, be.OtherOpIndex)
	}
	if be.Conflict.Constraint != "uq_name" {
		t.Fatalf("constraint = %q, want uq_name", be.Conflict.Constraint)
	}
	if s.Len() != 0 {
		t.Fatalf("aborted batch must not apply, Len=%d", s.Len())
	}
	mustNoErr(t, s.CheckIndex())
}

// TestBatchConflictWithExisting: a batch op clashing with a stored
// record reports OtherOpIndex=-1 and leaves the store untouched.
func TestBatchConflictWithExisting(t *testing.T) {
	s := batchStore()
	mustNoErr(t, s.Insert("keep", rec("name", "Alice")))
	err := s.ApplyBatch([]Op{
		InsertOp("x", rec("name", "bob")),
		InsertOp("y", rec("name", " alice ")),
	})
	be, ok := err.(*BatchError)
	if !ok {
		t.Fatalf("expected *BatchError, got %v (%T)", err, err)
	}
	if be.OpIndex != 1 || be.OtherOpIndex != -1 {
		t.Fatalf("positions = (%d, %d), want (1, -1)", be.OpIndex, be.OtherOpIndex)
	}
	if be.Conflict.ExistingPK != "keep" {
		t.Fatalf("ExistingPK=%q, want keep", be.Conflict.ExistingPK)
	}
	if s.Len() != 1 {
		t.Fatalf("store must be untouched, Len=%d", s.Len())
	}
	if _, ok := s.Get("x"); ok {
		t.Fatal("earlier batch op must be rolled back too")
	}
}
