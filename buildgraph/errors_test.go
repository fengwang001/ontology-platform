package buildgraph

import (
	"errors"
	"testing"
)

func TestMissingSourceOrdering(t *testing.T) {
	g := New()
	mustAdd(t, g, "z", "z", []string{"z.out"}, []string{"shared"}, nil, nil, false)
	mustAdd(t, g, "a", "a", []string{"a.out"}, nil, []string{"missing-a"}, []string{"missing-o"}, false)
	mustAdd(t, g, "m", "m", []string{"m.out"}, []string{"a.out", "missing-m1", "missing-m2"}, nil, nil, false)
	mustSet(t, g, "shared", 1)
	if err := g.Complete("z", map[string]int64{"z.out": 1}); err != nil {
		t.Fatal(err)
	}
	_, err := g.DirtySet([]string{"z.out", "m.out"})
	var ms *MissingSourceError
	if !errors.As(err, &ms) {
		t.Fatalf("want MissingSourceError, got %v", err)
	}
	if ms.EdgeID != "a" || ms.Path != "missing-a" {
		t.Fatalf("want a/missing-a, got %s/%s", ms.EdgeID, ms.Path)
	}
	if !errors.Is(err, ErrMissingInput) {
		t.Fatal("MissingSourceError must satisfy errors.Is(ErrMissingInput)")
	}
	mustSet(t, g, "missing-a", 1)
	mustSet(t, g, "missing-o", 1)
	_, err = g.DirtySet([]string{"m.out"})
	if !errors.As(err, &ms) || ms.EdgeID != "m" || ms.Path != "missing-m1" {
		t.Fatalf("want m/missing-m1, got edge=%s path=%s", ms.EdgeID, ms.Path)
	}
	// Completing everything makes the call succeed.
	mustSet(t, g, "missing-m1", 1)
	mustSet(t, g, "missing-m2", 1)
	if err := g.Complete("a", map[string]int64{"a.out": 1}); err != nil {
		t.Fatal(err)
	}
	if err := g.Complete("m", map[string]int64{"m.out": 1}); err != nil {
		t.Fatal(err)
	}
	if got := dirtySet(t, g, []string{"m.out"}); len(got) != 0 {
		t.Fatalf("expected clean closure, got %v", got)
	}
}

func TestRejectionPriorities(t *testing.T) {
	g := New()
	if err := g.AddEdge("", "c", []string{"o"}, nil, nil, nil, false); !errors.Is(err, ErrEmptyID) {
		t.Fatalf("empty id, got %v", err)
	}
	mustAdd(t, g, "e", "c", []string{"o"}, nil, nil, nil, false)
	if err := g.AddEdge("e", "c", nil, []string{""}, nil, nil, false); !errors.Is(err, ErrDuplicateID) {
		t.Fatalf("duplicate id, got %v", err)
	}
	if err := g.AddEdge("e2", "c", nil, []string{""}, nil, nil, false); !errors.Is(err, ErrNoOutputs) {
		t.Fatalf("no outputs, got %v", err)
	}
	if err := g.AddEdge("e3", "c", []string{"o", ""}, nil, nil, nil, false); !errors.Is(err, ErrEmptyPath) {
		t.Fatalf("empty path, got %v", err)
	}
	if err := g.AddEdge("e4", "c", []string{"o", "o2"}, []string{"o2"}, nil, nil, false); !errors.Is(err, ErrOutputOwned) {
		t.Fatalf("owned output, got %v", err)
	}
	if err := g.AddEdge("e5", "c", []string{"o5", "o6"}, []string{"o5"}, nil, nil, false); !errors.Is(err, ErrPathIsInAndOut) {
		t.Fatalf("path both in and out, got %v", err)
	}
	mustAdd(t, g, "x", "x", []string{"x.out"}, []string{"y.out"}, nil, nil, false)
	mustAdd(t, g, "y", "y", []string{"y.out"}, nil, []string{"z.out"}, nil, false)
	mustAdd(t, g, "z", "z", []string{"z.out"}, []string{"seed"}, nil, nil, false)
	// Demonstrate the 3-edge cycle directly with a throwaway graph.
	gc := New()
	mustAdd(t, gc, "cx", "x", []string{"cx.out"}, []string{"cy.out"}, nil, nil, false)
	mustAdd(t, gc, "cy", "y", []string{"cy.out"}, nil, []string{"cz.out"}, nil, false)
	if err := gc.AddEdge("cz", "z", []string{"cz.out"}, nil, nil, []string{"cx.out"}, false); !errors.Is(err, ErrCycle) {
		t.Fatalf("order-only cycle, got %v", err)
	}
	mustAdd(t, gc, "cz", "z", []string{"cz.out"}, []string{"cseed"}, nil, nil, false)

	if err := g.Complete("nope", nil); !errors.Is(err, ErrEdgeNotFound) {
		t.Fatalf("edge not found, got %v", err)
	}
	if err := g.Complete("e", map[string]int64{"o": 1, "extra": 1}); !errors.Is(err, ErrOutputSetMismatch) {
		t.Fatalf("output set mismatch, got %v", err)
	}
	if err := g.Complete("e", map[string]int64{"o": 0}); !errors.Is(err, ErrInvalidMtime) {
		t.Fatalf("invalid mtime, got %v", err)
	}
	mustAdd(t, g, "need", "c", []string{"need.out"}, []string{"need.in"}, nil, nil, false)
	if err := g.Complete("need", map[string]int64{"need.out": 1}); !errors.Is(err, ErrMissingInput) {
		t.Fatalf("missing input, got %v", err)
	}

	if err := g.SetMtime("", 0); !errors.Is(err, ErrEmptyPath) {
		t.Fatalf("empty path, got %v", err)
	}
	if err := g.SetMtime("p", 0); !errors.Is(err, ErrInvalidMtime) {
		t.Fatalf("t<1, got %v", err)
	}
	if err := g.Remove(""); !errors.Is(err, ErrEmptyPath) {
		t.Fatalf("remove empty path, got %v", err)
	}

	mustSet(t, g, "seed", 1)
	for _, id := range []string{"z", "y", "x"} {
		if err := g.Complete(id, map[string]int64{id + ".out": 1}); err != nil {
			t.Fatalf("complete %s: %v", id, err)
		}
	}
	_, err := g.DirtySet([]string{"unknown-1", "x.out", "unknown-2"})
	var te *TargetError
	if !errors.As(err, &te) || te.Path != "unknown-1" {
		t.Fatalf("want first unknown target, got %+v", te)
	}
	if !errors.Is(err, ErrTargetNotInGraph) {
		t.Fatal("TargetError must satisfy errors.Is(ErrTargetNotInGraph)")
	}
	// A path appearing only as an edge input is "in the graph" as a target.
	if _, err := g.DirtySet([]string{"seed"}); err != nil {
		t.Fatalf("source input target should be accepted, got %v", err)
	}
}

func TestRejectionsDoNotMutate(t *testing.T) {
	g := New()
	mustAdd(t, g, "p", "c", []string{"p.out"}, []string{"p.in"}, nil, nil, false)
	mustSet(t, g, "p.in", 5)
	mustSet(t, g, "p.out", 7)
	if err := g.Remove("p.in"); err != nil {
		t.Fatal(err)
	}
	if err := g.Complete("p", map[string]int64{"p.out": 0}); !errors.Is(err, ErrInvalidMtime) {
		t.Fatalf("want invalid mtime, got %v", err)
	}
	if g.mtimes["p.out"] != 7 {
		t.Fatalf("mtime mutated by rejected Complete: %d", g.mtimes["p.out"])
	}
	if _, ok := g.logs["p"]; ok {
		t.Fatal("log written by rejected Complete")
	}
	// Missing input rejection also leaves state untouched.
	if err := g.Complete("p", map[string]int64{"p.out": 9}); !errors.Is(err, ErrMissingInput) {
		t.Fatalf("want missing input, got %v", err)
	}
	if g.mtimes["p.out"] != 7 {
		t.Fatalf("mtime mutated: %d", g.mtimes["p.out"])
	}

	// Rejected AddEdge leaves producer/user tables untouched.
	if err := g.AddEdge("q", "c", []string{"p.out"}, nil, nil, nil, false); !errors.Is(err, ErrOutputOwned) {
		t.Fatalf("want owned output, got %v", err)
	}
	if _, ok := g.edges["q"]; ok {
		t.Fatal("rejected edge was inserted")
	}
	// Rejected SetMtime leaves file absent.
	if err := g.SetMtime("absent", 0); !errors.Is(err, ErrInvalidMtime) {
		t.Fatalf("want invalid mtime, got %v", err)
	}
	if _, ok := g.mtimes["absent"]; ok {
		t.Fatal("rejected SetMtime created file")
	}
}

func TestRemoveNoOp(t *testing.T) {
	g := New()
	if err := g.Remove("never-existed"); err != nil {
		t.Fatalf("removing absent file should succeed, got %v", err)
	}
}
