package instance

import (
	"reflect"
	"testing"
)

func TestGetReturnsIndependentSnapshot(t *testing.T) {
	store, _ := testStore()
	in := map[string]any{
		"tags": []any{"a", "b"},
		"nested": map[string]any{
			"nums": []any{1, 2},
		},
	}
	inst, err := store.Create("Doc", "d1", in)
	if err != nil {
		t.Fatal(err)
	}

	// Mutate the caller's input after Create; storage must not change.
	in["tags"].([]any)[0] = "MUTATED"
	in["new"] = "leaked"

	got, _ := store.Get("Doc", "d1")
	if got.Attributes["new"] != nil {
		t.Fatal("store retained caller's map: new key leaked in")
	}
	if got.Attributes["tags"].([]any)[0] != "a" {
		t.Fatal("store retained caller's slice: mutation visible")
	}

	// Mutate Create's returned snapshot; storage must not change.
	inst.Attributes["tags"].([]any)[1] = "X"
	again, _ := store.Get("Doc", "d1")
	if again.Attributes["tags"].([]any)[1] != "b" {
		t.Fatal("mutation of returned Create snapshot reached storage")
	}

	// Mutate a Get snapshot; a later Get must be unaffected.
	got.Attributes["nested"].(map[string]any)["nums"].([]any)[0] = 99
	got.Attributes["tags"] = append(got.Attributes["tags"].([]any), "c")
	fresh, _ := store.Get("Doc", "d1")
	if !reflect.DeepEqual(fresh.Attributes, again.Attributes) {
		t.Fatalf("snapshot mutation leaked into storage:\n got %#v\nwant %#v",
			fresh.Attributes, again.Attributes)
	}
}

func TestLaterWriteDoesNotChangeOldSnapshot(t *testing.T) {
	store, _ := testStore()
	inst, _ := store.Create("Cfg", "c", map[string]any{"v": "one"})

	_, err := store.Update("Cfg", "c", inst.Version, map[string]any{"v": "two"})
	if err != nil {
		t.Fatal(err)
	}
	if inst.Attributes["v"] != "one" {
		t.Fatalf("old snapshot changed: %v", inst.Attributes["v"])
	}

	fresh, _ := store.Get("Cfg", "c")
	if fresh.Attributes["v"] != "two" || fresh.Version != 2 {
		t.Fatalf("fresh snapshot wrong: %+v", fresh)
	}
}

func TestConstraintViolationRejectsBadValues(t *testing.T) {
	store, _ := testStore()

	bad := []map[string]any{
		{"f": struct{ X int }{1}},
		{"f": make(chan int)},
		{"f": map[int]string{1: "non-string key"}},
		{"f": []any{func() {}}},
	}
	for i, attrs := range bad {
		if _, err := store.Create("Bad", "k", attrs); err == nil {
			t.Fatalf("case %d: expected constraint error", i)
		} else if we, ok := AsWriteError(err); !ok || we.Reason != ReasonConstraint {
			t.Fatalf("case %d: err = %v, want constraint", i, err)
		}
	}

	if _, err := store.Get("Bad", "k"); err == nil {
		t.Fatal("rejected Create must leave nothing behind")
	}
}

func TestCyclicAttributeRejected(t *testing.T) {
	store, _ := testStore()
	cyclic := map[string]any{}
	cyclic["self"] = cyclic

	_, err := store.Create("Loop", "l", cyclic)
	if we, ok := AsWriteError(err); !ok || we.Reason != ReasonConstraint {
		t.Fatalf("cyclic value err = %v, want constraint", err)
	}
}
