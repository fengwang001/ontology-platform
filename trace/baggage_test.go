package trace_test

import (
	"slices"
	"testing"

	"ontology/trace"
)

func TestBaggageCopyOnWrite(t *testing.T) {
	root := trace.NewRoot(seqGen(), true)
	root.SetBaggage("shared", "at-derivation")

	child := root.Child()
	if v, ok := child.Baggage("shared"); !ok || v != "at-derivation" {
		t.Fatalf("child at derivation: got %q,%v", v, ok)
	}

	// Alternate writes to the same key on both sides.
	root.SetBaggage("shared", "parent-write")
	child.SetBaggage("shared", "child-write")
	root.SetBaggage("shared", "parent-final")
	child.SetBaggage("shared", "child-final")

	if v, _ := root.Baggage("shared"); v != "parent-final" {
		t.Fatalf("parent polluted by child: %q", v)
	}
	if v, _ := child.Baggage("shared"); v != "child-final" {
		t.Fatalf("child polluted by parent: %q", v)
	}

	// Keys added after derivation stay on their own side.
	root.SetBaggage("parent-only", "p")
	child.SetBaggage("child-only", "c")
	if _, ok := child.Baggage("parent-only"); ok {
		t.Fatal("child sees key added to parent after derivation")
	}
	if _, ok := root.Baggage("child-only"); ok {
		t.Fatal("parent sees key added to child after derivation")
	}
}

func TestBaggageKeysSortedAndUnique(t *testing.T) {
	s := trace.NewRoot(seqGen(), false)
	for _, k := range []string{"delta", "alpha", "charlie", "bravo"} {
		s.SetBaggage(k, "v-"+k)
	}
	s.SetBaggage("alpha", "last-wins")
	s.SetBaggage("alpha", "final")

	want := []string{"alpha", "bravo", "charlie", "delta"}
	if got := s.BaggageKeys(); !slices.Equal(got, want) {
		t.Fatalf("keys = %v, want %v", got, want)
	}
	if v, _ := s.Baggage("alpha"); v != "final" {
		t.Fatalf("duplicate set: got %q, want %q", v, "final")
	}
}

func TestBaggageAbsent(t *testing.T) {
	s := trace.NewRoot(seqGen(), false)
	if v, ok := s.Baggage("nope"); ok || v != "" {
		t.Fatalf("absent key: got %q,%v", v, ok)
	}
	if keys := s.BaggageKeys(); len(keys) != 0 {
		t.Fatalf("empty span keys = %v", keys)
	}
}
