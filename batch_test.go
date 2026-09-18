package instance

import "testing"

func TestBatchWriteMixedSuccess(t *testing.T) {
	store, _ := testStore()
	toDelete, _ := store.Create("Person", "gone", nil)
	toUpdate, _ := store.Create("Person", "kept", nil)

	results, err := store.BatchWrite([]Op{
		{Kind: OpCreate, ObjectType: "Person", Key: "new", Attributes: map[string]any{"a": 1}},
		{Kind: OpUpdate, ObjectType: "Person", Key: "kept", ExpectedVersion: toUpdate.Version,
			Attributes: map[string]any{"a": 2}},
		{Kind: OpDelete, ObjectType: "Person", Key: "gone", ExpectedVersion: toDelete.Version},
	})
	if err != nil {
		t.Fatalf("BatchWrite: %v", err)
	}
	if results[0] == nil || results[0].Version != 1 {
		t.Fatalf("create result = %+v", results[0])
	}
	if results[1] == nil || results[1].Version != 2 {
		t.Fatalf("update result = %+v", results[1])
	}
	if results[2] != nil {
		t.Fatalf("delete result = %+v, want nil", results[2])
	}

	if _, err := store.Get("Person", "gone"); err == nil {
		t.Fatal("existing should be deleted within the batch")
	}
	if got, err := store.Get("Person", "kept"); err != nil || got.Version != 2 {
		t.Fatalf("kept should be updated to v2: %v", err)
	}
	if got, err := store.Get("Person", "new"); err != nil || got.Version != 1 {
		t.Fatalf("new should exist at v1: %v", err)
	}
}

func TestBatchRejectsDuplicateKeyWithoutMerging(t *testing.T) {
	store, _ := testStore()
	_, _ = store.Create("Person", "p", nil)

	_, err := store.BatchWrite([]Op{
		{Kind: OpUpdate, ObjectType: "Person", Key: "p", ExpectedVersion: 1},
		{Kind: OpDelete, ObjectType: "Person", Key: "p", ExpectedVersion: 1},
	})
	be, ok := AsBatchError(err)
	if !ok {
		t.Fatalf("want BatchError, got %T %v", err, err)
	}
	if be.Reason != ReasonDuplicateInBatch || be.Index != 1 || be.Key != "p" {
		t.Fatalf("error = %+v", be)
	}

	got, _ := store.Get("Person", "p")
	if got.Version != 1 {
		t.Fatalf("duplicate batch must apply nothing; version = %d", got.Version)
	}
}

func TestBatchCreateDuplicateKeyAlsoRejected(t *testing.T) {
	store, _ := testStore()
	_, err := store.BatchWrite([]Op{
		{Kind: OpCreate, ObjectType: "T", Key: "k"},
		{Kind: OpCreate, ObjectType: "T", Key: "k"},
	})
	if be, _ := AsBatchError(err); be == nil || be.Index != 1 ||
		be.Reason != ReasonDuplicateInBatch {
		t.Fatalf("err = %v", err)
	}
}

func TestBatchRollsBackOnLateFailure(t *testing.T) {
	store, _ := testStore()
	_, _ = store.Create("Person", "stable", map[string]any{"v": "orig"}) // v1
	other, _ := store.Create("Person", "other", map[string]any{"v": "untouched"})

	// Third entry uses a stale expected version; the first two must not apply.
	_, err := store.BatchWrite([]Op{
		{Kind: OpCreate, ObjectType: "Person", Key: "wouldBeNew", Attributes: map[string]any{}},
		{Kind: OpUpdate, ObjectType: "Person", Key: "other", ExpectedVersion: other.Version,
			Attributes: map[string]any{"v": "changed"}},
		{Kind: OpDelete, ObjectType: "Person", Key: "stable", ExpectedVersion: 99},
	})
	be, ok := AsBatchError(err)
	if !ok || be.Index != 2 || be.Reason != ReasonVersionConflict {
		t.Fatalf("err = %v, want index 2 conflict", err)
	}
	if be.Expected != 99 || be.Actual != 1 {
		t.Fatalf("versions: expected=%d actual=%d", be.Expected, be.Actual)
	}

	got, gErr := store.Get("Person", "stable")
	if gErr != nil || got.Version != 1 || got.Attributes["v"] != "orig" {
		t.Fatalf("rollback failed for stable: %+v %v", got, gErr)
	}
	if got, err := store.Get("Person", "other"); err != nil ||
		got.Version != 1 || got.Attributes["v"] != "untouched" {
		t.Fatalf("rollback failed for other: %+v %v", got, err)
	}
	if _, err := store.Get("Person", "wouldBeNew"); err == nil {
		t.Fatal("created entry from failed batch must be rolled back")
	}
}

func TestBatchRollbackOnConstraintAndDeleted(t *testing.T) {
	store, _ := testStore()
	_, _ = store.Create("Person", "alive", nil)
	dead, _ := store.Create("Person", "dead", nil)
	_ = store.Delete("Person", "dead", dead.Version)

	// Constraint failure anywhere rejects the whole batch.
	_, err := store.BatchWrite([]Op{
		{Kind: OpCreate, ObjectType: "Person", Key: "x", Attributes: map[string]any{"ok": 1}},
		{Kind: OpCreate, ObjectType: "Person", Key: "bad",
			Attributes: map[string]any{"f": make(chan int)}},
	})
	if be, _ := AsBatchError(err); be == nil || be.Index != 1 || be.Reason != ReasonConstraint {
		t.Fatalf("constraint err = %v", err)
	}
	if _, err := store.Get("Person", "x"); err == nil {
		t.Fatal("constraint batch must roll back the valid create too")
	}

	// Updating a deleted instance is Deleted, not conflict and not NotFound.
	_, err = store.BatchWrite([]Op{
		{Kind: OpUpdate, ObjectType: "Person", Key: "dead", ExpectedVersion: 5},
	})
	if be, _ := AsBatchError(err); be == nil || be.Reason != ReasonDeleted || be.Actual != 2 {
		t.Fatalf("deleted in batch err = %v", err)
	}

	_, err = store.BatchWrite([]Op{
		{Kind: OpDelete, ObjectType: "Person", Key: "ghost", ExpectedVersion: 1},
	})
	if be, _ := AsBatchError(err); be == nil || be.Reason != ReasonNotFound {
		t.Fatalf("not found in batch err = %v", err)
	}
}
