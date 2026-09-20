package trace

import (
	"fmt"
	"testing"
)

// seqGen returns a deterministic ID generator producing id1, id2, ...
// (wire-format-safe: no '-' or '=' characters).
func seqGen() func() SpanID {
	n := 0
	return func() SpanID {
		n++
		return SpanID(fmt.Sprintf("id%d", n))
	}
}

// Semantics 1: derivation — child inherits the trace ID, gets a fresh
// span ID, records the parent ID; root has an empty parent ID; the
// trace ID survives three levels of derivation.
func TestDerivation(t *testing.T) {
	root := NewRoot(seqGen(), true)
	if root.TraceID() != root.SpanID() {
		t.Fatalf("root: TraceID %q != SpanID %q", root.TraceID(), root.SpanID())
	}
	if root.ParentID() != "" {
		t.Fatalf("root: ParentID = %q, want empty", root.ParentID())
	}

	child := root.Child()
	if child.TraceID() != root.TraceID() {
		t.Fatalf("child: TraceID %q != root %q", child.TraceID(), root.TraceID())
	}
	if child.SpanID() == root.SpanID() {
		t.Fatalf("child: SpanID %q not fresh", child.SpanID())
	}
	if child.ParentID() != root.SpanID() {
		t.Fatalf("child: ParentID %q != root SpanID %q", child.ParentID(), root.SpanID())
	}

	grand := child.Child()
	if grand.TraceID() != root.TraceID() {
		t.Fatalf("grandchild: TraceID %q != root %q", grand.TraceID(), root.TraceID())
	}
	if grand.ParentID() != child.SpanID() {
		t.Fatalf("grandchild: ParentID %q != child SpanID %q", grand.ParentID(), child.SpanID())
	}
	if grand.SpanID() == child.SpanID() || grand.SpanID() == root.SpanID() {
		t.Fatalf("grandchild: SpanID %q collides with ancestor", grand.SpanID())
	}
}

// Semantics 2: the sampling decision is inherited, never recomputed.
// Derivation must not draw from any randomness source other than the
// injected ID generator.
func TestSampledInheritedOnly(t *testing.T) {
	for _, sampled := range []bool{true, false} {
		root := NewRoot(seqGen(), sampled)
		s := root
		for depth := 0; depth < 5; depth++ {
			s = s.Child()
			if s.Sampled() != sampled {
				t.Fatalf("depth %d: Sampled = %v, want %v", depth+1, s.Sampled(), sampled)
			}
		}
	}
}

// Semantics 2 (cont.): derivation consumes exactly one ID per child
// from the injected generator — no hidden random draws.
func TestDerivationConsumesOnlyGenerator(t *testing.T) {
	n := 0
	gen := func() SpanID {
		n++
		return SpanID(fmt.Sprintf("g%d", n))
	}
	root := NewRoot(gen, true) // consumes g1
	if n != 1 {
		t.Fatalf("NewRoot made %d generator calls, want 1", n)
	}
	c := root.Child()
	if n != 2 {
		t.Fatalf("Child made %d generator calls total, want 2", n)
	}
	if c.SpanID() != "g2" {
		t.Fatalf("child SpanID = %q, want g2", c.SpanID())
	}
}

// Semantics 8 (parent/child side): mutating a parent's baggage after
// derivation does not leak into the child, and vice versa — the two
// spans do not share a mutable map.
func TestNoSharedMapBetweenParentAndChild(t *testing.T) {
	root := NewRoot(seqGen(), true)
	root.SetBaggage("a", "1")
	child := root.Child()

	root.SetBaggage("b", "parent-only")
	child.SetBaggage("c", "child-only")
	root.SetBaggage("a", "parent-rewrote")

	if v, _ := child.Baggage("a"); v != "1" {
		t.Fatalf("child saw parent rewrite: a = %q, want 1", v)
	}
	if _, ok := child.Baggage("b"); ok {
		t.Fatal("child saw baggage added to parent after derivation")
	}
	if _, ok := root.Baggage("c"); ok {
		t.Fatal("parent saw baggage added to child")
	}
	if v, _ := root.Baggage("a"); v != "parent-rewrote" {
		t.Fatalf("parent lost its own write: a = %q", v)
	}
}
