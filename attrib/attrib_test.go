package attrib

import (
	"testing"

	"ontology/stack"
	"ontology/tree"
)

func fr(n string) stack.Frame { return stack.Frame{Func: n} }

func build(stacks ...[]stack.Frame) *tree.Node {
	tr := tree.New()
	for _, s := range stacks {
		tr.Insert(stack.Normalize(s, 64))
	}
	return tr.Snapshot()
}

func findFunc(stats []FuncStat, name string) FuncStat {
	for _, s := range stats {
		if s.Func == name {
			return s
		}
	}
	return FuncStat{Func: name}
}

func TestByFunc(t *testing.T) {
	cases := []struct {
		name      string
		stacks    [][]stack.Frame
		query     string
		wantSelf  int
		wantTotal int
		wantEmpty bool
	}{
		{
			"recursive-F-outer-only",
			[][]stack.Frame{{fr("A"), fr("F"), fr("G"), fr("F"), fr("H")}},
			"F", 0, 1, false,
		},
		{
			"two-stacks-F-total",
			[][]stack.Frame{
				{fr("A"), fr("F"), fr("H")},
				{fr("B"), fr("F"), fr("K")},
			},
			"F", 0, 2, false,
		},
		{
			"leaf-self",
			[][]stack.Frame{{fr("A"), fr("B")}, {fr("A"), fr("C")}},
			"A", 0, 2, false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := New(build(tc.stacks...))
			stats := a.ByFunc()
			if tc.wantEmpty {
				if len(stats) != 0 {
					t.Fatalf("want empty, got %d", len(stats))
				}
				return
			}
			got := findFunc(stats, tc.query)
			if got.Self != tc.wantSelf || got.Total != tc.wantTotal {
				t.Fatalf("%s self=%d total=%d want (%d,%d)",
					tc.query, got.Self, got.Total, tc.wantSelf, tc.wantTotal)
			}
			if a.RebuildCount() != 0 {
				t.Fatalf("rebuild count=%d want 0", a.RebuildCount())
			}
		})
	}
}

func TestZeroSampleEmpty(t *testing.T) {
	a := New(build())
	if len(a.ByFunc()) != 0 || len(a.NodesBySelf()) != 0 || len(a.NodesByTotal()) != 0 {
		t.Fatal("zero samples must yield empty attribution")
	}
}

func TestSortOrders(t *testing.T) {
	a := New(build(
		[]stack.Frame{fr("A"), fr("B")},
		[]stack.Frame{fr("A"), fr("B")},
		[]stack.Frame{fr("A"), fr("C")},
	))
	bySelf := a.NodesBySelf()
	if bySelf[0].Frame.Func != "B" || bySelf[0].Self != 2 {
		t.Fatalf("top self=%v", bySelf[0].Frame.Func)
	}
	byTotal := a.NodesByTotal()
	if byTotal[0].Frame.Func != "A" || byTotal[0].Total != 3 {
		t.Fatalf("top total=%v", byTotal[0].Frame.Func)
	}
}

func TestExcludeSplice(t *testing.T) {
	// A -> F -> G -> leaf H；排除 F 后，G 提升为 A 的子节点，F 不出现。
	a := New(build([]stack.Frame{fr("A"), fr("F"), fr("G"), fr("H")}))
	stats := a.Exclude("F")
	if findFunc(stats, "F").Total != 0 {
		t.Fatal("excluded F must be absent")
	}
	g := findFunc(stats, "G")
	if g.Total != 1 {
		t.Fatalf("G total after splice=%d want 1", g.Total)
	}
}
