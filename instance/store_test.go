package instance

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func personSpec() TypeSpec {
	return TypeSpec{
		Name: "person",
		Properties: []PropertySpec{
			{Name: "name", Kind: KindString, Required: true},
			{Name: "age", Kind: KindInt},
		},
	}
}

func TestCreateGetUpdateBasic(t *testing.T) {
	s := NewStore(personSpec())

	created, err := s.Create("person", "p1", map[string]any{"name": "Ada", "age": 36})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.Version != 1 {
		t.Fatalf("initial version = %d, want 1", created.Version)
	}
	if created.LastWriteTime.IsZero() {
		t.Fatal("LastWriteTime not set")
	}

	got, err := s.Get("person", "p1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Version != 1 || got.Properties["name"] != "Ada" || got.Properties["age"] != 36 {
		t.Fatalf("unexpected snapshot: %+v", got)
	}

	// Duplicate create on a live key is rejected.
	if _, err := s.Create("person", "p1", map[string]any{"name": "X"}); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("dup create err = %v, want already-exists", err)
	}

	// Stale update is a conflict carrying both versions.
	_, err = s.Update("person", "p1", 5, map[string]any{"name": "Ada2"})
	var ce *Error
	if !errors.As(err, &ce) || ce.Kind != KindConflict {
		t.Fatalf("stale update err = %v, want *Error KindConflict", err)
	}
	if ce.Expected != 5 || ce.Actual != 1 {
		t.Fatalf("conflict versions = expected %d actual %d, want 5/1", ce.Expected, ce.Actual)
	}

	// Updating a never-existing key is NotFound, not a conflict.
	if _, err := s.Update("person", "ghost", 1, map[string]any{"name": "g"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update unknown err = %v, want not-found", err)
	}

	// Happy-path update advances version and timestamp.
	updated, err := s.Update("person", "p1", 1, map[string]any{"name": "Ada Lovelace", "age": 37})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Version != 2 {
		t.Fatalf("version after update = %d, want 2", updated.Version)
	}
	if updated.Properties["name"] != "Ada Lovelace" {
		t.Fatalf("properties not replaced: %+v", updated.Properties)
	}

	got2, _ := s.Get("person", "p1")
	if got2.Version != 2 {
		t.Fatalf("get version = %d, want 2", got2.Version)
	}
}

func TestLogicalDeleteAndResurrection(t *testing.T) {
	s := NewStore(personSpec())

	if _, err := s.Create("person", "p1", map[string]any{"name": "Ada"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Update("person", "p1", 1, map[string]any{"name": "Ada2"}); err != nil {
		t.Fatal(err)
	}

	// Wrong-version delete is a conflict, instance stays live.
	err := s.Delete("person", "p1", 9)
	if !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale delete err = %v, want conflict", err)
	}
	var ce *Error
	if errors.As(err, &ce); ce.Actual != 2 || ce.Expected != 9 {
		t.Fatalf("delete conflict versions = %d/%d", ce.Expected, ce.Actual)
	}
	if _, err := s.Get("person", "p1"); err != nil {
		t.Fatalf("instance should still be live: %v", err)
	}

	if err := s.Delete("person", "p1", 2); err != nil {
		t.Fatalf("delete: %v", err)
	}

	// Deleted key: invisible to Get, reported as deleted (distinct
	// from never-existed and distinct from a conflict).
	if _, err := s.Get("person", "p1"); !errors.Is(err, ErrDeleted) {
		t.Fatalf("get deleted err = %v, want ErrDeleted", err)
	}
	if _, err := s.Get("person", "never-existed"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get unknown err = %v, want ErrNotFound", err)
	}

	// Updating the deleted instance fails as deleted; updating a
	// never-seen key fails as not-found. Different categories.
	if _, err := s.Update("person", "p1", 2, map[string]any{"name": "x"}); !errors.Is(err, ErrDeleted) {
		t.Fatalf("update deleted err = %v, want ErrDeleted", err)
	}
	if _, err := s.Update("person", "ghost", 2, map[string]any{"name": "x"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update ghost err = %v, want ErrNotFound", err)
	}

	// Deleting again / deleting an unknown key.
	if err := s.Delete("person", "p1", 2); !errors.Is(err, ErrDeleted) {
		t.Fatalf("re-delete err = %v, want ErrDeleted", err)
	}
	if err := s.Delete("person", "ghost", 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete ghost err = %v, want ErrNotFound", err)
	}

	// Resurrection: version continues from the pre-deletion value,
	// never restarts at 1.
	r, err := s.Create("person", "p1", map[string]any{"name": "Reborn"})
	if err != nil {
		t.Fatalf("resurrect: %v", err)
	}
	if r.Version != 3 {
		t.Fatalf("resurrected version = %d, want 3", r.Version)
	}
	got, err := s.Get("person", "p1")
	if err != nil {
		t.Fatalf("get resurrected: %v", err)
	}
	if got.Version != 3 || got.Properties["name"] != "Reborn" {
		t.Fatalf("unexpected resurrected instance: %+v", got)
	}

	// Resurrect again after another delete: still monotonic, no reset.
	if err := s.Delete("person", "p1", 3); err != nil {
		t.Fatal(err)
	}
	r2, err := s.Create("person", "p1", map[string]any{"name": "Reborn2"})
	if err != nil {
		t.Fatal(err)
	}
	if r2.Version != 5 {
		t.Fatalf("second resurrection version = %d, want 5", r2.Version)
	}
}

func TestGetSnapshotIsolation(t *testing.T) {
	s := NewStore()

	input := map[string]any{
		"name": "Ada",
		"tags": []any{"math", "engine"},
		"nested": map[string]any{
			"city": "London",
		},
	}
	if _, err := s.Create("doc", "d1", input); err != nil {
		t.Fatal(err)
	}

	// Mutating the caller's input after Create must not reach storage.
	input["name"] = "MUTATED"
	input["tags"].([]any)[0] = "MUTATED"
	input["nested"].(map[string]any)["city"] = "MUTATED"

	snap1, err := s.Get("doc", "d1")
	if err != nil {
		t.Fatal(err)
	}
	if snap1.Properties["name"] != "Ada" {
		t.Fatalf("store aliased caller input: %+v", snap1.Properties)
	}

	// Mutating the returned snapshot must not reach storage (outward
	// isolation).
	snap1.Properties["name"] = "HACKED"
	snap1.Properties["tags"].([]any)[0] = "HACKED"
	snap1.Properties["nested"].(map[string]any)["city"] = "HACKED"
	snap1.Version = 999

	snap2, _ := s.Get("doc", "d1")
	if snap2.Properties["name"] != "Ada" ||
		snap2.Properties["tags"].([]any)[0] != "math" ||
		snap2.Properties["nested"].(map[string]any)["city"] != "London" ||
		snap2.Version != 1 {
		t.Fatalf("store visible after snapshot mutation: %+v", snap2.Properties)
	}

	// A later write must not change the earlier snapshot (inward
	// isolation), including slice backing arrays.
	oldTags := snap2.Properties["tags"].([]any)
	if _, err := s.Update("doc", "d1", 1, map[string]any{
		"name": "Ada2",
		"tags": []any{"new"},
	}); err != nil {
		t.Fatal(err)
	}
	if oldTags[0] != "math" || oldTags[1] != "engine" {
		t.Fatalf("old snapshot slice changed by later write: %v", oldTags)
	}
	if snap2.Properties["name"] != "Ada" {
		t.Fatal("old snapshot map changed by later write")
	}
}

func TestConstraints(t *testing.T) {
	s := NewStore(personSpec())

	cases := []struct {
		name  string
		props map[string]any
	}{
		{"unknown property", map[string]any{"name": "a", "bogus": 1}},
		{"wrong type", map[string]any{"name": 123}},
		{"missing required", map[string]any{"age": 3}},
	}
	for _, tc := range cases {
		if _, err := s.Create("person", "k", tc.props); !errors.Is(err, ErrConstraint) {
			t.Fatalf("%s: err = %v, want constraint", tc.name, err)
		}
	}

	// Unregistered types accept arbitrary properties.
	if _, err := s.Create("freeform", "k", map[string]any{"anything": []int{1, 2}}); err != nil {
		t.Fatalf("unregistered type: %v", err)
	}

	// Constraint failure on update leaves the instance untouched.
	if _, err := s.Create("person", "p", map[string]any{"name": "ok"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Update("person", "p", 1, map[string]any{"age": "not an int"}); !errors.Is(err, ErrConstraint) {
		t.Fatalf("bad update: %v", err)
	}
	got, _ := s.Get("person", "p")
	if got.Version != 1 || got.Properties["name"] != "ok" {
		t.Fatalf("instance mutated after rejected update: %+v", got)
	}
}
