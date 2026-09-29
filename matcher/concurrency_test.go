package matcher

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"ontology/graph"
)

func workersPattern() *Pattern {
	return &Pattern{
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
}

// TestConcurrentMatch_SameGraphConsistentResults 并发匹配同一图，
// 所有调用必须得到完全一致的匹配集合。
func TestConcurrentMatch_SameGraphConsistentResults(t *testing.T) {
	g := buildDiamondGraph(t, diamondEdges())
	snap := g.Snapshot()
	p := workersPattern()

	const goroutines = 32
	var wg sync.WaitGroup
	results := make([][]Match, goroutines)
	errs := make(chan error, goroutines)
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(i int) {
			defer wg.Done()
			m := New()
			matches, _, err := m.Match(context.Background(), snap, p)
			if err != nil {
				errs <- err
				return
			}
			results[i] = matches
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent match: %v", err)
	}

	want := results[0]
	if len(want) == 0 {
		t.Fatalf("expected non-empty baseline result")
	}
	for i := 1; i < goroutines; i++ {
		if len(results[i]) != len(want) {
			t.Fatalf("goroutine %d got %d matches, want %d", i, len(results[i]), len(want))
		}
		for j := range want {
			if !sameBinding(results[i][j], want[j]) {
				t.Fatalf("goroutine %d match %d = %v, want %v", i, j, results[i][j], want[j])
			}
		}
	}
}

// TestMatch_IndependentOfEdgeInsertionOrder 同一图以任意边插入顺序构建，
// 匹配集合必须完全相同。
func TestMatch_IndependentOfEdgeInsertionOrder(t *testing.T) {
	orders := [][][3]string{
		{
			{"worksAt", "p1", "c1"},
			{"worksAt", "p2", "c1"},
			{"knows", "p1", "p2"},
		},
		{
			{"knows", "p1", "p2"},
			{"worksAt", "p2", "c1"},
			{"worksAt", "p1", "c1"},
		},
		{
			{"worksAt", "p2", "c1"},
			{"knows", "p1", "p2"},
			{"worksAt", "p1", "c1"},
		},
	}
	p := workersPattern()
	var baseline []Match
	for i, order := range orders {
		g := buildDiamondGraph(t, order)
		m := New()
		matches, _, err := m.Match(context.Background(), g.Snapshot(), p)
		if err != nil {
			t.Fatalf("order %d: %v", i, err)
		}
		if i == 0 {
			baseline = matches
			continue
		}
		if len(matches) != len(baseline) {
			t.Fatalf("order %d got %d matches, want %d", i, len(matches), len(baseline))
		}
		for j := range baseline {
			if !sameBinding(matches[j], baseline[j]) {
				t.Fatalf("order %d match %d = %v, want %v", i, j, matches[j], baseline[j])
			}
		}
	}
}

// TestConcurrentMatch_RaceSafe 在写入并发进行时匹配不发生数据竞争。
func TestConcurrentMatch_RaceSafe(t *testing.T) {
	g := buildDiamondGraph(t, diamondEdges())
	p := workersPattern()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			id := fmt.Sprintf("px%d", i)
			_ = g.AddObject(graph.Object{ID: id, Type: "Person"})
			_ = g.AddLink(graph.Link{Type: "worksAt", Source: id, Target: "c1"})
		}
	}()
	go func() {
		defer wg.Done()
		m := New()
		for i := 0; i < 50; i++ {
			_, _, _ = m.Match(context.Background(), g.Snapshot(), p)
		}
	}()
	wg.Wait()
}
