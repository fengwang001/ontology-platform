package graph

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

func TestAddObjectValidation(t *testing.T) {
	g := New()
	if !errors.Is(g.AddObject(Object{Type: "T"}), ErrEmptyID) {
		t.Fatalf("empty id must be rejected")
	}
	if !errors.Is(g.AddObject(Object{ID: "o1"}), ErrEmptyType) {
		t.Fatalf("empty type must be rejected")
	}
	if err := g.AddObject(Object{ID: "o1", Type: "T"}); err != nil {
		t.Fatalf("add: %v", err)
	}
	if !errors.Is(g.AddLink(Link{Type: "e", Source: "o1", Target: "o1"}), ErrSelfLoop) {
		t.Fatalf("self loop must be rejected")
	}
	if !errors.Is(g.AddLink(Link{Type: "e", Source: "o1", Target: "missing"}), ErrMissingEndpoint) {
		t.Fatalf("missing endpoint must be rejected")
	}
}

func TestSnapshotDeterminism(t *testing.T) {
	orders := [][][2]string{
		{{"a", "b"}, {"b", "c"}, {"a", "c"}},
		{{"a", "c"}, {"a", "b"}, {"b", "c"}},
		{{"b", "c"}, {"a", "c"}, {"a", "b"}},
	}
	var baseline []string
	for i, order := range orders {
		g := New()
		for _, id := range []string{"a", "b", "c"} {
			if err := g.AddObject(Object{ID: id, Type: "N"}); err != nil {
				t.Fatalf("add %s: %v", id, err)
			}
		}
		for _, pair := range order {
			if err := g.AddLink(Link{Type: "rel", Source: pair[0], Target: pair[1]}); err != nil {
				t.Fatalf("link %v: %v", pair, err)
			}
		}
		snap := g.Snapshot().(*snapshot)
		got := make([]string, 0, len(snap.edges))
		for _, e := range snap.edges {
			got = append(got, e.source+"->"+e.target)
		}
		if i == 0 {
			baseline = got
			continue
		}
		if fmt.Sprint(got) != fmt.Sprint(baseline) {
			t.Fatalf("order %d: edges %v, want %v", i, got, baseline)
		}
	}
}

func TestSnapshotStableUnderWrites(t *testing.T) {
	g := New()
	for _, id := range []string{"a", "b"} {
		_ = g.AddObject(Object{ID: id, Type: "N"})
	}
	snap := g.Snapshot()
	_ = g.AddObject(Object{ID: "c", Type: "N"})
	_ = g.AddLink(Link{Type: "rel", Source: "a", Target: "b"})
	if _, ok := snap.Object("c"); ok {
		t.Fatalf("old snapshot must not observe later writes")
	}
	if len(snap.OutNeighbors("a", "rel")) != 0 {
		t.Fatalf("old snapshot must not observe later links")
	}
}

func TestAttributeDefensiveCopy(t *testing.T) {
	g := New()
	attrs := map[string]any{"v": 1}
	_ = g.AddObject(Object{ID: "o1", Type: "T", Attributes: attrs})
	attrs["v"] = 2
	obj, _ := g.Snapshot().Object("o1")
	if obj.Attributes["v"] != 1 {
		t.Fatalf("external attribute mutation leaked into graph: %v", obj.Attributes)
	}
}

func TestConcurrentReadsAndWrites(t *testing.T) {
	g := New()
	for _, id := range []string{"a", "b"} {
		_ = g.AddObject(Object{ID: id, Type: "N"})
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			id := fmt.Sprintf("n%d", i)
			_ = g.AddObject(Object{ID: id, Type: "N"})
			_ = g.AddLink(Link{Type: "rel", Source: "a", Target: id})
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			snap := g.Snapshot()
			_ = snap.ObjectsByType("N")
			_ = snap.OutNeighbors("a", "rel")
		}
	}()
	wg.Wait()
}
