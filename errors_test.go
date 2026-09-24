package ontology

import (
	"errors"
	"testing"
)

func TestLocateOnEmptyRing(t *testing.T) {
	r := New()
	node, err := r.Locate("anything")
	if !errors.Is(err, ErrEmptyRing) {
		t.Fatalf("err = %v, want ErrEmptyRing", err)
	}
	if node != "" {
		t.Fatalf("node = %q, want empty string", node)
	}
}

func TestAddRejectsNonPositiveVnodes(t *testing.T) {
	r := New()
	for _, v := range []int{0, -1, -100} {
		if err := r.Add("n", v); !errors.Is(err, ErrInvalidVnodes) {
			t.Fatalf("vnodes=%d: err = %v, want ErrInvalidVnodes", v, err)
		}
	}
	if r.Len() != 0 {
		t.Fatalf("ring should stay empty, has %d nodes", r.Len())
	}
	if _, err := r.Locate("k"); !errors.Is(err, ErrEmptyRing) {
		t.Fatalf("err = %v, want ErrEmptyRing", err)
	}
}

func TestDuplicateAddErrorsAndLeavesRingUnchanged(t *testing.T) {
	keys := generateKeys(5000)
	r := buildRing(t, nodeIDs(6), 100)
	before := locateAll(r, keys)

	err := r.Add(nodeName(2), 100)
	if !errors.Is(err, ErrNodeExists) {
		t.Fatalf("err = %v, want ErrNodeExists", err)
	}

	after := locateAll(r, keys)
	for i := range keys {
		if after[i] != before[i] {
			t.Fatalf("key %d owner changed %q -> %q after duplicate add",
				i, before[i], after[i])
		}
	}
	if r.Len() != 6 {
		t.Fatalf("Len = %d, want 6", r.Len())
	}
}

func TestRemoveMissingNode(t *testing.T) {
	r := buildRing(t, nodeIDs(3), 10)
	err := r.Remove("ghost")
	if !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("err = %v, want ErrNodeNotFound", err)
	}
	if r.Len() != 3 {
		t.Fatalf("Len = %d, want 3", r.Len())
}
}

func TestErrorValuesAreDistinct(t *testing.T) {
	errs := []error{ErrEmptyRing, ErrInvalidVnodes, ErrNodeExists, ErrNodeNotFound}
	for i := range errs {
		for j := i + 1; j < len(errs); j++ {
			if errors.Is(errs[i], errs[j]) {
				t.Fatalf("errors %v and %v are not distinguishable", errs[i], errs[j])
			}
		}
	}
}
