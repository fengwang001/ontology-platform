package trace

import (
	"reflect"
	"testing"
)

// Semantics 3: baggage is copied at derivation time. The child sees
// everything the parent had at that instant; afterwards the two sides
// evolve independently, proven by alternating writes to the same key.
func TestBaggageCopyOnWriteAlternatingSameKey(t *testing.T) {
	root := NewRoot(seqGen(), true)
	root.SetBaggage("k", "p1")
	root.SetBaggage("pre", "visible")

	child := root.Child()

	// Child sees the parent's baggage as of derivation time.
	if v, ok := child.Baggage("k"); !ok || v != "p1" {
		t.Fatalf("child: k = %q,%v want p1,true", v, ok)
	}
	if v, ok := child.Baggage("pre"); !ok || v != "visible" {
		t.Fatalf("child: pre = %q,%v want visible,true", v, ok)
	}

	// Alternate writes to the same key on both sides.
	child.SetBaggage("k", "c1")
	root.SetBaggage("k", "p2")
	child.SetBaggage("k", "c2")
	root.SetBaggage("k", "p3")

	if v, _ := child.Baggage("k"); v != "c2" {
		t.Fatalf("child: k = %q, want c2 (parent writes leaked in)", v)
	}
	if v, _ := root.Baggage("k"); v != "p3" {
		t.Fatalf("parent: k = %q, want p3 (child writes leaked out)", v)
	}

	// New keys added after derivation stay on their own side.
	root.SetBaggage("parent-new", "x")
	child.SetBaggage("child-new", "y")
	if _, ok := child.Baggage("parent-new"); ok {
		t.Fatal("child saw key added to parent after derivation")
	}
	if _, ok := root.Baggage("child-new"); ok {
		t.Fatal("parent saw key added to child")
	}
}

// Semantics 4: keys come back sorted ascending regardless of insertion
// order; re-setting a key keeps the last value and a single key entry.
func TestBaggageKeysSortedAndUnique(t *testing.T) {
	s := NewRoot(seqGen(), false)
	for _, k := range []string{"zeta", "alpha", "mike", "alpha", "bravo", "zeta"} {
		s.SetBaggage(k, "v-"+k)
	}
	s.SetBaggage("alpha", "final")

	want := []string{"alpha", "bravo", "mike", "zeta"}
	if got := s.BaggageKeys(); !reflect.DeepEqual(got, want) {
		t.Fatalf("BaggageKeys = %v, want %v", got, want)
	}
	if v, _ := s.Baggage("alpha"); v != "final" {
		t.Fatalf("alpha = %q, want final (last write wins)", v)
	}

	// Stability: repeated calls return equal slices.
	if got := s.BaggageKeys(); !reflect.DeepEqual(got, want) {
		t.Fatalf("second BaggageKeys = %v, want %v", got, want)
	}
}

// Baggage on a key that was never set reports absence.
func TestBaggageMissingKey(t *testing.T) {
	s := NewRoot(seqGen(), true)
	if v, ok := s.Baggage("nope"); ok || v != "" {
		t.Fatalf("Baggage(nope) = %q,%v want \"\",false", v, ok)
	}
}
