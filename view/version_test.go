package view_test

import (
	"errors"
	"reflect"
	"testing"

	"ontology/change"
	"ontology/view"
)

func TestDuplicateVersionIdempotent(t *testing.T) {
	v := newTestView(t)
	c := ins(1, "k", "g", 5)
	if err := v.Submit(c); err != nil {
		t.Fatal(err)
	}
	before := v.Groups()
	beforeVer := v.MaxVersion()

	if err := v.Submit(c); err != nil {
		t.Fatalf("exact redelivery must be idempotent, got %v", err)
	}
	after := v.Groups()
	if !reflect.DeepEqual(before, after) {
		t.Fatal("view changed on duplicate version")
	}
	if v.MaxVersion() != beforeVer {
		t.Fatal("version watermark changed on duplicate")
	}
	g, _ := v.Lookup("g")
	if g.Count != 1 || g.Sum != 5 {
		t.Fatalf("duplicate altered aggregates: %+v", g)
	}
	if v.Stats().Reapplied != 1 {
		t.Fatalf("reapplied=%d want 1", v.Stats().Reapplied)
	}
}

func TestBackwardVersionRejected(t *testing.T) {
	v := newTestView(t)
	if err := v.Submit(ins(5, "k", "g", 1)); err != nil {
		t.Fatal(err)
	}
	if err := v.Submit(ins(4, "k2", "g", 2)); !errors.Is(err, view.ErrVersionBackward) {
		t.Fatalf("want ErrVersionBackward, got %v", err)
	}
	if v.MaxVersion() != 5 {
		t.Fatal("watermark must not move backwards")
	}
	g, _ := v.Lookup("g")
	if g.Count != 1 {
		t.Fatal("view polluted by backward change")
	}
	if v.Stats().Rejected != 1 {
		t.Fatalf("rejected=%d want 1", v.Stats().Rejected)
	}
}

func TestVersionReuseConflict(t *testing.T) {
	v := newTestView(t)
	if err := v.Submit(ins(3, "k", "g", 1)); err != nil {
		t.Fatal(err)
	}
	other := ins(3, "other", "g", 2)
	if err := v.Submit(other); !errors.Is(err, view.ErrVersionConflict) {
		t.Fatalf("want conflict, got %v", err)
	}
}

func TestOutOfOrderCounted(t *testing.T) {
	v := newTestView(t)
	seq := []change.Change{
		ins(2, "a", "g", 1),
		ins(1, "b", "g", 1), // late
		ins(3, "c", "g", 1),
		ins(3, "d", "g", 1), // conflicting same version
	}
	var rejects int
	for _, c := range seq {
		if err := v.Submit(c); err != nil {
			rejects++
		}
	}
	if rejects != 2 || v.Stats().Rejected != 2 {
		t.Fatalf("rejects=%d stats=%d", rejects, v.Stats().Rejected)
	}
	g, _ := v.Lookup("g")
	if g.Count != 2 {
		t.Fatalf("view must contain only 2 accepted records, got %v", g.Count)
	}
}
