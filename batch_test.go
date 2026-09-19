package ontology

import (
	"errors"
	"testing"
)

func TestBatchAtomicRollback(t *testing.T) {
	s := newCardinalityStore(t)
	mustLink(t, s, "spouse", "p1", "p2")
	b := &Batch{}
	b.CreateLink("employs", "c1", "p3") // ok
	b.CreateLink("spouse", "p3", "p2")  // ONE_TO_ONE violation
	err := s.ApplyBatch(b)
	var be *BatchError
	if !errors.As(err, &be) {
		t.Fatalf("expected *BatchError, got %v", err)
	}
	if be.Index != 1 {
		t.Fatalf("BatchError.Index = %d, want 1", be.Index)
	}
	if !IsViolation(err, ViolationOneToOne) {
		t.Fatalf("wrapped violation lost: %v", err)
	}
	// Nothing applied: employs c1->p3 must not exist.
	if s.HasLink("employs", "c1", "p3") {
		t.Fatal("batch partially applied")
	}
	if err := s.CheckInvariant(); err != nil {
		t.Fatalf("CheckInvariant: %v", err)
	}
}

func TestBatchErrorCarriesContext(t *testing.T) {
	s := newCardinalityStore(t)
	b := &Batch{}
	b.CreateLink("employs", "c1", "ghost")
	err := s.ApplyBatch(b)
	var be *BatchError
	if !errors.As(err, &be) {
		t.Fatalf("expected *BatchError, got %v", err)
	}
	if be.Op.LinkType != "employs" || be.Op.Source != "c1" || be.Op.Target != "ghost" {
		t.Fatalf("op context missing: %+v", be.Op)
	}
	if !IsViolation(err, ViolationEndpointNotFound) {
		t.Fatalf("want ViolationEndpointNotFound, got %v", err)
	}
}

// TestBatchDuplicateCreate: creating the same link twice in one batch is a
// no-op, not a duplicate error.
func TestBatchDuplicateCreate(t *testing.T) {
	s := newGraphStore(t)
	mustObjects(t, s, "Person", "p1", "p2")
	b := &Batch{}
	b.CreateLink("knows", "p1", "p2")
	b.CreateLink("knows", "p1", "p2")
	if err := s.ApplyBatch(b); err != nil {
		t.Fatalf("ApplyBatch: %v", err)
	}
	if got := s.LinksFrom("knows", "p1"); !equalStrings(got, []string{"p2"}) {
		t.Fatalf("LinksFrom = %v", got)
	}
}

// TestBatchCreateThenDelete: create-then-delete of the same link leaves it
// absent.
func TestBatchCreateThenDelete(t *testing.T) {
	s := newGraphStore(t)
	mustObjects(t, s, "Person", "p1", "p2")
	b := &Batch{}
	b.CreateLink("knows", "p1", "p2")
	b.DeleteLink("knows", "p1", "p2")
	if err := s.ApplyBatch(b); err != nil {
		t.Fatalf("ApplyBatch: %v", err)
	}
	if s.HasLink("knows", "p1", "p2") {
		t.Fatal("link should be absent after create+delete in one batch")
	}
	if err := s.CheckInvariant(); err != nil {
		t.Fatalf("CheckInvariant: %v", err)
	}
}

// TestBatchCreateThenCascadeDelete: a link created earlier in the batch is
// still swept by a later cascade delete in the same batch.
func TestBatchCreateThenCascadeDelete(t *testing.T) {
	s := newCascadeStore(t)
	mustObjects(t, s, "A", "a1")
	mustObjects(t, s, "B", "b1")
	b := &Batch{}
	b.CreateLink("ab", "a1", "b1") // CASCADE link created in-batch
	b.DeleteObject("a1")           // must cascade to b1
	if err := s.ApplyBatch(b); err != nil {
		t.Fatalf("ApplyBatch: %v", err)
	}
	if s.HasObject("a1") || s.HasObject("b1") {
		t.Fatal("cascade did not settle in-batch link")
	}
	if s.HasLink("ab", "a1", "b1") {
		t.Fatal("in-batch link survived cascade")
	}
	if err := s.CheckInvariant(); err != nil {
		t.Fatalf("CheckInvariant: %v", err)
	}
}

// TestBatchDeleteThenRecreate: delete-then-create of the same link leaves
// it present.
func TestBatchDeleteThenRecreate(t *testing.T) {
	s := newGraphStore(t)
	mustObjects(t, s, "Person", "p1", "p2")
	mustLink(t, s, "knows", "p1", "p2")
	b := &Batch{}
	b.DeleteLink("knows", "p1", "p2")
	b.CreateLink("knows", "p1", "p2")
	if err := s.ApplyBatch(b); err != nil {
		t.Fatalf("ApplyBatch: %v", err)
	}
	if !s.HasLink("knows", "p1", "p2") {
		t.Fatal("link should be present after delete+create in one batch")
	}
}

// TestBatchMixedOps: a mixed batch applies fully when valid.
func TestBatchMixedOps(t *testing.T) {
	s := newGraphStore(t)
	mustObjects(t, s, "Person", "p1", "p2", "p3")
	mustLink(t, s, "knows", "p1", "p2")
	b := &Batch{}
	b.DeleteLink("knows", "p1", "p2")
	b.CreateLink("knows", "p2", "p3")
	b.DeleteObject("p1")
	if err := s.ApplyBatch(b); err != nil {
		t.Fatalf("ApplyBatch: %v", err)
	}
	if s.HasObject("p1") || s.HasLink("knows", "p1", "p2") {
		t.Fatal("deletes not applied")
	}
	if !s.HasLink("knows", "p2", "p3") {
		t.Fatal("create not applied")
	}
	if err := s.CheckInvariant(); err != nil {
		t.Fatalf("CheckInvariant: %v", err)
	}
}
