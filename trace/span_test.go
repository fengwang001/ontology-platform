package trace_test

import (
	"fmt"
	"testing"

	"ontology/trace"
)

// seqGen returns a generator producing id000001, id000002, ...
// deterministically. IDs avoid '-' and ';', the wire separators.
func seqGen() func() trace.SpanID {
	n := 0
	return func() trace.SpanID {
		n++
		return trace.SpanID(fmt.Sprintf("id%06d", n))
	}
}

func TestDerivation(t *testing.T) {
	root := trace.NewRoot(seqGen(), true)
	if root.TraceID() != root.SpanID() {
		t.Fatalf("root: TraceID %q != SpanID %q", root.TraceID(), root.SpanID())
	}
	if root.ParentID() != "" {
		t.Fatalf("root: ParentID = %q, want empty", root.ParentID())
	}

	child := root.Child()
	if child.TraceID() != root.TraceID() {
		t.Fatalf("child: TraceID %q, want %q", child.TraceID(), root.TraceID())
	}
	if child.SpanID() == root.SpanID() {
		t.Fatalf("child: SpanID %q not fresh", child.SpanID())
	}
	if child.ParentID() != root.SpanID() {
		t.Fatalf("child: ParentID %q, want %q", child.ParentID(), root.SpanID())
	}

	grandchild := child.Child()
	if grandchild.TraceID() != root.TraceID() {
		t.Fatalf("grandchild: TraceID %q, want root's %q", grandchild.TraceID(), root.TraceID())
	}
	if grandchild.ParentID() != child.SpanID() {
		t.Fatalf("grandchild: ParentID %q, want %q", grandchild.ParentID(), child.SpanID())
	}
}

func TestSampledInheritedNotRecomputed(t *testing.T) {
	for _, sampled := range []bool{true, false} {
		root := trace.NewRoot(seqGen(), sampled)
		child := root.Child()
		grandchild := child.Child()
		if child.Sampled() != sampled || grandchild.Sampled() != sampled {
			t.Fatalf("sampled=%v: child=%v grandchild=%v",
				sampled, child.Sampled(), grandchild.Sampled())
		}
	}
}

func TestDerivationUsesOnlyGenerator(t *testing.T) {
	calls := 0
	gen := func() trace.SpanID {
		calls++
		return trace.SpanID(fmt.Sprintf("g%d", calls))
	}
	root := trace.NewRoot(gen, true)
	if calls != 1 {
		t.Fatalf("NewRoot called generator %d times, want 1", calls)
	}
	root.Child()
	if calls != 2 {
		t.Fatalf("Child called generator %d times total, want 2", calls)
	}
}
