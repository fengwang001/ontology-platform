package instance

import "testing"

func TestBatchResurrectContinuesVersion(t *testing.T) {
	store, _ := testStore()
	inst, _ := store.Create("Person", "p", nil)   // v1
	_ = store.Delete("Person", "p", inst.Version) // v2 deleted

	results, err := store.BatchWrite([]Op{
		{Kind: OpCreate, ObjectType: "Person", Key: "p", Attributes: map[string]any{"back": true}},
	})
	if err != nil {
		t.Fatalf("batch resurrect: %v", err)
	}
	if results[0].Version != 3 {
		t.Fatalf("resurrected version = %d, want 3", results[0].Version)
	}

	got, _ := store.Get("Person", "p")
	if got.Version != 3 || got.Deleted {
		t.Fatalf("resurrected state wrong: %+v", got)
	}
}

func TestBatchEmptyIsNoop(t *testing.T) {
	store, _ := testStore()
	results, err := store.BatchWrite(nil)
	if err != nil {
		t.Fatalf("empty batch: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("empty batch results = %v", results)
	}
}
