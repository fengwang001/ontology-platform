package buildgraph

import (
	"sort"
	"testing"
)

func mustAdd(t *testing.T, g *Graph, id, cmd string, outputs, explicit, implicit, orderOnly []string, restat bool) {
	t.Helper()
	if err := g.AddEdge(id, cmd, outputs, explicit, implicit, orderOnly, restat); err != nil {
		t.Fatalf("AddEdge(%s): %v", id, err)
	}
}

func mustSet(t *testing.T, g *Graph, path string, mt int64) {
	t.Helper()
	if err := g.SetMtime(path, mt); err != nil {
		t.Fatalf("SetMtime(%s,%d): %v", path, mt, err)
	}
}

func dirtySet(t *testing.T, g *Graph, targets []string) []string {
	t.Helper()
	got, err := g.DirtySet(targets)
	if err != nil {
		t.Fatalf("DirtySet(%v): %v", targets, err)
	}
	return got
}

func TestEqualVsOneLess(t *testing.T) {
	g := New()
	mustAdd(t, g, "e", "cc", []string{"out"}, []string{"in"}, nil, nil, false)
	mustSet(t, g, "in", 10)
	mustSet(t, g, "out", 10)
	if err := g.Complete("e", map[string]int64{"out": 10}); err != nil {
		t.Fatal(err)
	}
	if got := dirtySet(t, g, []string{"out"}); len(got) != 0 {
		t.Fatalf("equal mtimes should be clean, got %v", got)
	}
	mustSet(t, g, "in", 11)
	if got := dirtySet(t, g, []string{"out"}); len(got) != 1 || got[0] != "e" {
		t.Fatalf("input one newer should be dirty, got %v", got)
	}
}

func TestMultiOutputMin(t *testing.T) {
	g := New()
	mustAdd(t, g, "e", "cc", []string{"o1", "o2"}, []string{"in"}, nil, nil, false)
	mustSet(t, g, "in", 5)
	if err := g.Complete("e", map[string]int64{"o1": 5, "o2": 9}); err != nil {
		t.Fatal(err)
	}
	if got := dirtySet(t, g, []string{"o1"}); len(got) != 0 {
		t.Fatalf("min output == input max should be clean, got %v", got)
	}
	mustSet(t, g, "o1", 4)
	if got := dirtySet(t, g, []string{"o2"}); len(got) != 1 {
		t.Fatalf("min output older than input should be dirty, got %v", got)
	}
}

func TestMissingLogAndCommandChange(t *testing.T) {
	g := New()
	mustAdd(t, g, "e", "cc", []string{"out"}, []string{"in"}, nil, nil, false)
	mustSet(t, g, "in", 3)
	mustSet(t, g, "out", 9)
	if got := dirtySet(t, g, []string{"out"}); len(got) != 1 {
		t.Fatalf("edge without log should be dirty, got %v", got)
	}
	if err := g.Complete("e", map[string]int64{"out": 9}); err != nil {
		t.Fatal(err)
	}
	if got := dirtySet(t, g, []string{"out"}); len(got) != 0 {
		t.Fatalf("completed edge should be clean, got %v", got)
	}
	g.edges["e"].cmd = "gcc"
	if got := dirtySet(t, g, []string{"out"}); len(got) != 1 {
		t.Fatalf("command mismatch with log should be dirty, got %v", got)
	}
}

func TestRestatInMax(t *testing.T) {
	g := New()
	mustAdd(t, g, "r", "gen", []string{"r.out"}, []string{"r.in"}, nil, nil, true)
	mustSet(t, g, "r.in", 7)
	if err := g.Complete("r", map[string]int64{"r.out": 1}); err != nil {
		t.Fatal(err)
	}
	if got := dirtySet(t, g, []string{"r.out"}); len(got) != 0 {
		t.Fatalf("restat edge with caught-up inMax should be clean, got %v", got)
	}
	mustSet(t, g, "r.in", 8)
	if got := dirtySet(t, g, []string{"r.out"}); len(got) != 1 {
		t.Fatalf("restat edge with newer input should be dirty, got %v", got)
	}
}

func TestOrderOnlySemantics(t *testing.T) {
	g := New()
	mustAdd(t, g, "tool", "build-tool", []string{"tool"}, []string{"tool.src"}, nil, nil, false)
	mustAdd(t, g, "main", "build-main", []string{"main.out"}, []string{"src"}, nil, []string{"tool"}, false)
	mustSet(t, g, "tool.src", 2)
	mustSet(t, g, "src", 2)
	if err := g.Complete("tool", map[string]int64{"tool": 5}); err != nil {
		t.Fatal(err)
	}
	if err := g.Complete("main", map[string]int64{"main.out": 6}); err != nil {
		t.Fatal(err)
	}
	mustSet(t, g, "tool", 100)
	if got := dirtySet(t, g, []string{"main.out"}); len(got) != 0 {
		t.Fatalf("order-only mtime change must not dirty main, got %v", got)
	}
	mustSet(t, g, "tool.src", 101)
	got := dirtySet(t, g, []string{"main.out"})
	if len(got) != 1 || got[0] != "tool" {
		t.Fatalf("only dirty order-only producer should be listed, got %v", got)
	}
}

func TestUpstreamPropagation(t *testing.T) {
	g := New()
	mustAdd(t, g, "a", "a", []string{"a.out"}, []string{"src"}, nil, nil, false)
	mustAdd(t, g, "b", "b", []string{"b.out"}, []string{"a.out"}, nil, nil, false)
	mustSet(t, g, "src", 1)
	if err := g.Complete("a", map[string]int64{"a.out": 2}); err != nil {
		t.Fatal(err)
	}
	if err := g.Complete("b", map[string]int64{"b.out": 3}); err != nil {
		t.Fatal(err)
	}
	mustSet(t, g, "src", 9)
	got := dirtySet(t, g, []string{"b.out"})
	sort.Strings(got)
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("dirty propagation should list a,b got %v", got)
	}
}

func TestRestatUpstreamCompleteCleansDownstream(t *testing.T) {
	g := New()
	mustAdd(t, g, "u", "gen", []string{"u.out"}, []string{"u.in"}, nil, nil, true)
	mustAdd(t, g, "d", "cc", []string{"d.out"}, []string{"u.out"}, nil, nil, false)
	mustSet(t, g, "u.in", 10)
	if err := g.Complete("u", map[string]int64{"u.out": 10}); err != nil {
		t.Fatal(err)
	}
	if err := g.Complete("d", map[string]int64{"d.out": 11}); err != nil {
		t.Fatal(err)
	}
	mustSet(t, g, "u.in", 12)
	if got := dirtySet(t, g, []string{"d.out"}); len(got) != 2 {
		t.Fatalf("expected u,d dirty before rebuild, got %v", got)
	}
	if err := g.Complete("u", map[string]int64{"u.out": 10}); err != nil {
		t.Fatal(err)
	}
	if got := dirtySet(t, g, []string{"d.out"}); len(got) != 0 {
		t.Fatalf("both edges clean after restat re-complete, got %v", got)
	}
}
