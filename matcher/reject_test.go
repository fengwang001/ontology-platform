package matcher

import (
	"context"
	"errors"
	"testing"

	"ontology/graph"
)

func TestReject_EmptyPattern(t *testing.T) {
	m := New()
	matches, _, err := m.Match(context.Background(), graph.New().Snapshot(), &Pattern{})
	if !errors.Is(err, ErrEmptyPattern) {
		t.Fatalf("err = %v, want ErrEmptyPattern", err)
	}
	if matches != nil {
		t.Fatalf("rejected query must not return partial results, got %v", matches)
	}
}

func TestReject_UnconstrainedVariable(t *testing.T) {
	m := New()
	p := &Pattern{
		Nodes: []NodeVar{
			{Name: "a", Type: "Person"},
			{Name: "free"},
		},
	}
	matches, _, err := m.Match(context.Background(), graph.New().Snapshot(), p)
	if !errors.Is(err, ErrUnconstrainedVariable) {
		t.Fatalf("err = %v, want ErrUnconstrainedVariable", err)
	}
	if matches != nil {
		t.Fatalf("rejected query must not return partial results, got %v", matches)
	}
}

func TestReject_InvalidPatternDefinitions(t *testing.T) {
	cases := []struct {
		name string
		p    *Pattern
	}{
		{"empty var name", &Pattern{Nodes: []NodeVar{{Type: "Person"}}}},
		{"duplicate var", &Pattern{Nodes: []NodeVar{
			{Name: "a", Type: "Person"}, {Name: "a", Type: "Person"}}}},
		{"edge missing source", &Pattern{
			Nodes: []NodeVar{{Name: "a", Type: "Person"}},
			Edges: []EdgePat{{Type: "knows", Source: "x", Target: "a"}},
		}},
		{"edge missing target", &Pattern{
			Nodes: []NodeVar{{Name: "a", Type: "Person"}},
			Edges: []EdgePat{{Type: "knows", Source: "a", Target: "x"}},
		}},
		{"bad operator", &Pattern{Nodes: []NodeVar{{
			Name: "a", Type: "Person",
			Constraints: []Constraint{{Attr: "x", Op: "~", Value: 1}}}}}},
	}
	m := New()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			matches, _, err := m.Match(context.Background(), graph.New().Snapshot(), tc.p)
			if !errors.Is(err, ErrPatternDefinition) {
				t.Fatalf("err = %v, want ErrPatternDefinition", err)
			}
			if matches != nil {
				t.Fatalf("rejected query must not return partial results, got %v", matches)
			}
		})
	}
}

func TestReject_FullScanDegeneration(t *testing.T) {
	g := buildDiamondGraph(t, diamondEdges())
	m := New()
	p := &Pattern{
		Nodes: []NodeVar{
			{Name: "a"},
			{Name: "b", Type: "Person"},
		},
		Edges: []EdgePat{{Type: "knows", Source: "a", Target: "b"}},
	}
	matches, _, err := m.Match(context.Background(), g.Snapshot(), p)
	if !errors.Is(err, ErrFullScanDegeneration) {
		t.Fatalf("err = %v, want ErrFullScanDegeneration", err)
	}
	if matches != nil {
		t.Fatalf("rejected query must not return partial results, got %v", matches)
	}
}

// TestReject_InconsistentBinding 验证“同一变量绑定不同对象”会在真实
// 搜索路径上被拒绝，且查询不返回部分结果。
func TestReject_InconsistentBinding(t *testing.T) {
	g := buildDiamondGraph(t, diamondEdges())
	m := New()
	m.checkBinding = func(varName string, assignment map[string]string, id string) error {
		// 模拟搜索器试图把已绑定为 someone-else 的变量 b 再绑定到 id。
		if varName == "b" {
			return defaultCheckBinding("b",
				map[string]string{"b": "someone-else"}, id)
		}
		return nil
	}
	p := &Pattern{
		Nodes: []NodeVar{
			{Name: "a", Type: "Person"},
			{Name: "b", Type: "Person"},
		},
		Edges: []EdgePat{{Type: "knows", Source: "a", Target: "b"}},
	}
	matches, _, err := m.Match(context.Background(), g.Snapshot(), p)
	if !errors.Is(err, ErrInconsistentBinding) {
		t.Fatalf("err = %v, want ErrInconsistentBinding", err)
	}
	if matches != nil {
		t.Fatalf("rejected query must not return partial results, got %v", matches)
	}
}

// TestReject_DuplicateMatch 验证去重被突破时（同组对象映射重复出现）
// 查询整体被拒绝，且不返回部分结果。
func TestReject_DuplicateMatch(t *testing.T) {
	g := buildDiamondGraph(t, diamondEdges())
	m := New()
	m.onDuplicate = func(map[string]string) error {
		return ErrDuplicateMatch
	}
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
	matches, _, err := m.Match(context.Background(), g.Snapshot(), p)
	if !errors.Is(err, ErrDuplicateMatch) {
		t.Fatalf("err = %v, want ErrDuplicateMatch", err)
	}
	if matches != nil {
		t.Fatalf("rejected query must not return partial results, got %v", matches)
	}
}
