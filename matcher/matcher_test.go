package matcher

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"testing"
)

func newTestMatcher(buf *bytes.Buffer) *Matcher {
	return NewMatcher(log.New(buf, "", 0))
}

// buildCompanyGraph:
//
//	alice(Manager, level=3) -MANAGES-> bob(Engineer, level=2)
//	carol(Manager, level=1) -MANAGES-> dave(Engineer, level=1)
//	bob -WORKS_WITH-> dave
func buildCompanyGraph() *Graph {
	g := NewGraph()
	g.AddObject(Object{ID: "alice", Type: "Manager", Attrs: map[string]any{"level": 3}})
	g.AddObject(Object{ID: "carol", Type: "Manager", Attrs: map[string]any{"level": 1}})
	g.AddObject(Object{ID: "bob", Type: "Engineer", Attrs: map[string]any{"level": 2}})
	g.AddObject(Object{ID: "dave", Type: "Engineer", Attrs: map[string]any{"level": 1}})
	g.AddEdge(Edge{Type: "MANAGES", FromID: "alice", ToID: "bob"})
	g.AddEdge(Edge{Type: "MANAGES", FromID: "carol", ToID: "dave"})
	g.AddEdge(Edge{Type: "WORKS_WITH", FromID: "bob", ToID: "dave"})
	return g
}

func TestBasicPatternMatch(t *testing.T) {
	var buf bytes.Buffer
	m := newTestMatcher(&buf)
	p := Pattern{
		Nodes: []NodePattern{
			{Var: "boss", Type: "Manager"},
			{Var: "eng", Type: "Engineer"},
		},
		Edges: []EdgePattern{
			{FromVar: "boss", Type: "MANAGES", ToVar: "eng"},
		},
	}
	matches, err := m.MatchAll(context.Background(), buildCompanyGraph(), p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertSameKeys(t, matchKeys(matches), []string{"boss=alice,eng=bob", "boss=carol,eng=dave"})

	logOut := buf.String()
	for _, fragment := range []string{"MATCH-START", "nodes[boss:Manager,eng:Engineer]", "edges[boss-MANAGES->eng]", "MATCH accept=", "MATCH-DONE"} {
		if !bytes.Contains(buf.Bytes(), []byte(fragment)) {
			t.Errorf("log missing %q\nlogs:\n%s", fragment, logOut)
		}
	}
}

func TestConstraintPruning(t *testing.T) {
	var buf bytes.Buffer
	m := newTestMatcher(&buf)
	p := Pattern{
		Nodes: []NodePattern{
			{Var: "boss", Type: "Manager", Constraints: []Constraint{{Attr: "level", Op: OpGt, Val: 2}}},
			{Var: "eng", Type: "Engineer", Constraints: []Constraint{{Attr: "level", Op: OpEq, Val: 2}}},
		},
		Edges: []EdgePattern{{FromVar: "boss", Type: "MANAGES", ToVar: "eng"}},
	}
	matches, err := m.MatchAll(context.Background(), buildCompanyGraph(), p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertSameKeys(t, matchKeys(matches), []string{"boss=alice,eng=bob"})

	logOut := buf.String()
	for _, fragment := range []string{
		"PRUNE var=boss candidate=carol basis=attr \"level\" 1 <= 2",
		"PRUNE var=eng candidate=dave basis=attr \"level\" 1 != 2",
	} {
		if !bytes.Contains(buf.Bytes(), []byte(fragment)) {
			t.Errorf("log missing pruning rationale %q\nlogs:\n%s", fragment, logOut)
		}
	}
}

func TestEdgePruning(t *testing.T) {
	var buf bytes.Buffer
	m := newTestMatcher(&buf)
	// Manager managing an engineer who works with another engineer:
	// only alice -> bob (bob WORKS_WITH dave) survives; carol's branch (dave)
	// has no WORKS_WITH peer and is removed via adjacency intersection.
	p := Pattern{
		Nodes: []NodePattern{
			{Var: "boss", Type: "Manager"},
			{Var: "eng", Type: "Engineer"},
			{Var: "peer", Type: "Engineer"},
		},
		Edges: []EdgePattern{
			{FromVar: "boss", Type: "MANAGES", ToVar: "eng"},
			{FromVar: "eng", Type: "WORKS_WITH", ToVar: "peer"},
		},
	}
	matches, err := m.MatchAll(context.Background(), buildCompanyGraph(), p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertSameKeys(t, matchKeys(matches), []string{"boss=alice,eng=bob,peer=dave"})
	if !bytes.Contains(buf.Bytes(), []byte("basis=type+constraints+edges consistent")) {
		t.Fatalf("log missing binding basis:\n%s", buf.String())
	}
}

func TestIsomorphicDedup(t *testing.T) {
	// Symmetric pattern (p -KNOW-> q AND q -KNOW-> p) over a triangle.
	// Every ordered pair satisfying both directions appears exactly once:
	// the injective binding plus dedup key guarantee no tuple repeats.
	g := NewGraph()
	for _, id := range []string{"a", "b", "c"} {
		g.AddObject(Object{ID: id, Type: "Person"})
	}
	pairs := [][2]string{{"a", "b"}, {"a", "c"}, {"b", "c"}}
	for _, pair := range pairs {
		g.AddEdge(Edge{Type: "KNOW", FromID: pair[0], ToID: pair[1]})
		g.AddEdge(Edge{Type: "KNOW", FromID: pair[1], ToID: pair[0]})
	}

	var buf bytes.Buffer
	m := newTestMatcher(&buf)
	p := Pattern{
		Nodes: []NodePattern{
			{Var: "p", Type: "Person"},
			{Var: "q", Type: "Person"},
		},
		Edges: []EdgePattern{
			{FromVar: "p", Type: "KNOW", ToVar: "q"},
			{FromVar: "q", Type: "KNOW", ToVar: "p"},
		},
	}
	matches, err := m.MatchAll(context.Background(), g, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := matchKeys(matches)
	if len(got) != 6 {
		t.Fatalf("expected 6 ordered pairs, got %d: %v", len(got), got)
	}
	seen := map[string]int{}
	for _, key := range got {
		seen[key]++
	}
	for key, count := range seen {
		if count != 1 {
			t.Errorf("isomorphic match %q returned %d times, want exactly 1", key, count)
		}
	}
}

func TestSameVariableSameObject(t *testing.T) {
	// Self edge x -LOOP-> x: one variable binds one object, and that same
	// object must satisfy both endpoints; x != y mappings are rejected.
	g := NewGraph()
	g.AddObject(Object{ID: "looper", Type: "Node"})
	g.AddObject(Object{ID: "plain", Type: "Node"})
	g.AddEdge(Edge{Type: "LOOP", FromID: "looper", ToID: "looper"})
	g.AddEdge(Edge{Type: "LOOP", FromID: "looper", ToID: "plain"})

	var buf bytes.Buffer
	m := newTestMatcher(&buf)
	p := Pattern{
		Nodes: []NodePattern{{Var: "x", Type: "Node"}},
		Edges: []EdgePattern{{FromVar: "x", Type: "LOOP", ToVar: "x"}},
	}
	matches, err := m.MatchAll(context.Background(), g, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertSameKeys(t, matchKeys(matches), []string{"x=looper"})
}

func TestDistinctVariablesNeverShareObject(t *testing.T) {
	// Edge p -LINK-> q with a self-loop a-LINK->a would, under homomorphism,
	// let p and q both bind a. Isomorphic matching requires injectivity, so
	// that binding is pruned and no match is returned.
	g := NewGraph()
	g.AddObject(Object{ID: "a", Type: "N"})
	g.AddObject(Object{ID: "b", Type: "N"})
	g.AddEdge(Edge{Type: "LINK", FromID: "a", ToID: "a"})

	var buf bytes.Buffer
	m := newTestMatcher(&buf)
	p := Pattern{
		Nodes: []NodePattern{{Var: "p", Type: "N"}, {Var: "q", Type: "N"}},
		Edges: []EdgePattern{{FromVar: "p", Type: "LINK", ToVar: "q"}},
	}
	matches, err := m.MatchAll(context.Background(), g, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("expected no injective match, got %v", matches)
	}
	if !bytes.Contains(buf.Bytes(), []byte("basis=object already bound to another variable")) {
		t.Fatalf("injectivity pruning not logged:\n%s", buf.String())
	}
}

func TestRejectionsAreDistinguishableAndReturnNoResults(t *testing.T) {
	cases := []struct {
		name     string
		pattern  Pattern
		kind     string
		sentinel error
	}{
		{
			name:     "degenerate full scan",
			pattern:  Pattern{},
			kind:     "degenerate_full_scan",
			sentinel: ErrDegenerateFullScan,
		},
		{
			name: "duplicate edge yields isomorphic repeats",
			pattern: Pattern{
				Nodes: []NodePattern{{Var: "a", Type: "T"}, {Var: "b", Type: "T"}},
				Edges: []EdgePattern{
					{FromVar: "a", Type: "E", ToVar: "b"},
					{FromVar: "a", Type: "E", ToVar: "b"},
				},
			},
			kind:     "isomorphic_duplicates",
			sentinel: ErrIsomorphicDuplicates,
		},
		{
			name: "same variable bound to different node types",
			pattern: Pattern{
				Nodes: []NodePattern{{Var: "x", Type: "T"}, {Var: "x", Type: "U"}},
			},
			kind:     "inconsistent_binding",
			sentinel: ErrInconsistentBinding,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			m := newTestMatcher(&buf)
			matches, err := m.MatchAll(context.Background(), buildCompanyGraph(), tc.pattern)
			if err == nil {
				t.Fatalf("expected rejection, got matches %v", matches)
			}
			if !errors.Is(err, tc.sentinel) {
				t.Fatalf("error %v does not wrap %v", err, tc.sentinel)
			}
			if got := RejectionKind(err); got != tc.kind {
				t.Fatalf("rejection kind = %q, want %q", got, tc.kind)
			}
			if matches != nil {
				t.Fatalf("rejected query returned partial results: %v", matches)
			}
			if !bytes.Contains(buf.Bytes(), []byte("REJECT")) || !bytes.Contains(buf.Bytes(), []byte("reason=")) {
				t.Fatalf("rejection not logged with reason:\n%s", buf.String())
			}
		})
	}
}

func TestConcurrentAndOrderIndependent(t *testing.T) {
	p := Pattern{
		Nodes: []NodePattern{
			{Var: "boss", Type: "Manager"},
			{Var: "eng", Type: "Engineer"},
		},
		Edges: []EdgePattern{{FromVar: "boss", Type: "MANAGES", ToVar: "eng"}},
	}

	nodes := []Object{
		{ID: "alice", Type: "Manager", Attrs: map[string]any{"level": 3}},
		{ID: "carol", Type: "Manager", Attrs: map[string]any{"level": 1}},
		{ID: "bob", Type: "Engineer", Attrs: map[string]any{"level": 2}},
		{ID: "dave", Type: "Engineer", Attrs: map[string]any{"level": 1}},
	}
	edges := []Edge{
		{Type: "MANAGES", FromID: "alice", ToID: "bob"},
		{Type: "MANAGES", FromID: "carol", ToID: "dave"},
		{Type: "WORKS_WITH", FromID: "bob", ToID: "dave"},
	}

	reference := []string{"boss=alice,eng=bob", "boss=carol,eng=dave"}
	rng := rand.New(rand.NewSource(42))

	for iteration := 0; iteration < 5; iteration++ {
		g := NewGraph()
		shuffledNodes := append([]Object(nil), nodes...)
		rng.Shuffle(len(shuffledNodes), func(i, j int) {
			shuffledNodes[i], shuffledNodes[j] = shuffledNodes[j], shuffledNodes[i]
		})
		for _, node := range shuffledNodes {
			g.AddObject(node)
		}
		shuffledEdges := append([]Edge(nil), edges...)
		rng.Shuffle(len(shuffledEdges), func(i, j int) {
			shuffledEdges[i], shuffledEdges[j] = shuffledEdges[j], shuffledEdges[i]
		})
		for _, edge := range shuffledEdges {
			g.AddEdge(edge)
		}

		const goroutines = 16
		var wg sync.WaitGroup
		errs := make(chan error, goroutines)
		for i := 0; i < goroutines; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				var local bytes.Buffer
				mm := NewMatcher(log.New(&local, "", 0))
				matches, err := mm.MatchAll(context.Background(), g, p)
				if err != nil {
					errs <- err
					return
				}
				got := matchKeys(matches)
				if len(got) != len(reference) {
					errs <- fmt.Errorf("got %v want %v", got, reference)
					return
				}
				for j := range got {
					if got[j] != reference[j] {
						errs <- fmt.Errorf("got %v want %v (order/content mismatch)", got, reference)
						return
					}
				}
			}()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Fatalf("iteration %d: %v", iteration, err)
		}
	}
}

func matchKeys(matches []Match) []string {
	keys := make([]string, 0, len(matches))
	for _, match := range matches {
		vars := make([]string, 0, len(match))
		for variable := range match {
			vars = append(vars, variable)
		}
		sort.Strings(vars)
		parts := make([]string, 0, len(vars))
		for _, variable := range vars {
			parts = append(parts, variable+"="+match[variable])
		}
		keys = append(keys, strings.Join(parts, ","))
	}
	sort.Strings(keys)
	return keys
}

func assertSameKeys(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}
