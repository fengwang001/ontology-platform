package territory

import (
	"errors"
	"fmt"
	"testing"
)

func exampleTree(t *testing.T) *Tree {
	t.Helper()
	tr, err := NewTree(map[string][]string{
		Root: {"EU", "AS"},
		"EU": {"FR", "DE"},
		"AS": {"JP", "KR"},
	})
	if err != nil {
		t.Fatalf("NewTree: %v", err)
	}
	return tr
}

func TestNewTreeErrors(t *testing.T) {
	cases := []struct {
		name     string
		children map[string][]string
		want     error
	}{
		{"root missing", map[string][]string{"EU": {"FR"}}, ErrRootMissing},
		{"empty code", map[string][]string{Root: {""}}, ErrEmptyCode},
		{"root as child", map[string][]string{Root: {Root}}, ErrRootHasParent},
		{"duplicate child in list", map[string][]string{Root: {"EU", "EU"}}, ErrDuplicateChild},
		{"duplicate across parents", map[string][]string{Root: {"A", "B"}, "A": {"X"}, "B": {"X"}}, ErrDuplicateChild},
		{"orphan", map[string][]string{Root: {"A"}, "GHOST": {"X"}}, ErrOrphanNode},
		{"disjoint cycle", map[string][]string{Root: {}, "A": {"B"}, "B": {"A"}}, ErrOrphanNode},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewTree(tc.children)
			if !errors.Is(err, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
		})
	}
}

func TestDepthAndSizeLimits(t *testing.T) {
	chain := map[string][]string{Root: {"n1"}}
	for i := 1; i < MaxDepth; i++ {
		chain[fmt.Sprintf("n%d", i)] = []string{fmt.Sprintf("n%d", i+1)}
	}
	if _, err := NewTree(chain); err != nil {
		t.Fatalf("depth=%d chain should be valid: %v", MaxDepth, err)
	}
	chain[fmt.Sprintf("n%d", MaxDepth)] = []string{"tooDeep"}
	if _, err := NewTree(chain); !errors.Is(err, ErrDepthExceeded) {
		t.Fatalf("depth=%d want ErrDepthExceeded, got %v", MaxDepth+1, err)
	}

	big := map[string][]string{}
	kids := make([]string, MaxNodes-1)
	for i := range kids {
		kids[i] = fmt.Sprintf("k%06d", i)
	}
	big[Root] = kids
	if _, err := NewTree(big); err != nil {
		t.Fatalf("exactly %d nodes should be valid: %v", MaxNodes, err)
	}
	big[Root] = append(kids, "oneMore")
	if _, err := NewTree(big); !errors.Is(err, ErrTooManyNodes) {
		t.Fatalf("want ErrTooManyNodes, got %v", err)
	}
}

func TestLeafOrderingAndRanges(t *testing.T) {
	tr := exampleTree(t)
	want := []string{"JP", "KR", "DE", "FR"}
	if got := tr.Leaves(); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("leaves = %v, want %v", got, want)
	}
	ranges := map[string]Span{
		"FR": {3, 4},
		"DE": {2, 3},
		"JP": {0, 1},
		"KR": {1, 2},
		"EU": {2, 4},
		"AS": {0, 2},
		Root: {0, 4},
	}
	for code, wantSpan := range ranges {
		got, ok := tr.LeafRange(code)
		if !ok || got != wantSpan {
			t.Fatalf("LeafRange(%q) = %v,%v want %v", code, got, ok, wantSpan)
		}
	}
	if _, ok := tr.LeafRange("MARS"); ok {
		t.Fatal("unknown node must report false")
	}
	for _, leaf := range want {
		if !tr.IsLeaf(leaf) {
			t.Fatalf("%q should be leaf", leaf)
		}
	}
	for _, inner := range []string{"EU", "AS", Root} {
		if tr.IsLeaf(inner) {
			t.Fatalf("%q should not be leaf", inner)
		}
	}
	if tr.IsLeaf("MARS") {
		t.Fatal("unknown should not be leaf")
	}
}

func TestSingleRootIsLeaf(t *testing.T) {
	tr, err := NewTree(map[string][]string{Root: {}})
	if err != nil {
		t.Fatal(err)
	}
	if !tr.IsLeaf(Root) {
		t.Fatal("root with no children must be a leaf")
	}
	if got := tr.Leaves(); len(got) != 1 || got[0] != Root {
		t.Fatalf("leaves = %v", got)
	}
}

func TestDescendantAndCover(t *testing.T) {
	tr := exampleTree(t)
	if !tr.IsProperDescendant("FR", Root) || !tr.IsProperDescendant("FR", "EU") {
		t.Fatal("FR should descend from WORLD and EU")
	}
	if tr.IsProperDescendant("FR", "AS") || tr.IsProperDescendant("EU", "EU") || tr.IsProperDescendant("EU", "FR") {
		t.Fatal("invalid descendant relations accepted")
	}

	keep, holes := tr.Cover(Root, []string{"DE", "JP"})
	if fmt.Sprint(holes) != "[{0 1} {2 3}]" {
		t.Fatalf("holes = %v", holes)
	}
	if fmt.Sprint(keep) != "[{1 2} {3 4}]" {
		t.Fatalf("keep = %v", keep)
	}

	// 排除两个子节点使 EU 覆盖为空
	keepEU, _ := tr.Cover("EU", []string{"FR", "DE"})
	if len(keepEU) != 0 {
		t.Fatalf("EU minus FR,DE should be empty, got %v", keepEU)
	}

	// 题设反例：「欧洲排除法国」= [DE] 与「欧洲排除德国」= [FR] 无公共叶
	a, _ := tr.Cover("EU", []string{"FR"})
	b, _ := tr.Cover("EU", []string{"DE"})
	if spansOverlapNaive(a, b) {
		t.Fatal("EU\\FR and EU\\DE must share no leaf")
	}
}

func spansOverlapNaive(a, b []Span) bool {
	for _, x := range a {
		for i := x.Lo; i < x.Hi; i++ {
			for _, y := range b {
				if i >= y.Lo && i < y.Hi {
					return true
				}
			}
		}
	}
	return false
}
