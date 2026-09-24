package ring

import (
	"errors"
	"testing"
)

// The four failure modes must be distinguishable with errors.Is.
func TestErrorsAreDistinguishable(t *testing.T) {
	r := New()

	if _, err := r.Locate("anything"); !errors.Is(err, ErrEmptyRing) {
		t.Fatalf("Locate on empty ring: %v, want ErrEmptyRing", err)
	}
	if err := r.Add("n1", 0); !errors.Is(err, ErrInvalidVnodes) {
		t.Fatalf("Add vnodes=0: %v, want ErrInvalidVnodes", err)
	}
	if err := r.Add("n1", -3); !errors.Is(err, ErrInvalidVnodes) {
		t.Fatalf("Add vnodes=-3: %v, want ErrInvalidVnodes", err)
	}
	if err := r.Add("n1", 10); err != nil {
		t.Fatalf("Add n1: %v", err)
	}
	if err := r.Add("n1", 10); !errors.Is(err, ErrNodeExists) {
		t.Fatalf("duplicate Add: %v, want ErrNodeExists", err)
	}
	if err := r.Remove("ghost"); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("Remove unknown: %v, want ErrNodeNotFound", err)
	}

	all := []error{ErrEmptyRing, ErrInvalidVnodes, ErrNodeExists, ErrNodeNotFound}
	for i, a := range all {
		for j, b := range all {
			if i != j && errors.Is(a, b) {
				t.Fatalf("%v and %v must be distinguishable", a, b)
			}
		}
	}
}

// A duplicate Add must fail and leave the ring untouched: every key's
// ownership is compared before and after the rejected Add.
func TestDuplicateAddLeavesRingUnchanged(t *testing.T) {
	keys := genKeys(20000)
	r := buildRing(t, 5, 100)
	before := locateAll(t, r, keys)
	pointsBefore := r.Points()

	if err := r.Add(nodeID(2), 100); !errors.Is(err, ErrNodeExists) {
		t.Fatalf("duplicate Add: %v, want ErrNodeExists", err)
	}

	pointsAfter := r.Points()
	if len(pointsBefore) != len(pointsAfter) {
		t.Fatalf("point count changed: %d -> %d", len(pointsBefore), len(pointsAfter))
	}
	for i := range pointsBefore {
		if pointsBefore[i] != pointsAfter[i] {
			t.Fatalf("point %d changed by rejected Add", i)
		}
	}
	after := locateAll(t, r, keys)
	for i := range keys {
		if before[i] != after[i] {
			t.Fatalf("key %q moved %s -> %s after rejected Add",
				keys[i], before[i], after[i])
		}
	}
}

// Locate on an empty ring must return a non-empty error, never ("", nil).
func TestEmptyRingLocate(t *testing.T) {
	r := New()
	owner, err := r.Locate("k")
	if err == nil || owner != "" {
		t.Fatalf("Locate on empty ring = (%q, %v), want (\"\", ErrEmptyRing)", owner, err)
	}
}
