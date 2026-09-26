package api

import (
	"errors"
	"testing"
)

func TestSentinelErrorsDistinct(t *testing.T) {
	errs := []error{ErrEmptyRing, ErrNodeExists, ErrNodeNotFound, ErrInvalidVnodes}
	seen := map[string]bool{}
	for _, e := range errs {
		if e == nil || seen[e.Error()] {
			t.Fatalf("sentinel errors must be non-nil and distinct: %v", errs)
		}
		seen[e.Error()] = true
	}
}

func TestRejectedOpsNoTrace(t *testing.T) {
	// vnodes < 1 rejected; no ring created.
	if _, err := New(0); !errors.Is(err, ErrInvalidVnodes) {
		t.Fatalf("New(0) err=%v", err)
	}
	if _, err := New(-3); !errors.Is(err, ErrInvalidVnodes) {
		t.Fatalf("New(-3) err=%v", err)
	}
	r, err := New(2)
	if err != nil {
		t.Fatal(err)
	}
	// Get on empty ring.
	if _, err := r.Get(50); !errors.Is(err, ErrEmptyRing) || r.NodeCount() != 0 {
		t.Fatalf("empty Get err=%v count=%d", err, r.NodeCount())
	}
	// Duplicate add rejected, count stays 1.
	if err := r.AddNode(1); err != nil {
		t.Fatal(err)
	}
	if err := r.AddNode(1); !errors.Is(err, ErrNodeExists) || r.NodeCount() != 1 {
		t.Fatalf("dup add err=%v count=%d", err, r.NodeCount())
	}
	// Removing a non-member rejected, count stays 1.
	if err := r.RemoveNode(2); !errors.Is(err, ErrNodeNotFound) || r.NodeCount() != 1 {
		t.Fatalf("missing remove err=%v count=%d", err, r.NodeCount())
	}
	// State intact and ring still usable after all rejections.
	if err := r.AddNode(2); err != nil {
		t.Fatalf("ring unusable after rejections: %v", err)
	}
	if got, err := r.Get(50); err != nil || (got != 1 && got != 2) {
		t.Fatalf("Get after rejections: %d %v", got, err)
	}
	if r.NodeCount() != 2 {
		t.Fatalf("count=%d want 2", r.NodeCount())
	}
}

func TestAddRemoveRoundTrip(t *testing.T) {
	r, _ := New(2)
	for _, id := range []uint32{1, 2, 3} {
		if err := r.AddNode(id); err != nil {
			t.Fatal(err)
		}
	}
	if r.NodeCount() != 3 {
		t.Fatalf("count=%d", r.NodeCount())
	}
	if err := r.RemoveNode(2); err != nil {
		t.Fatal(err)
	}
	if err := r.RemoveNode(2); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("second remove err=%v", err)
	}
	// Re-adding a removed id is allowed.
	if err := r.AddNode(2); err != nil {
		t.Fatalf("re-add removed node: %v", err)
	}
}

func TestSelfCheck(t *testing.T) {
	r, err := New(2)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}
