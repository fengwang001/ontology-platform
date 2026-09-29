package matcher

import (
	"context"
	"testing"

	"ontology/graph"
)

// TestMatch_ChainEndpointsSymmetric 验证无向意义上的对称端点：
// 两个 Person 通过同一个 Company 的出/入结构对称时仍按同构去重。
func TestMatch_ChainEndpointsSymmetric(t *testing.T) {
	g := graph.New()
	for _, id := range []string{"p1", "p2"} {
		if err := g.AddObject(graph.Object{ID: id, Type: "Person"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := g.AddObject(graph.Object{ID: "c1", Type: "Company"}); err != nil {
		t.Fatal(err)
	}
	_ = g.AddLink(graph.Link{Type: "worksAt", Source: "p1", Target: "c1"})
	_ = g.AddLink(graph.Link{Type: "worksAt", Source: "p2", Target: "c1"})

	m := New()
	p := &Pattern{
		Nodes: []NodeVar{
			{Name: "p1", Type: "Person"},
			{Name: "p2", Type: "Person"},
			{Name: "c", Type: "Company"},
		},
		Edges: []EdgePat{
			{Type: "worksAt", Source: "p1", Target: "c"},
			{Type: "worksAt", Source: "p2", Target: "c"},
		},
	}
	matches, stats, err := m.Match(context.Background(), g.Snapshot(), p)
	if err != nil {
		t.Fatal(err)
	}
	assertMatches(t, matches, []Match{
		{"p1": "p1", "p2": "p2", "c": "c1"},
	})
	if stats.IsomorphicDuplicates != 1 {
		t.Fatalf("expected one suppressed duplicate, got %d", stats.IsomorphicDuplicates)
	}
}

// TestMatch_NeOperators 覆盖 != 与 < 算子以及字符串相等。
func TestMatch_Operators(t *testing.T) {
	g := graph.New()
	_ = g.AddObject(graph.Object{ID: "a", Type: "Person",
		Attributes: map[string]any{"name": "alice", "age": 30}})
	_ = g.AddObject(graph.Object{ID: "b", Type: "Person",
		Attributes: map[string]any{"name": "bob", "age": 20}})
	m := New()

	p := &Pattern{
		Nodes: []NodeVar{{
			Name: "p", Type: "Person",
			Constraints: []Constraint{
				{Attr: "name", Op: OpNe, Value: "bob"},
				{Attr: "age", Op: OpLt, Value: 40},
			},
		}},
	}
	matches, _, err := m.Match(context.Background(), g.Snapshot(), p)
	if err != nil {
		t.Fatal(err)
	}
	assertMatches(t, matches, []Match{{"p": "a"}})
}
