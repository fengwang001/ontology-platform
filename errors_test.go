package ontology

import (
	"errors"
	"testing"
)

func TestLocateOnEmptyRing(t *testing.T) {
	r := New()
	got, err := r.Locate("anything")
	if !errors.Is(err, ErrEmptyRing) {
		t.Fatalf("Locate on empty ring: err = %v, want ErrEmptyRing", err)
	}
	if got != "" {
		t.Fatalf("Locate on empty ring: got %q, want empty string", got)
	}
}

func TestAddWithNonPositiveVnodes(t *testing.T) {
	r := New()
	for _, vn := range []int{0, -1, -100} {
		if err := r.Add("node-x", vn); !errors.Is(err, ErrInvalidVnodes) {
			t.Fatalf("Add(vnodes=%d): err = %v, want ErrInvalidVnodes", vn, err)
		}
	}
	if r.Len() != 0 {
		t.Fatalf("failed Adds changed the ring: Len = %d, want 0", r.Len())
	}
}

// A duplicate Add must fail with ErrNodeExists and leave every key's
// ownership exactly as it was.
func TestDuplicateAddFailsAndKeepsRing(t *testing.T) {
	ids := nodeIDs(5)
	r := buildRing(t, ids, 100)
	keys := Keys(10000)
	before := owners(t, r, keys)

	err := r.Add(ids[2], 100)
	if !errors.Is(err, ErrNodeExists) {
		t.Fatalf("duplicate Add: err = %v, want ErrNodeExists", err)
	}
	after := owners(t, r, keys)
	for i := range keys {
		if after[i] != before[i] {
			t.Fatalf("duplicate Add moved key %q: %q -> %q", keys[i], before[i], after[i])
		}
	}
}

func TestRemoveMissingNode(t *testing.T) {
	r := buildRing(t, nodeIDs(3), 10)
	if err := r.Remove("ghost"); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("Remove(ghost): err = %v, want ErrNodeNotFound", err)
	}
	if r.Len() != 3 {
		t.Fatalf("failed Remove changed the ring: Len = %d, want 3", r.Len())
	}
}

// The four sentinel errors must be mutually distinguishable via errors.Is.
func TestSentinelErrorsAreDistinct(t *testing.T) {
	all := []error{ErrEmptyRing, ErrInvalidVnodes, ErrNodeExists, ErrNodeNotFound}
	for i, a := range all {
		for j, b := range all {
			if i != j && errors.Is(a, b) {
				t.Fatalf("errors %d and %d are indistinguishable: %v vs %v", i, j, a, b)
			}
		}
	}
}
