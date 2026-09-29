package matcher

import (
	"context"
	"errors"
	"testing"

	"ontology/graph"
)

func TestMatch_BasicPattern(t *testing.T) {
	g := buildDiamondGraph(t, diamondEdges())
	logger := &testLogger{}
	m := New(WithLogger(logger))

	p := &Pattern{
		Nodes: []NodeVar{
			{Name: "person", Type: "Person"},
			{Name: "company", Type: "Company"},
		},
		Edges: []EdgePat{
			{Type: "worksAt", Source: "person", Target: "company"},
		},
	}
	matches, _, err := m.Match(context.Background(), g.Snapshot(), p)
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	assertMatches(t, matches, []Match{
		{"person": "p1", "company": "c1"},
		{"person": "p2", "company": "c1"},
	})

	for _, match := range matches {
		if len(match) != len(p.Nodes) {
			t.Fatalf("binding size = %d, want %d", len(match), len(p.Nodes))
		}
	}

	if !logger.contains("match START pattern=") || !logger.contains("match DONE") {
		t.Fatalf("pattern/summary log missing: %v", logger.lines)
	}
	if !logger.contains("match ACCEPT") || !logger.contains("canonical=") {
		t.Fatalf("match/decision log missing: %v", logger.lines)
	}
}

func TestMatch_IsomorphicDedup(t *testing.T) {
	g := buildDiamondGraph(t, diamondEdges())
	m := New()

	p := &Pattern{
		Nodes: []NodeVar{
			{Name: "person1", Type: "Person"},
			{Name: "person2", Type: "Person"},
			{Name: "company", Type: "Company"},
		},
		Edges: []EdgePat{
			{Type: "worksAt", Source: "person1", Target: "company"},
			{Type: "worksAt", Source: "person2", Target: "company"},
		},
	}
	matches, stats, err := m.Match(context.Background(), g.Snapshot(), p)
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	assertMatches(t, matches, []Match{
		{"person1": "p1", "person2": "p2", "company": "c1"},
	})
	if stats.IsomorphicDuplicates != 1 {
		t.Fatalf("expected 1 suppressed isomorphic duplicate, got %d", stats.IsomorphicDuplicates)
	}
	if stats.Embeddings != 2 {
		t.Fatalf("expected 2 raw embeddings, got %d", stats.Embeddings)
	}
}

func TestMatch_Constraints(t *testing.T) {
	g := buildDiamondGraph(t, diamondEdges())
	m := New()

	p := &Pattern{
		Nodes: []NodeVar{
			{
				Name: "person", Type: "Person",
				Constraints: []Constraint{{Attr: "age", Op: OpGt, Value: 28}},
			},
			{Name: "company", Type: "Company"},
		},
		Edges: []EdgePat{{Type: "worksAt", Source: "person", Target: "company"}},
	}
	matches, stats, err := m.Match(context.Background(), g.Snapshot(), p)
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	assertMatches(t, matches, []Match{
		{"person": "p1", "company": "c1"},
	})
	if stats.PrunedByConstraint < 1 {
		t.Fatalf("expected constraint pruning, stats=%+v", stats)
	}
}

func TestMatch_MissingAttributeFailsConstraint(t *testing.T) {
	g := buildDiamondGraph(t, diamondEdges())
	m := New()
	p := &Pattern{
		Nodes: []NodeVar{
			{Name: "company", Type: "Company",
				Constraints: []Constraint{{Attr: "nonexistent", Op: OpEq, Value: "x"}}},
		},
	}
	matches, _, err := m.Match(context.Background(), g.Snapshot(), p)
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("expected no matches, got %v", matches)
	}
}

func TestMatch_PruningByEdges(t *testing.T) {
	g := buildDiamondGraph(t, diamondEdges())
	m := New()
	p := &Pattern{
		Nodes: []NodeVar{
			{Name: "a", Type: "Person"},
			{Name: "b", Type: "Person"},
		},
		Edges: []EdgePat{{Type: "knows", Source: "a", Target: "b"}},
	}
	matches, stats, err := m.Match(context.Background(), g.Snapshot(), p)
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	assertMatches(t, matches, []Match{
		{"a": "p1", "b": "p2"},
	})
	if stats.PrunedByEdge == 0 || stats.Backtracks == 0 {
		t.Fatalf("expected edge pruning and backtracking, stats=%+v", stats)
	}
}

func TestMatch_EmptyCandidatePrunesImmediately(t *testing.T) {
	g := buildDiamondGraph(t, diamondEdges())
	m := New()
	p := &Pattern{
		Nodes: []NodeVar{
			{Name: "ghost", Type: "Nonexistent"},
			{Name: "person", Type: "Person"},
		},
		Edges: []EdgePat{{Type: "knows", Source: "person", Target: "ghost"}},
	}
	matches, _, err := m.Match(context.Background(), g.Snapshot(), p)
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("expected zero matches, got %v", matches)
	}
}

func TestMatch_SameVariableSameObject(t *testing.T) {
	g := buildDiamondGraph(t, diamondEdges())
	m := New()
	// p1 与 p2 在同一公司工作；该模式要求两人是同一个变量绑定，
	// 即对象自身与自身通过 worksAt 到同一公司——不存在自环边，
	// 因此无匹配，但关键是任何输出的匹配中该变量只能出现一个对象。
	p := &Pattern{
		Nodes: []NodeVar{
			{Name: "person", Type: "Person"},
			{Name: "company", Type: "Company"},
		},
		Edges: []EdgePat{
			{Type: "worksAt", Source: "person", Target: "company"},
		},
	}
	matches, _, err := m.Match(context.Background(), g.Snapshot(), p)
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	for _, match := range matches {
		if match["person"] == "" || match["company"] == "" {
			t.Fatalf("variable bound to empty object: %v", match)
		}
		if obj, ok := g.Snapshot().Object(match["person"]); !ok || obj.ID != match["person"] {
			t.Fatalf("inconsistent binding: %v", match)
		}
	}
}

var _ = graph.New
var _ = errors.Is
